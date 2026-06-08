package state

import (
	"sync"
	"time"
)

const defaultRecentCap = 50

// Recents is the shared, in-memory list of ended sessions shown in the
// navigator's Recent view. It is seeded from the DB at startup (so it survives
// a restart even though WriteSnapshot rewrites the recent table on every
// reconcile) and appended to as sessions end. Entries are most-recent first,
// deduped by tool+session_id+host, and capped.
type Recents struct {
	mu    sync.Mutex
	db    *DB
	cap   int
	items []RecentRow
}

func NewRecents(db *DB, capn int) *Recents {
	if capn <= 0 {
		capn = defaultRecentCap
	}
	r := &Recents{db: db, cap: capn}
	r.items = db.LoadRecent(capn)
	return r
}

// Add records an ended session at the front of the list. An existing entry for
// the same tool+session_id+host is replaced (moved to front). Name/dir fall
// back to the session_names cache when empty. No-op for an empty session id.
func (r *Recents) Add(tool Tool, sessionID, name, dir, tmuxSession string) {
	if sessionID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	if name == "" || dir == "" {
		n, d := r.db.LookupName(tool, sessionID)
		if name == "" {
			name = n
		}
		if dir == "" {
			dir = d
		}
	}

	row := RecentRow{
		Tool:        tool,
		SessionID:   sessionID,
		Name:        name,
		Dir:         dir,
		Updated:     time.Now().UnixMilli(),
		Host:        "local",
		TmuxSession: tmuxSession,
	}

	items := make([]RecentRow, 0, len(r.items)+1)
	items = append(items, row)
	for _, it := range r.items {
		if it.Tool == tool && it.SessionID == sessionID && it.Host == row.Host {
			continue
		}
		items = append(items, it)
	}
	if len(items) > r.cap {
		items = items[:r.cap]
	}
	r.items = items
}

// Rows returns a copy of the recent list, most-recent first.
func (r *Recents) Rows() []RecentRow {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]RecentRow, len(r.items))
	copy(out, r.items)
	return out
}
