package claude

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/moonstream-labs/tmux-agents/internal/state"
)

// hookPayload is the JSON structure Claude Code sends to HTTP hooks.
type hookPayload struct {
	SessionID      string `json:"session_id"`
	CWD            string `json:"cwd"`
	HookEventName  string `json:"hook_event_name"`
	ToolName       string `json:"tool_name,omitempty"`
	PermissionMode string `json:"permission_mode,omitempty"`
	// Notification-specific
	NotificationType string `json:"type,omitempty"`
}

type registerPayload struct {
	Name       string `json:"name"`
	PaneTarget string `json:"pane_target"`
	CWD        string `json:"cwd"`
}

// Reconciler is the part of state.Reconciler the handler uses. It is an
// interface so tests can substitute a fake: the real reconciler writes tmux
// options on the live server.
type Reconciler interface {
	Reconcile()
	AddRecent(tool state.Tool, sessionID, name, dir, paneTarget string)
}

type Handler struct {
	store      *Store
	reconciler Reconciler
}

func NewHandler(store *Store, reconciler Reconciler) *Handler {
	return &Handler{store: store, reconciler: reconciler}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /claude/hook", h.handleHook)
	mux.HandleFunc("POST /claude/register", h.handleRegister)
}

func (h *Handler) handleRegister(w http.ResponseWriter, r *http.Request) {
	var payload registerPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	h.store.AddPending(payload.Name, payload.PaneTarget, payload.CWD)
	log.Printf("claude: pre-registered pane=%s name=%q cwd=%s", payload.PaneTarget, payload.Name, payload.CWD)
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) handleHook(w http.ResponseWriter, r *http.Request) {
	var payload hookPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	// Always return 200 immediately — hooks are async.
	w.WriteHeader(http.StatusOK)

	if payload.SessionID == "" {
		return
	}

	changed := false

	switch payload.HookEventName {
	case "SessionStart":
		sess := h.store.Register(payload.SessionID, payload.CWD)
		log.Printf("claude: session start id=%s name=%q pane=%s cwd=%s",
			payload.SessionID, sess.Name, sess.PaneTarget, payload.CWD)
		changed = true

	case "UserPromptSubmit", "PreToolUse":
		changed = h.setState(payload, state.StateRunning)

	case "PermissionRequest":
		changed = h.setState(payload, state.StatePermission)

	case "Notification":
		// Only transition on permission_prompt notifications.
		// The payload for Notification hooks includes the notification type
		// in the matcher context, but the JSON body may not have it directly.
		// Claude Code fires the hook only when the matcher matches, so if we
		// configured matcher: "permission_prompt", we know this is a permission.
		changed = h.setState(payload, state.StatePermission)

	case "Stop":
		changed = h.setState(payload, state.StateIdle)

	case "SessionEnd":
		if sess := h.store.Remove(payload.SessionID); sess != nil {
			// Only a session that was visible in a pane belongs in Recent. A
			// pane-less one is a session running outside tmux (or headless) that
			// hooks made known; listing it would fill Recent with noise.
			if sess.PaneTarget != "" {
				h.reconciler.AddRecent(state.ToolClaude, sess.ID, sess.Name, sess.CWD, sess.PaneTarget)
				changed = true
			}
			log.Printf("claude: session end id=%s name=%q pane=%q", payload.SessionID, sess.Name, sess.PaneTarget)
		}

	default:
		log.Printf("claude: unhandled hook event: %s (session=%s)", payload.HookEventName, payload.SessionID)
	}

	if changed {
		h.reconciler.Reconcile()
	}
}

// setState applies a hook's state transition and reports whether it is visible
// (see Store.SetState).
//
// A hook can arrive for a session the server has not bound to a pane: the
// scanner is the only thing that binds panes, it runs on an interval, and a
// claude process can switch session id after it was first seen (`--resume`
// picker, /clear). Such an event used to be dropped, leaving the pane stuck on
// its old state. Instead the session is created pane-less and remembers the
// state; the scanner's next pass binds the pane with that state intact.
func (h *Handler) setState(p hookPayload, st state.SessionState) bool {
	if h.store.Ensure(p.SessionID, p.CWD) {
		log.Printf("claude: hook %s for unbound session id=%s (held until its pane is bound)",
			p.HookEventName, p.SessionID)
	}
	return h.store.SetState(p.SessionID, st)
}
