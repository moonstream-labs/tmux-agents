package codex

import (
	"database/sql"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	_ "modernc.org/sqlite"
)

// Thread is a row from Codex's internal threads table (the session index that
// backs `codex resume`). It is the Codex analog of Claude's session JSONL.
type Thread struct {
	ID               string
	Title            string
	FirstUserMessage string
	CWD              string
	UpdatedAt        int64
}

// IsAutoTitle reports whether Title looks auto-generated from the first user
// message rather than an explicit `/rename` (or a Codex-generated summary).
// Codex resets a session's title to this first-message form on resume, so we
// use this to tell a transient reset apart from a name worth preserving.
func (t Thread) IsAutoTitle() bool {
	if t.Title == "" {
		return true
	}
	if t.FirstUserMessage == "" {
		return false
	}
	trimmed := strings.TrimRight(strings.TrimSpace(t.Title), ".… ")
	if trimmed == "" {
		return true
	}
	return strings.Contains(t.FirstUserMessage, trimmed)
}

// ThreadReader reads session titles from Codex's internal state DB
// (~/.codex/state_<N>.sqlite). The schema is undocumented and version-numbered,
// so every access is best-effort: any failure degrades to "no title" rather
// than erroring. The DB is opened read-only and reopened if Codex rotates to a
// new schema version.
type ThreadReader struct {
	mu   sync.Mutex
	db   *sql.DB
	path string
	home string
}

func NewThreadReader() *ThreadReader {
	home, _ := os.UserHomeDir()
	return &ThreadReader{home: home}
}

var stateDBRe = regexp.MustCompile(`^state_(\d+)\.sqlite$`)

// latestStateDB returns the highest-versioned ~/.codex/state_<N>.sqlite path,
// or "" if none exists.
func (r *ThreadReader) latestStateDB() string {
	if r.home == "" {
		return ""
	}
	dir := filepath.Join(r.home, ".codex")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	best, bestN := "", -1
	for _, e := range entries {
		m := stateDBRe.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		if n, _ := strconv.Atoi(m[1]); n > bestN {
			bestN, best = n, filepath.Join(dir, e.Name())
		}
	}
	return best
}

// ensure returns an open read-only handle to the current latest state DB,
// reopening if the resolved path changed. Caller holds r.mu.
func (r *ThreadReader) ensure() *sql.DB {
	path := r.latestStateDB()
	if path == "" {
		return nil
	}
	if r.db != nil && r.path == path {
		return r.db
	}
	if r.db != nil {
		r.db.Close()
		r.db = nil
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&_busy_timeout=250")
	if err != nil {
		return nil
	}
	db.SetMaxOpenConns(1)
	r.db, r.path = db, path
	return db
}

// reset drops the cached handle so the next call reopens. Caller holds r.mu.
func (r *ThreadReader) reset() {
	if r.db != nil {
		r.db.Close()
		r.db = nil
		r.path = ""
	}
}

func (r *ThreadReader) query(where string, arg string) (Thread, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	db := r.ensure()
	if db == nil {
		return Thread{}, false
	}
	var t Thread
	err := db.QueryRow(
		`SELECT id, title, IFNULL(first_user_message,''), cwd, updated_at FROM threads WHERE `+where+` LIMIT 1`, arg).
		Scan(&t.ID, &t.Title, &t.FirstUserMessage, &t.CWD, &t.UpdatedAt)
	if err == sql.ErrNoRows {
		return Thread{}, false
	}
	if err != nil {
		// Schema change / lock / corruption — drop the handle and degrade.
		r.reset()
		return Thread{}, false
	}
	return t, true
}

// ByID looks up a thread by its id (which equals the hook session_id and the
// uuid embedded in the rollout filename). This is the only lookup we use:
// pane→session is resolved exactly from the codex process's open rollout file
// (see tmux.FindCodexSessionForPaneTree), never guessed by cwd.
func (r *ThreadReader) ByID(id string) (Thread, bool) {
	if id == "" {
		return Thread{}, false
	}
	return r.query("id = ?", id)
}
