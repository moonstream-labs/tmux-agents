package state

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

type DB struct {
	*sql.DB
	path string
}

func OpenDB(path string) (*DB, error) {
	dsn := fmt.Sprintf("file:%s?_journal_mode=WAL&_synchronous=NORMAL&_busy_timeout=1000", path)
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	sqlDB.SetMaxOpenConns(1)

	if err := initSchema(sqlDB); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}

	return &DB{DB: sqlDB, path: path}, nil
}

func initSchema(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS panes (
			target     TEXT PRIMARY KEY,
			tool       TEXT NOT NULL,
			state      TEXT NOT NULL,
			session_id TEXT,
			name       TEXT,
			dir        TEXT,
			updated    INTEGER,
			host       TEXT NOT NULL DEFAULT 'local',
			pane_id    TEXT
		);

		CREATE TABLE IF NOT EXISTS recent (
			tool         TEXT NOT NULL,
			session_id   TEXT NOT NULL,
			name         TEXT,
			dir          TEXT,
			updated      INTEGER,
			host         TEXT NOT NULL DEFAULT 'local',
			tmux_session TEXT,
			PRIMARY KEY(tool, session_id, host)
		);

		CREATE TABLE IF NOT EXISTS session_names (
			tool       TEXT NOT NULL,
			session_id TEXT NOT NULL,
			name       TEXT NOT NULL,
			dir        TEXT,
			updated    INTEGER,
			PRIMARY KEY(tool, session_id)
		);
	`)
	if err != nil {
		return err
	}

	// Migration: add pane_id to a panes table created before this column existed.
	// On a freshly-created table (above) the column is already present, so this
	// errors with "duplicate column name" — benign and intentionally ignored.
	_, _ = db.Exec(`ALTER TABLE panes ADD COLUMN pane_id TEXT`)
	return nil
}

func (db *DB) CacheName(tool Tool, sessionID, name, dir string) error {
	if name == "" {
		return nil
	}
	_, err := db.Exec(
		`INSERT OR REPLACE INTO session_names (tool, session_id, name, dir, updated)
		 VALUES (?, ?, ?, ?, strftime('%s','now') * 1000)`,
		tool, sessionID, name, dir)
	return err
}

func (db *DB) LookupName(tool Tool, sessionID string) (name string, dir string) {
	db.QueryRow(
		`SELECT name, IFNULL(dir,'') FROM session_names WHERE tool = ? AND session_id = ?`,
		tool, sessionID).Scan(&name, &dir)
	return
}

// LoadRecent reads persisted recent sessions, newest first. Used to seed the
// in-memory Recents ring at startup so the list survives a server restart.
func (db *DB) LoadRecent(limit int) []RecentRow {
	rows, err := db.Query(
		`SELECT tool, session_id, IFNULL(name,''), IFNULL(dir,''), IFNULL(updated,0), host, IFNULL(tmux_session,'')
		 FROM recent ORDER BY updated DESC LIMIT ?`, limit)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var out []RecentRow
	for rows.Next() {
		var r RecentRow
		var tool string
		if err := rows.Scan(&tool, &r.SessionID, &r.Name, &r.Dir, &r.Updated, &r.Host, &r.TmuxSession); err != nil {
			continue
		}
		r.Tool = Tool(tool)
		out = append(out, r)
	}
	return out
}

func (db *DB) WriteSnapshot(panes []PaneRow, recent []RecentRow) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec("DELETE FROM panes"); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM recent"); err != nil {
		return err
	}

	pStmt, err := tx.Prepare(`INSERT OR REPLACE INTO panes
		(target, tool, state, session_id, name, dir, updated, host, pane_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer pStmt.Close()

	for _, p := range panes {
		if _, err := pStmt.Exec(p.Target, p.Tool, p.State, p.SessionID, p.Name, p.Dir, p.Updated, p.Host, p.PaneID); err != nil {
			return err
		}
	}

	rStmt, err := tx.Prepare(`INSERT OR REPLACE INTO recent
		(tool, session_id, name, dir, updated, host, tmux_session)
		VALUES (?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer rStmt.Close()

	for _, r := range recent {
		if _, err := rStmt.Exec(r.Tool, r.SessionID, r.Name, r.Dir, r.Updated, r.Host, r.TmuxSession); err != nil {
			return err
		}
	}

	return tx.Commit()
}
