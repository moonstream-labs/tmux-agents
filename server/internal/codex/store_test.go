package codex

import (
	"testing"
	"time"

	"github.com/moonstream-labs/tmux-agents/internal/state"
)

func TestUpsertCreatesAndUpdates(t *testing.T) {
	s := NewStore()

	sess, created := s.Upsert("sid-1", "/work", "main:1.0")
	if !created {
		t.Fatal("first Upsert should report created")
	}
	if sess.State != state.StateIdle {
		t.Fatalf("new session should start idle, got %q", sess.State)
	}
	if sess.PaneTarget != "main:1.0" || sess.CWD != "/work" {
		t.Fatalf("pane/cwd not set: %+v", sess)
	}

	_, created = s.Upsert("sid-1", "", "")
	if created {
		t.Fatal("second Upsert should not report created")
	}
	// Empty cwd/pane must not clobber existing values.
	if got, _ := s.Get("sid-1"); got.PaneTarget != "main:1.0" || got.CWD != "/work" {
		t.Fatalf("empty Upsert clobbered values: %+v", got)
	}
}

func TestPendingNameClaimedByPane(t *testing.T) {
	s := NewStore()
	s.AddPending("my-feature", "main:2.1")

	sess, _ := s.Upsert("sid-2", "/repo", "main:2.1")
	if sess.Name != "my-feature" {
		t.Fatalf("pending name not claimed, got %q", sess.Name)
	}

	// The pending entry must be consumed (a second session on the same pane
	// should not inherit the now-claimed name).
	other, _ := s.Upsert("sid-3", "/repo", "main:2.1")
	if other.Name != "" {
		t.Fatalf("pending name claimed twice, got %q", other.Name)
	}
}

func TestSetStateReportsChange(t *testing.T) {
	s := NewStore()
	s.Upsert("sid", "/w", "x:0.0")

	if !s.SetState("sid", state.StateRunning) {
		t.Fatal("idle->running should report changed")
	}
	if s.SetState("sid", state.StateRunning) {
		t.Fatal("running->running should report unchanged")
	}
	if !s.SetState("sid", state.StatePermission) {
		t.Fatal("running->permission should report changed")
	}
	if s.SetState("missing", state.StateIdle) {
		t.Fatal("SetState on unknown session should report unchanged")
	}
}

func TestActivePanesSkipsPanelessSessions(t *testing.T) {
	s := NewStore()
	s.Upsert("with-pane", "/w", "sess:0.0")
	// A session that never received a resolvable pane.
	s.Upsert("no-pane", "/w", "")

	rows := s.ActivePanes()
	if len(rows) != 1 {
		t.Fatalf("expected 1 active pane, got %d", len(rows))
	}
	r := rows[0]
	if r.Tool != state.ToolCodex || r.SessionID != "with-pane" || r.Target != "sess:0.0" {
		t.Fatalf("unexpected row: %+v", r)
	}
	if r.Host != "local" {
		t.Fatalf("expected host=local, got %q", r.Host)
	}
}

func TestRemoveByTarget(t *testing.T) {
	s := NewStore()
	s.Upsert("sid", "/w", "kill:3.2")

	if got := s.RemoveByTarget("nope:0.0"); got != nil {
		t.Fatal("RemoveByTarget on unknown target should return nil")
	}
	got := s.RemoveByTarget("kill:3.2")
	if got == nil || got.ID != "sid" {
		t.Fatalf("RemoveByTarget did not return the session: %+v", got)
	}
	if _, ok := s.Get("sid"); ok {
		t.Fatal("session should be gone after RemoveByTarget")
	}
}

func TestPruneStalePending(t *testing.T) {
	s := NewStore()
	s.AddPending("fresh", "a:0.0")
	// Force one entry to look old.
	s.pending[0].CreatedAt = time.Now().Add(-time.Hour)

	s.PruneStalePending(2 * time.Minute)
	if len(s.pending) != 0 {
		t.Fatalf("stale pending not pruned: %d remain", len(s.pending))
	}
}

func TestPlaceholderSupersededByHook(t *testing.T) {
	s := NewStore()

	// Scanner discovers an idle/resumed codex pane (no hook yet).
	if !s.EnsurePlaceholder("sess:1.0", "/repo", "Fix parser") {
		t.Fatal("first EnsurePlaceholder should report changed")
	}
	rows := s.ActivePanes()
	if len(rows) != 1 || rows[0].SessionID != "" || rows[0].Name != "Fix parser" || rows[0].Target != "sess:1.0" {
		t.Fatalf("placeholder row wrong: %+v", rows)
	}
	if s.EnsurePlaceholder("sess:1.0", "/repo", "Fix parser") {
		t.Fatal("unchanged EnsurePlaceholder should report no change")
	}

	// A hook binds the pane: placeholder retired, name inherited, no double-count.
	sess, created := s.Upsert("sid-x", "/repo", "sess:1.0")
	if !created || sess.Name != "Fix parser" {
		t.Fatalf("hook upsert should inherit placeholder name: %+v", sess)
	}
	rows = s.ActivePanes()
	if len(rows) != 1 {
		t.Fatalf("expected 1 row after supersede (no double-count), got %d", len(rows))
	}
	if rows[0].SessionID != "sid-x" {
		t.Fatalf("expected the hook-bound row, got %+v", rows[0])
	}
}

func TestEnsurePlaceholderNoopWhenHookBound(t *testing.T) {
	s := NewStore()
	s.Upsert("sid", "/w", "x:0.0")
	if s.EnsurePlaceholder("x:0.0", "/w", "title") {
		t.Fatal("EnsurePlaceholder must no-op when a hook session owns the target")
	}
	if len(s.ActivePanes()) != 1 {
		t.Fatal("should still be exactly one row")
	}
}

func TestRemoveByTargetPlaceholder(t *testing.T) {
	s := NewStore()
	s.EnsurePlaceholder("p:2.1", "/w", "x")
	if got := s.RemoveByTarget("p:2.1"); got == nil {
		t.Fatal("RemoveByTarget should remove a placeholder")
	}
	if len(s.ActivePanes()) != 0 {
		t.Fatal("placeholder should be gone")
	}
}

func TestTrackedTargetsIncludesPlaceholders(t *testing.T) {
	s := NewStore()
	s.Upsert("sid", "/w", "real:0.0")
	s.EnsurePlaceholder("ph:1.0", "/w", "x")
	got := map[string]bool{}
	for _, tgt := range s.TrackedTargets() {
		got[tgt] = true
	}
	if !got["real:0.0"] || !got["ph:1.0"] {
		t.Fatalf("TrackedTargets missing entries: %v", got)
	}
}

func TestSetName(t *testing.T) {
	s := NewStore()
	s.Upsert("sid", "/w", "x:0.0")
	if !s.SetName("sid", "Renamed") {
		t.Fatal("SetName should report change")
	}
	if s.SetName("sid", "Renamed") {
		t.Fatal("SetName with same value should report no change")
	}
	if sess, _ := s.Get("sid"); sess.Name != "Renamed" {
		t.Fatalf("name not set: %q", sess.Name)
	}
}
