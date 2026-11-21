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
	// PID is the claude process whose ~/.claude/sessions/<pid>.json named this
	// session (0 if unknown). The scanner re-reads that file each pass to notice
	// the process switching to a different session.
	PID       int
	State     state.SessionState
	UpdatedAt time.Time
}

// pendingReg is a pre-registration from the claude() wrapper, made before the
// session is known to the server. Bind claims it by pane target.
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

// SetState records a session's state. It reports whether the change is visible
// -- i.e. the session is bound to a pane and so appears in ActivePanes. A
// session that is known but not yet bound still remembers the state (returning
// false), so it is correct the moment the scanner binds its pane.
func (s *Store) SetState(sessionID string, st state.SessionState) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[sessionID]
	if !ok {
		return false
	}
	sess.State = st
	sess.UpdatedAt = time.Now()
	return sess.PaneTarget != ""
}

// Ensure makes sure a session exists for sessionID, creating a pane-less one if
// it is unknown, and reports whether it was created. Hook events call this so
// that an event for a session the scanner has not bound yet is held rather than
// dropped. Unlike Register it never claims a wrapper pre-registration: a hook
// carries no pane, and matching one by cwd alone could bind the wrong pane.
func (s *Store) Ensure(sessionID, cwd string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.sessions[sessionID]; ok {
		return false
	}
	s.sessions[sessionID] = &Session{
		ID:        sessionID,
		CWD:       cwd,
		State:     state.StateIdle,
		UpdatedAt: time.Now(),
	}
	return true
}

// SessionForTarget returns a copy of the session bound to a pane target.
func (s *Store) SessionForTarget(target string) (Session, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, sess := range s.sessions {
		if sess.PaneTarget == target {
			return *sess, true
		}
	}
	return Session{}, false
}

// Bind attaches sessionID to a pane: "the claude process in this pane is
// running this session". It is the single place a pane<->session association is
// made, and it keeps the association one-to-one:
//
//   - The session is created if unknown (a session a hook already created keeps
//     the state that hook set).
//   - Any OTHER session bound to the same pane is removed and returned as
//     displaced. This is what un-sticks a pane whose process changed session id
//     after it was first seen -- e.g. `claude --resume` runs under a throwaway
//     id while its picker is open, then switches to the chosen session.
//   - A wrapper pre-registration for exactly this pane supplies the name if the
//     session has none.
//
// It returns a copy of the bound session, the displaced session (nil if none),
// and whether anything visible changed.
func (s *Store) Bind(sessionID, cwd, target string, pid int) (Session, *Session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	changed := false

	var displaced *Session
	for id, other := range s.sessions {
		if id != sessionID && other.PaneTarget == target {
			delete(s.sessions, id)
			displaced = other
			changed = true
		}
	}

	sess, ok := s.sessions[sessionID]
	if !ok {
		sess = &Session{
			ID:        sessionID,
			State:     state.StateIdle,
			UpdatedAt: time.Now(),
		}
		s.sessions[sessionID] = sess
		changed = true
	}
	if sess.PaneTarget != target {
		sess.PaneTarget = target
		changed = true
	}
	if pid != 0 {
		sess.PID = pid
	}
	if cwd != "" && sess.CWD != cwd {
		sess.CWD = cwd
		changed = true
	}
	if sess.Name == "" {
		for i := len(s.pending) - 1; i >= 0; i-- {
			if s.pending[i].PaneTarget == target {
				sess.Name = s.pending[i].Name
				s.pending = append(s.pending[:i], s.pending[i+1:]...)
				changed = true
				break
			}
		}
	}

	return *sess, displaced, changed
}

// PrunePaneless removes sessions that never got bound to a pane and have been
// silent for maxAge -- sessions running outside tmux (or headless), which hooks
// make known but which will never have a pane. Returns how many were removed.
// A pruned session that speaks again is simply re-created by its next hook.
func (s *Store) PrunePaneless(maxAge time.Duration) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	cutoff := time.Now().Add(-maxAge)
	n := 0
	for id, sess := range s.sessions {
		if sess.PaneTarget == "" && sess.UpdatedAt.Before(cutoff) {
			delete(s.sessions, id)
			n++
		}
	}
	return n
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

// matchPending finds a pending registration by CWD (used by Register, which has
// no pane to match on; Bind matches by pane target instead).
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
