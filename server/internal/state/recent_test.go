package state

import (
	"path/filepath"
	"testing"
)

func tmpDB(t *testing.T) *DB {
	t.Helper()
	db, err := OpenDB(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestRecentsOrderAndDedup(t *testing.T) {
	r := NewRecents(tmpDB(t), 50)
	r.Add(ToolClaude, "a", "Alpha", "/a", "sess")
	r.Add(ToolOpenCode, "b", "Beta", "/b", "sess")
	r.Add(ToolClaude, "a", "Alpha2", "/a2", "sess") // same id -> move to front, refresh

	rows := r.Rows()
	if len(rows) != 2 {
		t.Fatalf("want 2 rows, got %d: %+v", len(rows), rows)
	}
	if rows[0].SessionID != "a" || rows[0].Name != "Alpha2" || rows[0].Dir != "/a2" {
		t.Fatalf("front row should be the refreshed 'a': %+v", rows[0])
	}
	if rows[1].SessionID != "b" {
		t.Fatalf("second row should be 'b': %+v", rows[1])
	}
}

func TestRecentsEmptySessionIDNoOp(t *testing.T) {
	r := NewRecents(tmpDB(t), 50)
	r.Add(ToolCodex, "", "x", "/x", "sess") // placeholder with no id
	if got := len(r.Rows()); got != 0 {
		t.Fatalf("empty session id must be a no-op, got %d rows", got)
	}
}

func TestRecentsCap(t *testing.T) {
	r := NewRecents(tmpDB(t), 3)
	for _, id := range []string{"1", "2", "3", "4", "5"} {
		r.Add(ToolClaude, id, "n"+id, "/d", "sess")
	}
	rows := r.Rows()
	if len(rows) != 3 {
		t.Fatalf("cap not enforced: %d rows", len(rows))
	}
	if rows[0].SessionID != "5" || rows[2].SessionID != "3" {
		t.Fatalf("cap should keep newest 3 (5,4,3): %+v", rows)
	}
}

func TestRecentsNameFallbackFromCache(t *testing.T) {
	db := tmpDB(t)
	db.CacheName(ToolClaude, "sid", "CachedName", "/cached")
	r := NewRecents(db, 50)
	r.Add(ToolClaude, "sid", "", "", "sess") // empty name/dir -> fall back to cache
	rows := r.Rows()
	if len(rows) != 1 || rows[0].Name != "CachedName" || rows[0].Dir != "/cached" {
		t.Fatalf("name/dir should fall back to session_names: %+v", rows)
	}
}

func TestRecentsPersistAndReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")

	db, err := OpenDB(path)
	if err != nil {
		t.Fatal(err)
	}
	r := NewRecents(db, 50)
	r.Add(ToolCodex, "cx1", "CodexOne", "/repo", "work")
	// Persist exactly as Reconcile would (snapshot rewrites the recent table).
	if err := db.WriteSnapshot(nil, r.Rows()); err != nil {
		t.Fatal(err)
	}
	db.Close()

	// Reopen and reseed the ring — simulates a server restart.
	db2, err := OpenDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	rows := NewRecents(db2, 50).Rows()
	if len(rows) != 1 || rows[0].SessionID != "cx1" || rows[0].Name != "CodexOne" || rows[0].Tool != ToolCodex {
		t.Fatalf("recent did not survive restart: %+v", rows)
	}
}
