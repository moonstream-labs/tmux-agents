package claude

import (
	"testing"
	"time"

	"github.com/moonstream-labs/tmux-agents/internal/state"
)

func activeRow(t *testing.T, s *Store) state.PaneRow {
	t.Helper()
	rows := s.ActivePanes()
	if len(rows) != 1 {
		t.Fatalf("want exactly 1 active pane, got %d: %+v", len(rows), rows)
	}
	return rows[0]
}

func TestBindCreatesSessionOnPane(t *testing.T) {
	s := NewStore()
	sess, displaced, changed := s.Bind("sid", "/proj", "w:1.1", 42)
	if !changed || displaced != nil {
		t.Fatalf("first bind: changed=%v displaced=%v", changed, displaced)
	}
	if sess.ID != "sid" || sess.PaneTarget != "w:1.1" || sess.PID != 42 || sess.CWD != "/proj" {
		t.Fatalf("unexpected session: %+v", sess)
	}
	row := activeRow(t, s)
	if row.SessionID != "sid" || row.Target != "w:1.1" || row.State != state.StateIdle {
		t.Fatalf("unexpected row: %+v", row)
	}
}

func TestBindIsIdempotent(t *testing.T) {
	s := NewStore()
	s.Bind("sid", "/proj", "w:1.1", 42)
	_, displaced, changed := s.Bind("sid", "/proj", "w:1.1", 42)
	if changed || displaced != nil {
		t.Fatalf("re-bind of the same session: changed=%v displaced=%v", changed, displaced)
	}
}

// The incident: `claude --resume` runs under a throwaway session id while its
// picker is open. The scanner sees the pane then and binds the throwaway id;
// when a session is picked the process switches id. The pane must follow.
func TestBindDisplacesStaleSessionOnSamePane(t *testing.T) {
	s := NewStore()
	s.Bind("placeholder", "/home/u", "config:4.1", 100)

	sess, displaced, changed := s.Bind("real", "/home/u", "config:4.1", 100)
	if !changed {
		t.Fatal("switching the pane's session must report a change")
	}
	if displaced == nil || displaced.ID != "placeholder" {
		t.Fatalf("placeholder should be displaced, got %+v", displaced)
	}
	if sess.ID != "real" {
		t.Fatalf("bound session = %q, want real", sess.ID)
	}
	if _, ok := s.Get("placeholder"); ok {
		t.Fatal("placeholder must be gone from the store")
	}
	if row := activeRow(t, s); row.SessionID != "real" {
		t.Fatalf("pane shows %q, want real", row.SessionID)
	}
}

// A hook for a session not bound to a pane yet must be remembered, not dropped.
func TestHookStateSurvivesUntilBind(t *testing.T) {
	s := NewStore()
	if !s.Ensure("real", "/home/u") {
		t.Fatal("Ensure should create an unknown session")
	}
	if s.Ensure("real", "/home/u") {
		t.Fatal("Ensure must not re-create a known session")
	}
	if s.SetState("real", state.StateRunning) {
		t.Fatal("a pane-less session's state change is not visible")
	}
	if n := len(s.ActivePanes()); n != 0 {
		t.Fatalf("pane-less session must not appear in ActivePanes, got %d", n)
	}

	s.Bind("real", "/home/u", "config:4.1", 100)
	if row := activeRow(t, s); row.State != state.StateRunning {
		t.Fatalf("state after bind = %q, want running (the held hook state)", row.State)
	}
	if !s.SetState("real", state.StateIdle) {
		t.Fatal("a bound session's state change is visible")
	}
}

// The whole incident in order: placeholder bound, the real session's hook
// arrives, then the scanner re-checks the pane.
func TestIncidentPlaceholderThenHookThenRebind(t *testing.T) {
	s := NewStore()
	s.Bind("placeholder", "/home/u", "config:4.1", 100)

	// UserPromptSubmit for the real id (what the hook handler does).
	s.Ensure("real", "/home/u")
	s.SetState("real", state.StateRunning)
	if row := activeRow(t, s); row.SessionID != "placeholder" {
		t.Fatalf("before the re-check the pane still shows the placeholder, got %q", row.SessionID)
	}

	// Scanner re-check: the pane's process now reports the real id.
	_, displaced, _ := s.Bind("real", "/home/u", "config:4.1", 100)
	if displaced == nil || displaced.ID != "placeholder" {
		t.Fatalf("placeholder should be displaced, got %+v", displaced)
	}
	row := activeRow(t, s)
	if row.SessionID != "real" || row.State != state.StateRunning {
		t.Fatalf("pane = %q/%q, want real/running", row.SessionID, row.State)
	}
}

func TestBindFollowsSessionToNewTarget(t *testing.T) {
	s := NewStore()
	s.Bind("sid", "/proj", "w:2.1", 42)
	// renumber-windows rewrote the target.
	_, displaced, changed := s.Bind("sid", "/proj", "w:1.1", 42)
	if !changed || displaced != nil {
		t.Fatalf("retarget: changed=%v displaced=%v", changed, displaced)
	}
	if row := activeRow(t, s); row.Target != "w:1.1" {
		t.Fatalf("target = %q, want w:1.1", row.Target)
	}
}

func TestBindClaimsPendingNameByPane(t *testing.T) {
	s := NewStore()
	// Two wrapper launches in the same directory, different panes.
	s.AddPending("first", "w:1.1", "/proj")
	s.AddPending("second", "w:2.1", "/proj")

	sess, _, _ := s.Bind("b", "/proj", "w:2.1", 2)
	if sess.Name != "second" {
		t.Fatalf("pane w:2.1 got name %q, want second", sess.Name)
	}
	sess, _, _ = s.Bind("a", "/proj", "w:1.1", 1)
	if sess.Name != "first" {
		t.Fatalf("pane w:1.1 got name %q, want first", sess.Name)
	}

	// A session that already has a name leaves a pending registration alone.
	s.AddPending("unused", "w:3.1", "/proj")
	s.Ensure("c", "/proj")
	s.UpdateName("c", "titled")
	sess, _, _ = s.Bind("c", "/proj", "w:3.1", 3)
	if sess.Name != "titled" {
		t.Fatalf("named session was renamed to %q", sess.Name)
	}
}

func TestSessionForTarget(t *testing.T) {
	s := NewStore()
	if _, ok := s.SessionForTarget("w:1.1"); ok {
		t.Fatal("empty store should have no session for a target")
	}
	s.Bind("sid", "/proj", "w:1.1", 42)
	got, ok := s.SessionForTarget("w:1.1")
	if !ok || got.ID != "sid" || got.PID != 42 {
		t.Fatalf("SessionForTarget = %+v, %v", got, ok)
	}
}

func TestPrunePaneless(t *testing.T) {
	s := NewStore()
	s.Ensure("headless-old", "/x")
	s.Ensure("headless-fresh", "/x")
	s.Bind("bound-old", "/x", "w:1.1", 1)

	old := time.Now().Add(-time.Hour)
	s.sessions["headless-old"].UpdatedAt = old
	s.sessions["bound-old"].UpdatedAt = old

	if n := s.PrunePaneless(10 * time.Minute); n != 1 {
		t.Fatalf("pruned %d sessions, want 1", n)
	}
	if _, ok := s.Get("headless-old"); ok {
		t.Fatal("stale pane-less session should be pruned")
	}
	if _, ok := s.Get("headless-fresh"); !ok {
		t.Fatal("recent pane-less session must be kept")
	}
	if _, ok := s.Get("bound-old"); !ok {
		t.Fatal("a pane-bound session must never be pruned by age")
	}
}
