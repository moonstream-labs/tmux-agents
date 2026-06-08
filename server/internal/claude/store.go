package claude

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

// pendingReg is a pre-registration from the cc() wrapper,
// before the actual SessionStart hook fires.
type pendingReg struct {
	Name       string
	PaneTarget string
	CWD        string
	CreatedAt  time.Time
}

type Store struct {
	mu       sync.RWMutex
	sessions map[string]*Session // sessionID -> Session
	pending  []pendingReg
}

func NewStore() *Store {
	return &Store{
		sessions: make(map[string]*Session),
	}
}

func (s *Store) Get(sessionID string) (*Session, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sess, ok := s.sessions[sessionID]
	return sess, ok
}

func (s *Store) SetState(sessionID string, st state.SessionState) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[sessionID]
	if !ok {
		return false
	}
	sess.State = st
	sess.UpdatedAt = time.Now()
	return true
}

func (s *Store) Register(sessionID, cwd string) *Session {
	s.mu.Lock()
	defer s.mu.Unlock()

	if sess, ok := s.sessions[sessionID]; ok {
		sess.CWD = cwd
		sess.UpdatedAt = time.Now()
		return sess
	}

	sess := &Session{
		ID:        sessionID,
		CWD:       cwd,
		State:     state.StateIdle,
		UpdatedAt: time.Now(),
	}

	// Try to claim a pending registration.
	if reg, idx := s.matchPending(cwd); reg != nil {
		sess.Name = reg.Name
		sess.PaneTarget = reg.PaneTarget
		s.pending = append(s.pending[:idx], s.pending[idx+1:]...)
	}

	s.sessions[sessionID] = sess
	return sess
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

func (s *Store) UpdateName(sessionID, name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[sessionID]
	if !ok {
		return false
	}
	sess.Name = name
	sess.UpdatedAt = time.Now()
	return true
}

func (s *Store) SetPaneTarget(sessionID, target string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[sessionID]
	if !ok {
		return false
	}
	sess.PaneTarget = target
	return true
}

func (s *Store) AddPending(name, paneTarget, cwd string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending = append(s.pending, pendingReg{
		Name:       name,
		PaneTarget: paneTarget,
		CWD:        cwd,
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

// matchPending finds a pending registration by pane target or CWD.
// Caller must hold s.mu.
func (s *Store) matchPending(cwd string) (*pendingReg, int) {
	// Prefer most recent match.
	for i := len(s.pending) - 1; i >= 0; i-- {
		if s.pending[i].CWD == cwd {
			return &s.pending[i], i
		}
	}
	return nil, -1
}

func (s *Store) ActivePanes() []state.PaneRow {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows := make([]state.PaneRow, 0, len(s.sessions))
	for _, sess := range s.sessions {
		if sess.PaneTarget == "" {
			continue
		}
		rows = append(rows, state.PaneRow{
			Target:    sess.PaneTarget,
			Tool:      state.ToolClaude,
			State:     sess.State,
			SessionID: sess.ID,
			Name:      sess.Name,
			Dir:       sess.CWD,
			Updated:   sess.UpdatedAt.UnixMilli(),
			Host:      "local",
		})
	}
	return rows
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
