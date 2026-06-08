package opencode

import (
	"context"
	"sync"
	"time"

	"github.com/moonstream-labs/tmux-agents/internal/state"
)

type Session struct {
	ID        string
	Name      string
	Dir       string
	State     state.SessionState
	UpdatedAt time.Time
}

type Instance struct {
	Port            int
	PaneTarget      string
	Name            string // initial name from wrapper
	ActiveSessionID string // hint from -s flag on resume
	Sessions        map[string]*Session
	cancel          context.CancelFunc
	UpdatedAt       time.Time
}

type Store struct {
	mu        sync.RWMutex
	instances map[int]*Instance // port -> Instance
}

func NewStore() *Store {
	return &Store{
		instances: make(map[int]*Instance),
	}
}

func (s *Store) Register(port int, paneTarget, name string, cancel context.CancelFunc) *Instance {
	s.mu.Lock()
	defer s.mu.Unlock()

	// If instance already exists on this port, cancel old SSE and replace.
	if old, ok := s.instances[port]; ok {
		if old.cancel != nil {
			old.cancel()
		}
	}

	inst := &Instance{
		Port:       port,
		PaneTarget: paneTarget,
		Name:       name,
		Sessions:   make(map[string]*Session),
		cancel:     cancel,
		UpdatedAt:  time.Now(),
	}
	s.instances[port] = inst
	return inst
}

func (s *Store) Get(port int) (*Instance, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	inst, ok := s.instances[port]
	return inst, ok
}

func (s *Store) Remove(port int) *Instance {
	s.mu.Lock()
	defer s.mu.Unlock()
	inst, ok := s.instances[port]
	if !ok {
		return nil
	}
	if inst.cancel != nil {
		inst.cancel()
	}
	delete(s.instances, port)
	return inst
}

func (s *Store) SetSessionState(port int, sessionID string, st state.SessionState) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.setSessionStateLocked(port, sessionID, st)
}

// SetSessionStateIf sets the state only if the current state is NOT the excluded state.
// Used to prevent message.part.updated from overriding a recent session.idle.
func (s *Store) SetSessionStateIf(port int, sessionID string, st state.SessionState, excludeCurrent state.SessionState) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	inst, ok := s.instances[port]
	if !ok {
		return false
	}
	sess, ok := inst.Sessions[sessionID]
	if !ok {
		return false
	}
	if sess.State == excludeCurrent {
		return false
	}
	return s.setSessionStateLocked(port, sessionID, st)
}

func (s *Store) setSessionStateLocked(port int, sessionID string, st state.SessionState) bool {
	inst, ok := s.instances[port]
	if !ok {
		return false
	}
	sess, ok := inst.Sessions[sessionID]
	if !ok {
		return false
	}
	sess.State = st
	sess.UpdatedAt = time.Now()
	inst.UpdatedAt = time.Now()
	inst.ActiveSessionID = sessionID
	return true
}

func (s *Store) UpsertSession(port int, sessionID, name, dir string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	inst, ok := s.instances[port]
	if !ok {
		return false
	}

	sess, ok := inst.Sessions[sessionID]
	if ok {
		if name != "" {
			sess.Name = name
		}
		if dir != "" {
			sess.Dir = dir
		}
		sess.UpdatedAt = time.Now()
	} else {
		inst.Sessions[sessionID] = &Session{
			ID:        sessionID,
			Name:      name,
			Dir:       dir,
			State:     state.StateIdle,
			UpdatedAt: time.Now(),
		}
	}
	inst.UpdatedAt = time.Now()
	return true
}

func (s *Store) RemoveSession(port int, sessionID string) *Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	inst, ok := s.instances[port]
	if !ok {
		return nil
	}
	sess, ok := inst.Sessions[sessionID]
	if !ok {
		return nil
	}
	delete(inst.Sessions, sessionID)
	return sess
}

func (s *Store) Ports() []int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ports := make([]int, 0, len(s.instances))
	for p := range s.instances {
		ports = append(ports, p)
	}
	return ports
}

func (s *Store) ActivePanes() []state.PaneRow {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var rows []state.PaneRow
	for _, inst := range s.instances {
		// One pane per instance. Pick the most recently active session
		// to represent the instance state (one TUI = one visible session).
		row := state.PaneRow{
			Target:  inst.PaneTarget,
			Tool:    state.ToolOpenCode,
			State:   state.StateUnknown,
			Name:    inst.Name,
			Updated: inst.UpdatedAt.UnixMilli(),
			Host:    "local",
		}

		// Prefer the hinted active session (from -s flag), fall back to most recent.
		var best *Session
		if inst.ActiveSessionID != "" {
			best = inst.Sessions[inst.ActiveSessionID]
		}
		if best == nil {
			for _, sess := range inst.Sessions {
				if best == nil || sess.UpdatedAt.After(best.UpdatedAt) {
					best = sess
				}
			}
		}
		if best != nil {
			row.SessionID = best.ID
			row.State = best.State
			row.Dir = best.Dir
			row.Updated = best.UpdatedAt.UnixMilli()
			if best.Name != "" {
				row.Name = best.Name
			}
		}

		rows = append(rows, row)
	}
	return rows
}

func (s *Store) RecentSessions() []state.RecentRow {
	// Recent sessions populated when instances are removed.
	// For now, return empty — will be filled by removal path.
	return nil
}
