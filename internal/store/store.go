// Package store persists parsed transcripts in SQLite and answers the
// queries the report and findings layers need. It never computes or stores
// cost; callers price turns themselves from Turn.Model and Turn.Usage.
package store

import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"

	"github.com/magna-nz/tallybook/internal/model"
)

// Store wraps a SQLite database holding ingested transcripts.
type Store struct {
	db *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS schema_version (
	version INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS files (
	path      TEXT PRIMARY KEY,
	size      INTEGER NOT NULL,
	mtime     INTEGER NOT NULL,
	session_id TEXT
);

CREATE TABLE IF NOT EXISTS sessions (
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

CREATE TABLE IF NOT EXISTS turns (
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

CREATE TABLE IF NOT EXISTS tool_calls (
	session_id  TEXT NOT NULL,
	turn_id     TEXT NOT NULL,
	id          TEXT NOT NULL,
	name        TEXT,
	input_chars INTEGER,
	PRIMARY KEY (session_id, id)
);

CREATE TABLE IF NOT EXISTS agent_launches (
	session_id      TEXT NOT NULL,
	tool_call_id    TEXT NOT NULL,
	subagent_type   TEXT,
	requested_model TEXT,
	resolved_model  TEXT,
	agent_id        TEXT,
	description     TEXT,
	PRIMARY KEY (session_id, tool_call_id)
);

CREATE TABLE IF NOT EXISTS tool_results (
	session_id   TEXT NOT NULL,
	tool_call_id TEXT NOT NULL,
	ts           INTEGER,
	chars        INTEGER,
	is_error     INTEGER
);

CREATE INDEX IF NOT EXISTS idx_sessions_started_at ON sessions(started_at);
CREATE INDEX IF NOT EXISTS idx_sessions_project ON sessions(project);
CREATE INDEX IF NOT EXISTS idx_sessions_parent_agent ON sessions(parent_session_id, agent_id);
CREATE INDEX IF NOT EXISTS idx_turns_session_ts ON turns(session_id, ts);
CREATE INDEX IF NOT EXISTS idx_tool_calls_session_name ON tool_calls(session_id, name);
CREATE INDEX IF NOT EXISTS idx_tool_results_session ON tool_results(session_id);
`

// Open creates the database file and schema if needed, and enables WAL mode
// and foreign keys.
func Open(path string) (*Store, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	// SQLite via modernc does not support concurrent writers; keep a single
	// connection so PRAGMAs and transactions behave predictably.
	db.SetMaxOpenConns(1)

	pragmas := []string{
		"PRAGMA journal_mode=WAL;",
		"PRAGMA synchronous=NORMAL;",
		"PRAGMA foreign_keys=ON;",
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			db.Close()
			return nil, fmt.Errorf("store: pragma %q: %w", p, err)
		}
	}

	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: create schema: %w", err)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_version`).Scan(&count); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: read schema_version: %w", err)
	}
	if count == 0 {
		if _, err := db.Exec(`INSERT INTO schema_version(version) VALUES (1)`); err != nil {
			db.Close()
			return nil, fmt.Errorf("store: seed schema_version: %w", err)
		}
	}

	return &Store{db: db}, nil
}

// Close closes the underlying database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// FileState reports what was recorded for a transcript path at last ingest.
func (s *Store) FileState(path string) (size int64, mtime time.Time, found bool, err error) {
	var nanos int64
	row := s.db.QueryRow(`SELECT size, mtime FROM files WHERE path = ?`, path)
	err = row.Scan(&size, &nanos)
	if err == sql.ErrNoRows {
		return 0, time.Time{}, false, nil
	}
	if err != nil {
		return 0, time.Time{}, false, fmt.Errorf("store: file state %s: %w", path, err)
	}
	return size, time.Unix(0, nanos).UTC(), true, nil
}

// ReplaceTranscript deletes every row previously stored for t.Session.Path
// (session, turns, tool calls, launches, tool results) and inserts t, in one
// transaction, and records size/mtime in the files table.
func (s *Store) ReplaceTranscript(t *model.Transcript, size int64, mtime time.Time) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("store: begin tx: %w", err)
	}
	defer tx.Rollback()

	// Find the existing session id for this path, if any, so we can purge
	// its dependent rows even if the session id changed between ingests.
	var oldSessionID string
	err = tx.QueryRow(`SELECT id FROM sessions WHERE path = ?`, t.Session.Path).Scan(&oldSessionID)
	if err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("store: lookup existing session: %w", err)
	}

	newSessionID := t.Session.ID

	for _, id := range uniqueNonEmpty(oldSessionID, newSessionID) {
		if _, err := tx.Exec(`DELETE FROM tool_results WHERE session_id = ?`, id); err != nil {
			return fmt.Errorf("store: delete tool_results: %w", err)
		}
		if _, err := tx.Exec(`DELETE FROM agent_launches WHERE session_id = ?`, id); err != nil {
			return fmt.Errorf("store: delete agent_launches: %w", err)
		}
		if _, err := tx.Exec(`DELETE FROM tool_calls WHERE session_id = ?`, id); err != nil {
			return fmt.Errorf("store: delete tool_calls: %w", err)
		}
		if _, err := tx.Exec(`DELETE FROM turns WHERE session_id = ?`, id); err != nil {
			return fmt.Errorf("store: delete turns: %w", err)
		}
		if _, err := tx.Exec(`DELETE FROM sessions WHERE id = ?`, id); err != nil {
			return fmt.Errorf("store: delete sessions: %w", err)
		}
	}

	sess := t.Session
	_, err = tx.Exec(`
		INSERT INTO sessions(id, source, path, project, git_branch, started_at, ended_at, cli_version, parent_session_id, agent_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sess.ID, string(sess.Source), sess.Path, sess.Project, sess.GitBranch,
		toUnixNanos(sess.StartedAt), toUnixNanos(sess.EndedAt), sess.CLIVersion,
		sess.ParentSessionID, sess.AgentID,
	)
	if err != nil {
		return fmt.Errorf("store: insert session: %w", err)
	}

	turnStmt, err := tx.Prepare(`
		INSERT INTO turns(session_id, id, ts, model, effort, input, cache_read, cache_write_5m, cache_write_1h, output, thinking, text_chars)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("store: prepare turn insert: %w", err)
	}
	defer turnStmt.Close()

	toolCallStmt, err := tx.Prepare(`
		INSERT INTO tool_calls(session_id, turn_id, id, name, input_chars)
		VALUES (?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("store: prepare tool_call insert: %w", err)
	}
	defer toolCallStmt.Close()

	launchStmt, err := tx.Prepare(`
		INSERT INTO agent_launches(session_id, tool_call_id, subagent_type, requested_model, resolved_model, agent_id, description)
		VALUES (?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("store: prepare agent_launch insert: %w", err)
	}
	defer launchStmt.Close()

	for _, turn := range t.Turns {
		_, err = turnStmt.Exec(
			newSessionID, turn.ID, toUnixNanos(turn.Timestamp), turn.Model, turn.Effort,
			turn.Usage.Input, turn.Usage.CacheRead, turn.Usage.CacheWrite5m, turn.Usage.CacheWrite1h,
			turn.Usage.Output, turn.Usage.Thinking, turn.TextChars,
		)
		if err != nil {
			return fmt.Errorf("store: insert turn %s: %w", turn.ID, err)
		}

		for _, tc := range turn.ToolCalls {
			_, err = toolCallStmt.Exec(newSessionID, turn.ID, tc.ID, tc.Name, tc.InputChars)
			if err != nil {
				return fmt.Errorf("store: insert tool_call %s: %w", tc.ID, err)
			}
			if tc.Agent != nil {
				_, err = launchStmt.Exec(
					newSessionID, tc.ID, tc.Agent.SubagentType, tc.Agent.RequestedModel,
					tc.Agent.ResolvedModel, tc.Agent.AgentID, tc.Agent.Description,
				)
				if err != nil {
					return fmt.Errorf("store: insert agent_launch %s: %w", tc.ID, err)
				}
			}
		}
	}

	resultStmt, err := tx.Prepare(`
		INSERT INTO tool_results(session_id, tool_call_id, ts, chars, is_error)
		VALUES (?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("store: prepare tool_result insert: %w", err)
	}
	defer resultStmt.Close()

	for _, tr := range t.ToolResults {
		isErr := 0
		if tr.IsError {
			isErr = 1
		}
		_, err = resultStmt.Exec(newSessionID, tr.ToolCallID, toUnixNanos(tr.Timestamp), tr.Chars, isErr)
		if err != nil {
			return fmt.Errorf("store: insert tool_result: %w", err)
		}
	}

	_, err = tx.Exec(`
		INSERT INTO files(path, size, mtime, session_id) VALUES (?, ?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET size = excluded.size, mtime = excluded.mtime, session_id = excluded.session_id`,
		t.Session.Path, size, mtime.UnixNano(), newSessionID,
	)
	if err != nil {
		return fmt.Errorf("store: upsert file: %w", err)
	}

	return tx.Commit()
}

// Stats for the status command.
type Stats struct {
	Files    int
	Sessions int
	Turns    int
	DBBytes  int64
}

// Stats returns row counts and the on-disk size of the database.
func (s *Store) Stats() (Stats, error) {
	var st Stats
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM files`).Scan(&st.Files); err != nil {
		return Stats{}, fmt.Errorf("store: count files: %w", err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&st.Sessions); err != nil {
		return Stats{}, fmt.Errorf("store: count sessions: %w", err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM turns`).Scan(&st.Turns); err != nil {
		return Stats{}, fmt.Errorf("store: count turns: %w", err)
	}

	var pageCount, pageSize int64
	if err := s.db.QueryRow(`PRAGMA page_count`).Scan(&pageCount); err != nil {
		return Stats{}, fmt.Errorf("store: page_count: %w", err)
	}
	if err := s.db.QueryRow(`PRAGMA page_size`).Scan(&pageSize); err != nil {
		return Stats{}, fmt.Errorf("store: page_size: %w", err)
	}
	st.DBBytes = pageCount * pageSize

	return st, nil
}

// toUnixNanos converts t to unix nanoseconds, or 0 for the zero time.
func toUnixNanos(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixNano()
}

// fromUnixNanos converts unix nanoseconds back to a time.Time, returning the
// zero time for 0.
func fromUnixNanos(n int64) time.Time {
	if n == 0 {
		return time.Time{}
	}
	return time.Unix(0, n).UTC()
}

// uniqueNonEmpty returns the distinct, non-empty strings among ids.
func uniqueNonEmpty(ids ...string) []string {
	seen := make(map[string]bool, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}
