package store_test

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/magna-nz/tallybook/internal/store"
)

// oldSchema is the shape a database had before InputHash and
// CompactionBefore existed: no meta table, no tool_calls.class,
// tool_calls.input_hash or turns.compaction_before. It is written out here,
// rather than reused from package store, so this test really exercises
// "Open must succeed on a database the current code did not create" instead
// of comparing today's schema to itself.
const oldSchema = `
CREATE TABLE schema_version (version INTEGER NOT NULL);
INSERT INTO schema_version(version) VALUES (1);

CREATE TABLE files (
	path      TEXT PRIMARY KEY,
	size      INTEGER NOT NULL,
	mtime     INTEGER NOT NULL,
	session_id TEXT
);

CREATE TABLE sessions (
	id                TEXT PRIMARY KEY,
	source            TEXT NOT NULL,
	path              TEXT UNIQUE NOT NULL,
	project           TEXT,
	git_branch        TEXT,
	started_at        INTEGER,
	ended_at          INTEGER,
	cli_version       TEXT,
	parent_session_id TEXT,
	agent_id          TEXT
);

CREATE TABLE turns (
	session_id      TEXT NOT NULL,
	id              TEXT NOT NULL,
	ts              INTEGER,
	model           TEXT,
	effort          TEXT,
	input           INTEGER,
	cache_read      INTEGER,
	cache_write_5m  INTEGER,
	cache_write_1h  INTEGER,
	output          INTEGER,
	thinking        INTEGER,
	text_chars      INTEGER,
	PRIMARY KEY (session_id, id)
);

CREATE TABLE tool_calls (
	session_id  TEXT NOT NULL,
	turn_id     TEXT NOT NULL,
	id          TEXT NOT NULL,
	name        TEXT,
	input_chars INTEGER,
	PRIMARY KEY (session_id, id)
);

CREATE TABLE agent_launches (
	session_id      TEXT NOT NULL,
	tool_call_id    TEXT NOT NULL,
	subagent_type   TEXT,
	requested_model TEXT,
	resolved_model  TEXT,
	agent_id        TEXT,
	description     TEXT,
	PRIMARY KEY (session_id, tool_call_id)
);

CREATE TABLE tool_results (
	session_id   TEXT NOT NULL,
	tool_call_id TEXT NOT NULL,
	ts           INTEGER,
	chars        INTEGER,
	is_error     INTEGER
);
`

func hasColumnForTest(t *testing.T, db *sql.DB, table, column string) bool {
	t.Helper()
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		t.Fatalf("table_info(%s): %v", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, typ string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			t.Fatalf("scan table_info(%s): %v", table, err)
		}
		if name == column {
			return true
		}
	}
	return false
}

// TestOpenMigratesADatabaseFromBeforeThisFeature builds a database by hand in
// the schema the current code inherited, the way an existing tallybook user's
// database looks today, and checks that Open brings it up to date: the new
// columns appear, the meta table (and its salt) exists, and every file is
// forgotten so the next ingest re-reads every transcript rather than leaving
// old rows with no hash or compaction flag.
func TestOpenMigratesADatabaseFromBeforeThisFeature(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "old.db")

	raw, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	if _, err := raw.Exec(oldSchema); err != nil {
		t.Fatalf("create old schema: %v", err)
	}
	if _, err := raw.Exec(`INSERT INTO sessions(id, source, path) VALUES ('s', 'codex', '/x/s.jsonl')`); err != nil {
		t.Fatalf("seed sessions: %v", err)
	}
	if _, err := raw.Exec(`INSERT INTO files(path, size, mtime, session_id) VALUES ('/x/s.jsonl', 10, 0, 's')`); err != nil {
		t.Fatalf("seed files: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw db: %v", err)
	}

	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("Open on an old-schema database: %v", err)
	}
	defer s.Close()

	stats, err := s.Stats()
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.Files != 0 {
		t.Errorf("stats.Files = %d, want 0: migration must force a full re-read", stats.Files)
	}
	if stats.Sessions != 1 {
		t.Errorf("stats.Sessions = %d, want 1: migration must not touch existing rows", stats.Sessions)
	}

	reopen, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("reopen raw: %v", err)
	}
	defer reopen.Close()

	for _, col := range []struct{ table, name string }{
		{"tool_calls", "class"},
		{"tool_calls", "input_hash"},
		{"turns", "compaction_before"},
	} {
		if !hasColumnForTest(t, reopen, col.table, col.name) {
			t.Errorf("%s.%s missing after Open migrated the database", col.table, col.name)
		}
	}

	var metaCount int
	if err := reopen.QueryRow(`SELECT COUNT(*) FROM meta`).Scan(&metaCount); err != nil {
		t.Fatalf("meta table missing after Open: %v", err)
	}
	if metaCount != 1 {
		t.Errorf("meta table has %d rows, want 1 (the salt)", metaCount)
	}
}
