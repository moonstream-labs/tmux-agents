package codex

import (
	"sync"
	"time"

	"github.com/moonstream-labs/tmux-agents/internal/state"
)

type Session struct {
	ID         string
	Name       string
	PaneTarget string
	CWD        string
	State      state.SessionState
	UpdatedAt  time.Time
}

// pendingReg is a pre-registration from the codex() wrapper, carrying a
// display name keyed by pane target, before the first hook fires.
type pendingReg struct {
	Name       string
	PaneTarget string
	CreatedAt  time.Time
}

type Store struct {
	mu sync.RWMutex
	// sessions are hook-bound (we know the real session id), keyed by id.
	sessions map[string]*Session
	// placeholders are codex panes the scanner discovered but no hook has bound
	// yet (resumed/idle sessions), keyed by pane target. They keep such panes
	// visible — and named, via the threads DB — until a hook arrives.
	placeholders map[string]*Session
	pending      []pendingReg
}

func NewStore() *Store {
	return &Store{
		sessions:     make(map[string]*Session),
		placeholders: make(map[string]*Session),
	}
}

func (s *Store) Get(sessionID string) (*Session, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sess, ok := s.sessions[sessionID]
	return sess, ok
}

// Upsert creates the session if unknown, refreshes its pane target and cwd, and
// returns the session and whether it was newly created. Codex hooks self-report
// the tmux pane (via $TMUX_PANE), so a session always carries its own target.
//
// When a hook binds a pane, any scanner placeholder for that pane is retired
// (its name is inherited if we don't have one), and a pending wrapper name is
// claimed.
func (s *Store) Upsert(sessionID, cwd, paneTarget string) (*Session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	sess, ok := s.sessions[sessionID]
	created := !ok
	if !ok {
		sess = &Session{
			ID:        sessionID,
			State:     state.StateIdle,
			UpdatedAt: time.Now(),
		}
		s.sessions[sessionID] = sess
	}

	if cwd != "" {
		sess.CWD = cwd
	}
	if paneTarget != "" {
		sess.PaneTarget = paneTarget
	}
	sess.UpdatedAt = time.Now()

	if sess.PaneTarget != "" {
		if ph, ok := s.placeholders[sess.PaneTarget]; ok {
			if sess.Name == "" {
				sess.Name = ph.Name
			}
			delete(s.placeholders, sess.PaneTarget)
		}
		if sess.Name == "" {
			if reg, idx := s.matchPending(sess.PaneTarget); reg != nil {
				sess.Name = reg.Name
				s.pending = append(s.pending[:idx], s.pending[idx+1:]...)
			}
		}
	}

	return sess, created
}

// SetState updates a hook-bound session's state, returning true only if it changed.
func (s *Store) SetState(sessionID string, st state.SessionState) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[sessionID]
	if !ok {
		return false
	}
	sess.UpdatedAt = time.Now()
	if sess.State == st {
		return false
	}
	sess.State = st
	return true
}

// SetName sets a hook-bound session's display name (e.g. from the threads DB).
// Returns true if it changed.
func (s *Store) SetName(sessionID, name string) bool {
	if name == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[sessionID]
	if !ok || sess.Name == name {
		return false
	}
	sess.Name = name
	return true
}

// SessionForTarget returns the hook-bound session occupying a pane target.
func (s *Store) SessionForTarget(target string) (*Session, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sessionForTargetLocked(target)
}

func (s *Store) sessionForTargetLocked(target string) (*Session, bool) {
	for _, sess := range s.sessions {
		if sess.PaneTarget == target {
			return sess, true
		}
	}
	return nil, false
}

// EnsurePlaceholder registers or refreshes a target-keyed placeholder for a
// codex pane the scanner found but no hook has bound yet. It is a no-op if a
// hook-bound session already owns the target. Returns true if anything changed.
func (s *Store) EnsurePlaceholder(target, cwd, name string) bool {
	if target == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.sessionForTargetLocked(target); ok {
		return false
	}

	if name == "" {
		if reg, _ := s.matchPending(target); reg != nil {
			name = reg.Name
		}
	}

	ph, ok := s.placeholders[target]
	if !ok {
		s.placeholders[target] = &Session{
			PaneTarget: target,
			CWD:        cwd,
			Name:       name,
			State:      state.StateIdle,
			UpdatedAt:  time.Now(),
		}
		return true
	}

	changed := false
	if cwd != "" && ph.CWD != cwd {
		ph.CWD, changed = cwd, true
	}
	if name != "" && ph.Name != name {
		ph.Name, changed = name, true
	}
	ph.UpdatedAt = time.Now()
	return changed
}

func (s *Store) Remove(sessionID string) *Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[sessionID]
	if !ok {
		return nil
	}
	delete(s.sessions, sessionID)
	return sess
}

// RemoveByTarget removes the hook-bound session or placeholder bound to a pane.
// Used by the pane scanner when a codex pane disappears (Codex has no
// SessionEnd hook).
func (s *Store) RemoveByTarget(target string) *Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, sess := range s.sessions {
		if sess.PaneTarget == target {
			delete(s.sessions, id)
			delete(s.placeholders, target)
			return sess
		}
	}
	if ph, ok := s.placeholders[target]; ok {
		delete(s.placeholders, target)
		return ph
	}
	return nil
}

func (s *Store) AddPending(name, paneTarget string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending = append(s.pending, pendingReg{
		Name:       name,
		PaneTarget: paneTarget,
		CreatedAt:  time.Now(),
	})
}

// PruneStalePending removes registrations older than the given duration.
func (s *Store) PruneStalePending(maxAge time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cutoff := time.Now().Add(-maxAge)
	filtered := s.pending[:0]
	for _, r := range s.pending {
		if r.CreatedAt.After(cutoff) {
			filtered = append(filtered, r)
		}
	}
	s.pending = filtered
}

// matchPending finds a pending registration by pane target (most recent first).
// Caller must hold s.mu.
func (s *Store) matchPending(paneTarget string) (*pendingReg, int) {
	for i := len(s.pending) - 1; i >= 0; i-- {
		if s.pending[i].PaneTarget == paneTarget {
			return &s.pending[i], i
		}
	}
	return nil, -1
}

// TrackedTargets returns every pane target the store tracks (hook-bound
// sessions and placeholders), for the scanner's prune pass.
func (s *Store) TrackedTargets() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	seen := make(map[string]bool)
	var out []string
	for _, sess := range s.sessions {
		if sess.PaneTarget != "" && !seen[sess.PaneTarget] {
			seen[sess.PaneTarget] = true
			out = append(out, sess.PaneTarget)
		}
	}
	for t := range s.placeholders {
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}

// ActivePanes emits one row per pane: hook-bound sessions, plus placeholders
// for panes no hook has bound yet.
func (s *Store) ActivePanes() []state.PaneRow {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows := make([]state.PaneRow, 0, len(s.sessions)+len(s.placeholders))
	covered := make(map[string]bool)
	for _, sess := range s.sessions {
		if sess.PaneTarget == "" {
			continue
		}
		covered[sess.PaneTarget] = true
		rows = append(rows, rowFrom(sess))
	}
	for target, ph := range s.placeholders {
		if covered[target] {
			continue
		}
		rows = append(rows, rowFrom(ph))
	}
	return rows
}

func rowFrom(sess *Session) state.PaneRow {
	return state.PaneRow{
		Target:    sess.PaneTarget,
		Tool:      state.ToolCodex,
		State:     sess.State,
		SessionID: sess.ID,
		Name:      sess.Name,
		Dir:       sess.CWD,
		Updated:   sess.UpdatedAt.UnixMilli(),
		Host:      "local",
	}
}

func (s *Store) RecentSessions() []state.RecentRow {
	// Recent sessions are not yet populated (shared limitation with the Claude
	// and OpenCode stores). Codex resume uses `codex resume <id>`.
	return nil
}

func (s *Store) All() []*Session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	all := make([]*Session, 0, len(s.sessions))
	for _, sess := range s.sessions {
		all = append(all, sess)
	}
	return all
}
