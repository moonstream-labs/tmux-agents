package state

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// WriteSnapshot must persist PaneRow.PaneID into the panes.pane_id column so the
// navigator can order the active ring by %N.
func TestWriteSnapshotPersistsPaneID(t *testing.T) {
	db, err := OpenDB(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db.Close()

	rows := []PaneRow{{
		Target:    "main:1.0",
		PaneID:    "%81",
		Tool:      ToolClaude,
		State:     StateIdle,
		SessionID: "sess-1",
		Name:      "feature-x",
		Host:      "local",
	}}
	if err := db.WriteSnapshot(rows, nil); err != nil {
		t.Fatalf("WriteSnapshot: %v", err)
	}

	var target, paneID string
	if err := db.QueryRow(
		`SELECT target, IFNULL(pane_id, '') FROM panes WHERE session_id = ?`, "sess-1",
	).Scan(&target, &paneID); err != nil {
		t.Fatalf("query: %v", err)
	}
	if target != "main:1.0" || paneID != "%81" {
		t.Errorf("got target=%q pane_id=%q, want main:1.0 / %%81", target, paneID)
	}
}

// A database created before the pane_id column existed must be migrated in place
// by OpenDB (guarded ALTER), and the new column must then be writable/readable.
func TestMigrationAddsPaneIDColumn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")

	// Simulate a pre-pane_id panes table.
	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	if _, err := raw.Exec(`CREATE TABLE panes (
		target     TEXT PRIMARY KEY,
		tool       TEXT NOT NULL,
		state      TEXT NOT NULL,
		session_id TEXT,
		name       TEXT,
		dir        TEXT,
		updated    INTEGER,
		host       TEXT NOT NULL DEFAULT 'local'
	);`); err != nil {
		t.Fatalf("create legacy schema: %v", err)
	}
	raw.Close()

	// OpenDB must migrate without error.
	db, err := OpenDB(path)
	if err != nil {
		t.Fatalf("OpenDB on legacy db: %v", err)
	}
	defer db.Close()

	// The migrated column must round-trip.
	if err := db.WriteSnapshot([]PaneRow{{
		Target: "s:1.0", PaneID: "%5", Tool: ToolCodex, State: StateIdle, SessionID: "x", Host: "local",
	}}, nil); err != nil {
		t.Fatalf("WriteSnapshot after migration: %v", err)
	}
	var pid string
	if err := db.QueryRow(`SELECT IFNULL(pane_id,'') FROM panes WHERE session_id = ?`, "x").Scan(&pid); err != nil {
		t.Fatalf("query: %v", err)
	}
	if pid != "%5" {
		t.Errorf("pane_id = %q, want %%5", pid)
	}
}
