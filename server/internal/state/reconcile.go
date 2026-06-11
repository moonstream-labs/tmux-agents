package state

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/moonstream-labs/tmux-agents/internal/tmux"
)

// PaneProvider is implemented by each tool's session store. It reports only
// active panes; ended sessions are recorded via Reconciler.AddRecent and held
// in the shared Recents ring.
type PaneProvider interface {
	ActivePanes() []PaneRow
}

type Reconciler struct {
	mu        sync.Mutex
	db        *DB
	opts      *tmux.Options
	providers []PaneProvider
	recents   *Recents
	gen       int
	prevPill  map[Tool]string
	paneIDs   map[string]string // target→pane-id (%N), published by the scanner
}

func NewReconciler(db *DB, opts *tmux.Options) *Reconciler {
	gen, _ := opts.GetInt("@agents-gen")
	return &Reconciler{
		db:       db,
		opts:     opts,
		recents:  NewRecents(db, 0),
		gen:      gen,
		prevPill: make(map[Tool]string),
	}
}

func (r *Reconciler) RegisterProvider(p PaneProvider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers = append(r.providers, p)
}

// SetPaneIDs publishes the latest target→pane-id (%N) map from the scanner's
// list-panes pass. Reconcile() reads it to stamp PaneRow.PaneID with no
// per-event shell-out. Safe to call concurrently with Reconcile().
func (r *Reconciler) SetPaneIDs(m map[string]string) {
	r.mu.Lock()
	r.paneIDs = m
	r.mu.Unlock()
}

// AddRecent records an ended session in the recent list (most-recent first).
// paneTarget supplies the tmux session name; name/dir fall back to the
// session_names cache. No-op for an empty session id (e.g. an unbound
// placeholder). Call this before the follow-up Reconcile().
func (r *Reconciler) AddRecent(tool Tool, sessionID, name, dir, paneTarget string) {
	tmuxSession := paneTarget
	if before, _, ok := strings.Cut(paneTarget, ":"); ok {
		tmuxSession = before
	}
	r.recents.Add(tool, sessionID, name, dir, tmuxSession)
}

func (r *Reconciler) Reconcile() {
	r.mu.Lock()
	defer r.mu.Unlock()

	var allPanes []PaneRow
	for _, p := range r.providers {
		allPanes = append(allPanes, p.ActivePanes()...)
	}

	// Stamp pane ids (%N) from the scanner-published map (in-memory; no shell-out
	// on this hot path). A target missing from the map — including before the
	// first scan, when paneIDs is nil — leaves PaneID empty; navigate.sh's live
	// fallback covers that gap.
	for i := range allPanes {
		if pid := r.paneIDs[allPanes[i].Target]; pid != "" {
			allPanes[i].PaneID = pid
		}
	}

	allRecent := r.recents.Rows()

	// Compute per-tool pills.
	pills := make(map[Tool]string)
	for _, tool := range []Tool{ToolClaude, ToolOpenCode, ToolCodex} {
		pills[tool] = computePill(allPanes, tool)
	}

	changed := false
	for _, tool := range []Tool{ToolClaude, ToolOpenCode, ToolCodex} {
		if pills[tool] != r.prevPill[tool] {
			changed = true
			break
		}
	}

	// Cache any named sessions for future lookups.
	for i := range allPanes {
		p := &allPanes[i]
		if p.Name != "" && p.SessionID != "" {
			r.db.CacheName(p.Tool, p.SessionID, p.Name, p.Dir)
		}
		// Fill in names from cache if missing.
		if p.Name == "" && p.SessionID != "" {
			if name, dir := r.db.LookupName(p.Tool, p.SessionID); name != "" {
				p.Name = name
				if p.Dir == "" {
					p.Dir = dir
				}
			}
		}
	}

	// Always write DB snapshot if panes changed; only bump gen/pills if pill changed.
	if err := r.db.WriteSnapshot(allPanes, allRecent); err != nil {
		log.Printf("reconcile: db write error: %v", err)
		return
	}

	if changed {
		r.gen++
		for tool, pill := range pills {
			optName := fmt.Sprintf("@agents-%s-pill", tool)
			r.opts.Set(optName, pill)
			r.prevPill[tool] = pill
		}
		r.opts.Set("@agents-gen", fmt.Sprintf("%d", r.gen))
		r.opts.RefreshClients()
	}
}

func computePill(panes []PaneRow, tool Tool) string {
	var total, running, permission int
	for _, p := range panes {
		if p.Tool != tool {
			continue
		}
		total++
		switch p.State {
		case StatePermission:
			permission++
		case StateRunning:
			running++
		}
	}

	if total == 0 {
		return "idle|0"
	}
	if permission > 0 {
		return fmt.Sprintf("permission|%d", permission)
	}
	if running > 0 {
		return fmt.Sprintf("running|%d", running)
	}
	return fmt.Sprintf("active|%d", total)
}

// Heartbeat sets the server timestamp for external liveness checks.
func (r *Reconciler) Heartbeat() {
	r.opts.Set("@agents-server-ts", fmt.Sprintf("%d", time.Now().Unix()))
}
