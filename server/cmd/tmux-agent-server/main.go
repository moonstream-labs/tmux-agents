package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/moonstream-labs/tmux-agents/internal/claude"
	"github.com/moonstream-labs/tmux-agents/internal/codex"
	"github.com/moonstream-labs/tmux-agents/internal/opencode"
	"github.com/moonstream-labs/tmux-agents/internal/state"
	"github.com/moonstream-labs/tmux-agents/internal/tmux"
)

var (
	version = "dev"
)

func main() {
	port := flag.Int("port", 7077, "HTTP listen port")
	stateDir := flag.String("state-dir", "", "State directory (default: /tmp/tmux-agents-<uid>)")
	flag.Parse()

	if *stateDir == "" {
		*stateDir = fmt.Sprintf("/tmp/tmux-agents-%d", os.Getuid())
	}

	if err := os.MkdirAll(*stateDir, 0700); err != nil {
		log.Fatalf("failed to create state dir: %v", err)
	}

	dbPath := fmt.Sprintf("%s/state.db", *stateDir)
	db, err := state.OpenDB(dbPath)
	if err != nil {
		log.Fatalf("failed to open state db: %v", err)
	}

	opts := tmux.NewOptions()
	reconciler := state.NewReconciler(db, opts)

	// --- Claude Code ---
	claudeStore := claude.NewStore()
	reconciler.RegisterProvider(claudeStore)
	claudeHandler := claude.NewHandler(claudeStore, reconciler)

	// Recover existing Claude Code sessions.
	claude.ScanActiveSessions(claudeStore)
	for _, sess := range claudeStore.All() {
		if title, err := claude.ScanSessionTitle(sess.ID); err == nil && title != "" {
			claudeStore.UpdateName(sess.ID, title)
		}
	}

	// Start title watcher.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := claude.WatchTitles(ctx, claudeStore, reconciler); err != nil {
		log.Printf("warning: claude title watcher failed: %v", err)
	}

	// --- OpenCode ---
	opencodeStore := opencode.NewStore()
	reconciler.RegisterProvider(opencodeStore)
	opencodeHandler := opencode.NewHandler(opencodeStore, reconciler)

	// --- Codex ---
	codexStore := codex.NewStore()
	codexThreads := codex.NewThreadReader()
	reconciler.RegisterProvider(codexStore)
	codexHandler := codex.NewHandler(codexStore, reconciler)

	// Initial reconcile after startup recovery.
	reconciler.Reconcile()

	// --- HTTP ---
	mux := http.NewServeMux()

	claudeHandler.RegisterRoutes(mux)
	opencodeHandler.RegisterRoutes(mux)
	codexHandler.RegisterRoutes(mux)

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"status":             "ok",
			"version":            version,
			"uptime":             time.Since(startTime).Seconds(),
			"claude_sessions":    len(claudeStore.All()),
			"opencode_instances": len(opencodeStore.Ports()),
			"codex_sessions":     len(codexStore.All()),
		})
	})

	addr := fmt.Sprintf("127.0.0.1:%d", *port)
	srv := &http.Server{
		Addr:    addr,
		Handler: mux,
		BaseContext: func(l net.Listener) context.Context {
			return ctx
		},
	}

	// Heartbeat goroutine.
	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				reconciler.Heartbeat()
			}
		}
	}()

	// Prune stale pending registrations periodically.
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				claudeStore.PruneStalePending(2 * time.Minute)
				codexStore.PruneStalePending(2 * time.Minute)
			}
		}
	}()

	// Pane scanner: discover unwrapped sessions, prune dead panes.
	go tmux.RunScanner(ctx, 5*time.Second, tmux.ScannerCallbacks{
		IsClaudeTracked: func(target string) bool {
			for _, s := range claudeStore.All() {
				if s.PaneTarget == target {
					return true
				}
			}
			return false
		},
		IsOpenCodeTracked: func(target string) bool {
			for _, p := range opencodeStore.Ports() {
				inst, ok := opencodeStore.Get(p)
				if ok && inst.PaneTarget == target {
					return true
				}
			}
			return false
		},
		OnClaudeDiscovered: func(target string, pid int) bool {
			// The pane PID is the shell; claude is a child. Try the pane PID
			// itself first (when claude is the direct pane command), then
			// search child PIDs for a matching session file.
			sf := tmux.FindClaudeSessionForPaneTree(pid)
			if sf == nil {
				return false
			}

			// Check if we already know this session (e.g., from startup recovery)
			// and just need to assign the pane target.
			if existing, ok := claudeStore.Get(sf.SessionID); ok {
				if existing.PaneTarget == "" {
					claudeStore.SetPaneTarget(sf.SessionID, target)
					reconciler.Reconcile()
					log.Printf("scanner: assigned pane=%s to existing claude session=%s", target, sf.SessionID)
				}
				return true
			}

			sess := claudeStore.Register(sf.SessionID, sf.CWD)
			sess.PaneTarget = target
			if title, err := claude.ScanSessionTitle(sf.SessionID); err == nil && title != "" {
				claudeStore.UpdateName(sf.SessionID, title)
			}
			reconciler.Reconcile()
			log.Printf("scanner: discovered claude session=%s pane=%s", sf.SessionID, target)
			return true
		},
		OnOpenCodeDiscovered: func(target string, port int) bool {
			ocCtx, ocCancel := context.WithCancel(ctx)
			opencodeStore.Register(port, target, "", ocCancel)
			client := opencode.NewClient(port, opencodeStore, reconciler)
			go func() {
				client.Run(ocCtx)
				if ocCtx.Err() == nil {
					opencodeStore.Remove(port)
					reconciler.Reconcile()
				}
			}()
			reconciler.Reconcile()
			log.Printf("scanner: discovered opencode port=%d pane=%s", port, target)
			return true
		},
		OnCodexDiscovered: func(target, cwd string, pid int) {
			changed := false
			// Resolve the exact session id from the codex process's open rollout
			// file — avoids cwd ambiguity when sessions share a directory.
			sid := tmux.FindCodexSessionForPaneTree(pid)
			if sid != "" {
				sess, ok := codexStore.SessionForTarget(target)
				if !ok || sess.ID != sid {
					// New pane, or a different session resumed into it: (re)bind.
					if ok {
						codexStore.RemoveByTarget(target)
					}
					codexStore.Upsert(sid, cwd, target) // claims a wrapper-pending name, if any
					changed = true
				}
				// Name the session from Codex's threads DB. A `/rename` (or a
				// Codex-generated summary) — a title NOT derived from the first user
				// message — is adopted and cached. Codex resets the title to the
				// first-message form on resume; for that auto/reset form we restore
				// the last cached custom name instead, so a rename stays stable
				// across exit/resume.
				if th, ok := codexThreads.ByID(sid); ok {
					if !th.IsAutoTitle() {
						if codexStore.SetName(sid, th.Title) {
							changed = true
						}
					} else {
						name, _ := db.LookupName(state.ToolCodex, sid)
						if name == "" {
							name = th.Title
						}
						if codexStore.SetName(sid, name) {
							changed = true
						}
					}
				}
			} else if _, ok := codexStore.SessionForTarget(target); !ok {
				// Couldn't read the rollout fd — show a bare (unnamed) placeholder
				// rather than risk a wrong cwd-matched title.
				changed = codexStore.EnsurePlaceholder(target, cwd, "")
			}
			if changed {
				reconciler.Reconcile()
			}
		},
		GetTrackedTargets: func() []string {
			var targets []string
			for _, s := range claudeStore.All() {
				if s.PaneTarget != "" {
					targets = append(targets, s.PaneTarget)
				}
			}
			for _, p := range opencodeStore.Ports() {
				if inst, ok := opencodeStore.Get(p); ok {
					targets = append(targets, inst.PaneTarget)
				}
			}
			targets = append(targets, codexStore.TrackedTargets()...)
			return targets
		},
		OnPaneGone: func(target string) {
			// Check Claude store.
			for _, s := range claudeStore.All() {
				if s.PaneTarget == target {
					reconciler.AddRecent(state.ToolClaude, s.ID, s.Name, s.CWD, s.PaneTarget)
					claudeStore.Remove(s.ID)
					log.Printf("scanner: pruned claude session=%s pane=%s", s.ID, target)
					reconciler.Reconcile()
					return
				}
			}
			// Check OpenCode store.
			for _, p := range opencodeStore.Ports() {
				if inst, ok := opencodeStore.Get(p); ok && inst.PaneTarget == target {
					sid, name, dir, tgt, rok := opencodeStore.RecentInfo(p)
					opencodeStore.Remove(p)
					if rok {
						reconciler.AddRecent(state.ToolOpenCode, sid, name, dir, tgt)
					}
					log.Printf("scanner: pruned opencode port=%d pane=%s", p, target)
					reconciler.Reconcile()
					return
				}
			}
			// Check Codex store.
			if sess := codexStore.RemoveByTarget(target); sess != nil {
				reconciler.AddRecent(state.ToolCodex, sess.ID, sess.Name, sess.CWD, sess.PaneTarget)
				log.Printf("scanner: pruned codex session=%s pane=%s", sess.ID, target)
				reconciler.Reconcile()
				return
			}
		},
	})

	go func() {
		log.Printf("tmux-agent-server %s listening on %s (state: %s)", version, addr, dbPath)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("shutting down...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown error: %v", err)
	}

	db.Close()
	log.Println("stopped")
}

var startTime = time.Now()
