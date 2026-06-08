package codex

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/moonstream-labs/tmux-agents/internal/state"
	"github.com/moonstream-labs/tmux-agents/internal/tmux"
)

// hookPayload is the JSON object Codex sends on stdin for each lifecycle hook.
// The shim (scripts/codex-hook.sh) forwards it verbatim as the request body and
// adds the pane id via the X-Tmux-Pane header. The schema deliberately mirrors
// Claude Code's hook payload.
type hookPayload struct {
	SessionID     string `json:"session_id"`
	CWD           string `json:"cwd"`
	HookEventName string `json:"hook_event_name"`
	ToolName      string `json:"tool_name,omitempty"`
}

// registerPayload is the pre-registration from the codex() wrapper. It carries
// a display name keyed by the launching pane (Codex itself has no session name).
type registerPayload struct {
	Name string `json:"name"`
	Pane string `json:"pane"` // $TMUX_PANE, e.g. "%81"
}

type Handler struct {
	store      *Store
	reconciler *state.Reconciler
}

func NewHandler(store *Store, reconciler *state.Reconciler) *Handler {
	return &Handler{store: store, reconciler: reconciler}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /codex/hook", h.handleHook)
	mux.HandleFunc("POST /codex/register", h.handleRegister)
}

func (h *Handler) handleRegister(w http.ResponseWriter, r *http.Request) {
	var payload registerPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	target := resolveTarget(payload.Pane)
	if target == "" {
		w.WriteHeader(http.StatusOK)
		return
	}
	h.store.AddPending(payload.Name, target)
	log.Printf("codex: pre-registered pane=%s name=%q", target, payload.Name)
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) handleHook(w http.ResponseWriter, r *http.Request) {
	pane := r.Header.Get("X-Tmux-Pane")

	var payload hookPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	// Codex command hooks run synchronously; answer immediately.
	w.WriteHeader(http.StatusOK)

	if payload.SessionID == "" {
		return
	}

	target := resolveTarget(pane)
	log.Printf("codex: event=%s session=%s pane=%q cwd=%s", payload.HookEventName, payload.SessionID, target, payload.CWD)

	// Any event upserts the session, so we self-heal after a server restart
	// even without a fresh SessionStart.
	_, created := h.store.Upsert(payload.SessionID, payload.CWD, target)

	stateChanged := false
	switch payload.HookEventName {
	case "SessionStart":
		stateChanged = h.store.SetState(payload.SessionID, state.StateIdle)

	case "UserPromptSubmit", "PreToolUse", "PostToolUse":
		stateChanged = h.store.SetState(payload.SessionID, state.StateRunning)

	case "PermissionRequest":
		stateChanged = h.store.SetState(payload.SessionID, state.StatePermission)

	case "Stop":
		stateChanged = h.store.SetState(payload.SessionID, state.StateIdle)

	default:
		// PreCompact, PostCompact, SubagentStart, SubagentStop — tracked for
		// pane/cwd freshness but no top-level state transition.
	}

	if created || stateChanged {
		h.reconciler.Reconcile()
	}
}

// resolveTarget converts a tmux pane id ($TMUX_PANE, e.g. "%81") into a
// session:window.pane target. Returns "" if the pane is unknown or empty.
func resolveTarget(pane string) string {
	if pane == "" {
		return ""
	}
	target, err := tmux.ResolvePaneTarget(pane)
	if err != nil {
		return ""
	}
	return target
}
