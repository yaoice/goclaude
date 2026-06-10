package sqlite

import (
	"path/filepath"
	"testing"
)

func TestOpenDB_CreatesTables(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	db, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db.Close()

	tables := []string{"sessions", "observations", "observations_fts", "schema_migrations"}
	for _, name := range tables {
		var c int
		err := db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type IN ('table','view') AND name=?", name).Scan(&c)
		if err != nil {
			t.Errorf("query %s: %v", name, err)
		} else if c != 1 {
			t.Errorf("table %s not found", name)
		}
	}

	var version int
	db.QueryRow("SELECT MAX(version) FROM schema_migrations").Scan(&version)
	if version != SchemaVersion {
		t.Errorf("version = %d, want %d", version, SchemaVersion)
	}
}

func TestOpenDB_Idempotent(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	db1, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("first OpenDB: %v", err)
	}
	db1.Exec("INSERT INTO sessions (id) VALUES ('test1')")
	db1.Close()

	db2, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("second OpenDB: %v", err)
	}
	defer db2.Close()

	var id string
	if err := db2.QueryRow("SELECT id FROM sessions WHERE id='test1'").Scan(&id); err != nil {
		t.Fatalf("data lost after reopen: %v", err)
	}
}

func TestOpenDB_FTS5_Sync(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "fts.db")

	db, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db.Close()

	// 插入 → FTS 自动同步
	db.Exec("INSERT INTO observations (title, content) VALUES ('Go language project', 'using Go for development')")

	var count int
	db.QueryRow(`SELECT COUNT(*) FROM observations o
		JOIN observations_fts fts ON o.id = fts.rowid
		WHERE observations_fts MATCH '"Go"*'`).Scan(&count)
	if count != 1 {
		t.Errorf("FTS insert sync: count=%d, want 1 (use prefix match)", count)
	}

	// 更新 → FTS 同步
	result, _ := db.Exec("INSERT INTO observations (title, content) VALUES ('old title', 'old data value')")
	id, _ := result.LastInsertId()
	db.Exec("UPDATE observations SET title='new title', content='new data value' WHERE id=?", id)

	db.QueryRow(`SELECT COUNT(*) FROM observations o
		JOIN observations_fts fts ON o.id = fts.rowid
		WHERE observations_fts MATCH '"new"*'`).Scan(&count)
	if count != 1 {
		t.Errorf("FTS update sync: count=%d, want 1", count)
	}

	db.QueryRow(`SELECT COUNT(*) FROM observations o
		JOIN observations_fts fts ON o.id = fts.rowid
		WHERE observations_fts MATCH '"old"*'`).Scan(&count)
	if count != 0 {
		t.Error("old FTS entry not removed after UPDATE")
	}

	// 删除 → FTS 同步
	db.Exec("DELETE FROM observations WHERE id=?", id)
	db.QueryRow(`SELECT COUNT(*) FROM observations o
		JOIN observations_fts fts ON o.id = fts.rowid
		WHERE observations_fts MATCH '"new"*'`).Scan(&count)
	if count != 0 {
		t.Error("deleted record still in FTS index")
	}
}

func TestOpenDB_Indexes(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "idx.db")

	db, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db.Close()

	expected := []string{
		"idx_sessions_started", "idx_sessions_project",
		"idx_obs_type", "idx_obs_category", "idx_obs_session",
		"idx_obs_priority", "idx_obs_created", "idx_obs_expires", "idx_obs_last_accessed",
	}
	for _, idx := range expected {
		var c int
		db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?", idx).Scan(&c)
		if c != 1 {
			t.Errorf("index %s not found", idx)
		}
	}
}

func TestOpenDB_WALMode(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "wal.db")

	db, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db.Close()

	db.Exec("INSERT INTO observations (title, content) VALUES ('wal', 'test')")

	var mode string
	db.QueryRow("PRAGMA journal_mode").Scan(&mode)
	if mode != "wal" {
		t.Errorf("journal_mode = %s, want wal", mode)
	}
}

func TestOpenDB_BusyTimeout(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "busy.db")

	db, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db.Close()

	var timeout int
	db.QueryRow("PRAGMA busy_timeout").Scan(&timeout)
	if timeout != 5000 {
		t.Errorf("busy_timeout = %d, want 5000", timeout)
	}
}

func TestRebuildFTS(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "rebuild.db")

	db, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db.Close()

	db.Exec("INSERT INTO observations (title, content) VALUES ('a', 'test rebuild keyword')")
	if err := RebuildFTS(db); err != nil {
		t.Fatalf("RebuildFTS: %v", err)
	}

	var count int
	db.QueryRow(`SELECT COUNT(*) FROM observations o
		JOIN observations_fts fts ON o.id = fts.rowid
		WHERE observations_fts MATCH '"rebuild"*'`).Scan(&count)
	if count != 1 {
		t.Error("FTS rebuild did not restore index")
	}
}
