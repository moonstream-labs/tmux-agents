package state

import (
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/moonstream-labs/tmux-agents/internal/tmux"
)

// PaneProvider is implemented by each tool's session store.
type PaneProvider interface {
	ActivePanes() []PaneRow
	RecentSessions() []RecentRow
}

type Reconciler struct {
	mu        sync.Mutex
	db        *DB
	opts      *tmux.Options
	providers []PaneProvider
	gen       int
	prevPill  map[Tool]string
}

func NewReconciler(db *DB, opts *tmux.Options) *Reconciler {
	gen, _ := opts.GetInt("@agents-gen")
	return &Reconciler{
		db:       db,
		opts:     opts,
		gen:      gen,
		prevPill: make(map[Tool]string),
	}
}

func (r *Reconciler) RegisterProvider(p PaneProvider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers = append(r.providers, p)
}

func (r *Reconciler) Reconcile() {
	r.mu.Lock()
	defer r.mu.Unlock()

	var allPanes []PaneRow
	var allRecent []RecentRow

	for _, p := range r.providers {
		allPanes = append(allPanes, p.ActivePanes()...)
		allRecent = append(allRecent, p.RecentSessions()...)
	}

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
