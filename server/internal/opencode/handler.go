package opencode

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"

	"github.com/moonstream-labs/tmux-agents/internal/state"
)

type registerPayload struct {
	Port       int    `json:"port"`
	PaneTarget string `json:"pane_target"`
	Name       string `json:"name"`
	SessionID  string `json:"session_id,omitempty"`
}

type Handler struct {
	store      *Store
	reconciler *state.Reconciler

	// Port allocation state.
	portMu   sync.Mutex
	nextPort int
	minPort  int
	maxPort  int
}

func NewHandler(store *Store, reconciler *state.Reconciler) *Handler {
	return &Handler{
		store:      store,
		reconciler: reconciler,
		nextPort:   10000,
		minPort:    10000,
		maxPort:    64999,
	}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /opencode/register", h.handleRegister)
	mux.HandleFunc("GET /opencode/port", h.handlePort)
}

func (h *Handler) handleRegister(w http.ResponseWriter, r *http.Request) {
	var payload registerPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	if payload.Port == 0 {
		http.Error(w, "port required", http.StatusBadRequest)
		return
	}

	// Detach from request context — the SSE goroutine should outlive the HTTP request.
	ctx, cancel := context.WithCancel(context.Background())

	inst := h.store.Register(payload.Port, payload.PaneTarget, payload.Name, cancel)
	if payload.SessionID != "" {
		inst.ActiveSessionID = payload.SessionID
	}
	log.Printf("opencode: registered port=%d pane=%s name=%q sid=%s", payload.Port, payload.PaneTarget, payload.Name, payload.SessionID)

	// Start SSE client goroutine.
	client := NewClient(payload.Port, h.store, h.reconciler)
	go func() {
		client.Run(ctx)
		// SSE loop ended — check if instance should be cleaned up.
		if ctx.Err() != nil {
			return // cancelled by us (deregistration)
		}
		// Connection dropped and context not cancelled — instance may have died.
		log.Printf("opencode: SSE loop ended for port=%d, removing instance", inst.Port)
		sid, name, dir, target, ok := h.store.RecentInfo(payload.Port)
		h.store.Remove(payload.Port)
		if ok {
			h.reconciler.AddRecent(state.ToolOpenCode, sid, name, dir, target)
		}
		h.reconciler.Reconcile()
	}()

	h.reconciler.Reconcile()
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) handlePort(w http.ResponseWriter, r *http.Request) {
	h.portMu.Lock()
	defer h.portMu.Unlock()

	usedPorts := make(map[int]bool)
	for _, p := range h.store.Ports() {
		usedPorts[p] = true
	}

	// Scan forward from nextPort to find an available one.
	for i := 0; i < (h.maxPort - h.minPort + 1); i++ {
		candidate := h.minPort + ((h.nextPort - h.minPort + i) % (h.maxPort - h.minPort + 1))
		if !usedPorts[candidate] {
			h.nextPort = candidate + 1
			if h.nextPort > h.maxPort {
				h.nextPort = h.minPort
			}
			w.Header().Set("Content-Type", "text/plain")
			fmt.Fprintf(w, "%d", candidate)
			return
		}
	}

	http.Error(w, "no ports available", http.StatusServiceUnavailable)
}
