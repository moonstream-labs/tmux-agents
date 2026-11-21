package claude

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/moonstream-labs/tmux-agents/internal/state"
)

// fakeReconciler stands in for state.Reconciler, which would write tmux options
// on the live server.
type fakeReconciler struct {
	reconciles int
	recents    []string
}

func (f *fakeReconciler) Reconcile() { f.reconciles++ }

func (f *fakeReconciler) AddRecent(_ state.Tool, sessionID, _, _, _ string) {
	f.recents = append(f.recents, sessionID)
}

func postHook(t *testing.T, h *Handler, event, sessionID string) {
	t.Helper()
	body := fmt.Sprintf(`{"session_id":%q,"cwd":"/home/u","hook_event_name":%q}`, sessionID, event)
	req := httptest.NewRequest(http.MethodPost, "/claude/hook", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.handleHook(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s for %s: status %d", event, sessionID, rec.Code)
	}
}

func TestHookForUnboundSessionIsHeldNotDropped(t *testing.T) {
	store, rec := NewStore(), &fakeReconciler{}
	h := NewHandler(store, rec)

	postHook(t, h, "UserPromptSubmit", "real")

	if _, ok := store.Get("real"); !ok {
		t.Fatal("a hook for an unknown session must create it, not be dropped")
	}
	if rec.reconciles != 0 {
		t.Fatalf("a pane-less session is not visible; reconciled %d times", rec.reconciles)
	}

	// The scanner binds the pane on its next pass; the state must already be right.
	store.Bind("real", "/home/u", "w:1.1", 1)
	if row := activeRow(t, store); row.State != state.StateRunning {
		t.Fatalf("state after bind = %q, want running", row.State)
	}
}

// The incident end to end through the handler: the pane is bound to the
// `--resume` placeholder id, and every hook arrives under the real id.
func TestHooksUnderNewSessionIDReachThePaneAfterRebind(t *testing.T) {
	store, rec := NewStore(), &fakeReconciler{}
	h := NewHandler(store, rec)
	store.Bind("placeholder", "/home/u", "config:4.1", 100)

	postHook(t, h, "UserPromptSubmit", "real")
	postHook(t, h, "PreToolUse", "real")

	_, displaced, _ := store.Bind("real", "/home/u", "config:4.1", 100) // scanner re-check
	if displaced == nil || displaced.ID != "placeholder" {
		t.Fatalf("placeholder should be displaced, got %+v", displaced)
	}
	if row := activeRow(t, store); row.SessionID != "real" || row.State != state.StateRunning {
		t.Fatalf("pane = %q/%q, want real/running", row.SessionID, row.State)
	}

	before := rec.reconciles
	postHook(t, h, "Stop", "real")
	if rec.reconciles != before+1 {
		t.Fatal("a hook for the now-bound session must reconcile")
	}
	if row := activeRow(t, store); row.State != state.StateIdle {
		t.Fatalf("state after Stop = %q, want idle", row.State)
	}
}

func TestPermissionHooksSetPermissionState(t *testing.T) {
	for _, event := range []string{"PermissionRequest", "Notification"} {
		store, rec := NewStore(), &fakeReconciler{}
		h := NewHandler(store, rec)
		store.Bind("sid", "/home/u", "w:1.1", 1)

		postHook(t, h, event, "sid")
		if row := activeRow(t, store); row.State != state.StatePermission {
			t.Fatalf("%s: state = %q, want permission", event, row.State)
		}
	}
}

func TestSessionEndRecordsOnlyVisibleSessions(t *testing.T) {
	store, rec := NewStore(), &fakeReconciler{}
	h := NewHandler(store, rec)

	// A session running outside tmux: known through hooks, never bound.
	postHook(t, h, "UserPromptSubmit", "headless")
	postHook(t, h, "SessionEnd", "headless")
	if _, ok := store.Get("headless"); ok {
		t.Fatal("SessionEnd must remove the session")
	}
	if len(rec.recents) != 0 {
		t.Fatalf("a pane-less session must not be added to Recent, got %v", rec.recents)
	}

	store.Bind("visible", "/home/u", "w:1.1", 1)
	postHook(t, h, "SessionEnd", "visible")
	if len(rec.recents) != 1 || rec.recents[0] != "visible" {
		t.Fatalf("recents = %v, want [visible]", rec.recents)
	}
	if len(store.ActivePanes()) != 0 {
		t.Fatal("ended session must leave ActivePanes")
	}
}

func TestSessionEndForUnknownSessionIsNoop(t *testing.T) {
	store, rec := NewStore(), &fakeReconciler{}
	h := NewHandler(store, rec)

	postHook(t, h, "SessionEnd", "never-seen")
	if rec.reconciles != 0 || len(rec.recents) != 0 {
		t.Fatalf("unknown SessionEnd: reconciles=%d recents=%v", rec.reconciles, rec.recents)
	}
	if _, ok := store.Get("never-seen"); ok {
		t.Fatal("SessionEnd must not create a session")
	}
}
