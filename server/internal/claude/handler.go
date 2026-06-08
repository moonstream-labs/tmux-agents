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

type Handler struct {
	store      *Store
	reconciler *state.Reconciler
}

func NewHandler(store *Store, reconciler *state.Reconciler) *Handler {
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

	case "UserPromptSubmit":
		changed = h.store.SetState(payload.SessionID, state.StateRunning)

	case "PreToolUse":
		changed = h.store.SetState(payload.SessionID, state.StateRunning)

	case "PermissionRequest":
		changed = h.store.SetState(payload.SessionID, state.StatePermission)

	case "Notification":
		// Only transition on permission_prompt notifications.
		// The payload for Notification hooks includes the notification type
		// in the matcher context, but the JSON body may not have it directly.
		// Claude Code fires the hook only when the matcher matches, so if we
		// configured matcher: "permission_prompt", we know this is a permission.
		changed = h.store.SetState(payload.SessionID, state.StatePermission)

	case "Stop":
		changed = h.store.SetState(payload.SessionID, state.StateIdle)

	case "SessionEnd":
		if sess := h.store.Remove(payload.SessionID); sess != nil {
			h.reconciler.AddRecent(state.ToolClaude, sess.ID, sess.Name, sess.CWD, sess.PaneTarget)
			log.Printf("claude: session end id=%s name=%q", payload.SessionID, sess.Name)
			changed = true
		}

	default:
		log.Printf("claude: unhandled hook event: %s (session=%s)", payload.HookEventName, payload.SessionID)
	}

	if changed {
		h.reconciler.Reconcile()
	}
}
