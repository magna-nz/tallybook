package store_test

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/magna-nz/tallybook/internal/model"
	"github.com/magna-nz/tallybook/internal/store"
)

// TestToolCallsTableHasNoInputColumn checks the schema the way a curious
// user poking at the file with sqlite3 would: tool_calls may hold a size
// (input_chars) and a salted hash (input_hash), never anything wide enough
// to be the input itself.
func TestToolCallsTableHasNoInputColumn(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "t.db")
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	raw, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer raw.Close()

	rows, err := raw.Query(`PRAGMA table_info(tool_calls)`)
	if err != nil {
		t.Fatalf("table_info: %v", err)
	}
	defer rows.Close()

	want := map[string]bool{
		"session_id": true, "turn_id": true, "id": true, "name": true,
		"input_chars": true, "class": true, "input_hash": true,
	}
	got := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, typ string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			t.Fatalf("scan table_info: %v", err)
		}
		got[name] = true
	}
	for name := range got {
		if !want[name] {
			t.Errorf("unexpected tool_calls column %q: the store must never gain a column wide enough to hold a tool's raw input", name)
		}
	}
	for name := range want {
		if !got[name] {
			t.Errorf("expected tool_calls column %q missing", name)
		}
	}
}

// TestInputHashIsSaltedPerDatabase checks the privacy contract end to end:
// two stores, each with their own randomly generated salt, must hash the
// same in-memory digest to two different strings. If they ever produced the
// same hash, a rule (or a curious user) could match tool-call inputs across
// two different tallybook databases, which the salt exists to prevent.
func TestInputHashIsSaltedPerDatabase(t *testing.T) {
	ts := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	secretInput := []byte(`{"file_path":"/etc/shadow","content":"a secret nobody should be able to recover"}`)
	digest := model.DigestInput("Write", secretInput)

	build := func(sessionID string) *model.Transcript {
		return &model.Transcript{
			Session: model.Session{ID: sessionID, Source: model.SourceClaudeCode, Path: "/x/" + sessionID, StartedAt: ts, EndedAt: ts},
			Turns: []model.Turn{{
				SessionID: sessionID, ID: "t1", Timestamp: ts, Model: "claude-opus-5",
				ToolCalls: []model.ToolCall{{ID: "c1", Name: "Write", InputChars: len(secretInput), InputDigest: digest}},
			}},
		}
	}

	s1 := mustOpen(t)
	if err := s1.ReplaceTranscript(build("s1"), 1, ts); err != nil {
		t.Fatalf("ReplaceTranscript(s1): %v", err)
	}
	s2 := mustOpen(t)
	if err := s2.ReplaceTranscript(build("s2"), 1, ts); err != nil {
		t.Fatalf("ReplaceTranscript(s2): %v", err)
	}

	turns1, err := s1.Turns("s1")
	if err != nil {
		t.Fatalf("Turns(s1): %v", err)
	}
	turns2, err := s2.Turns("s2")
	if err != nil {
		t.Fatalf("Turns(s2): %v", err)
	}
	if len(turns1) != 1 || len(turns1[0].ToolCalls) != 1 {
		t.Fatalf("unexpected turns1 shape: %+v", turns1)
	}
	if len(turns2) != 1 || len(turns2[0].ToolCalls) != 1 {
		t.Fatalf("unexpected turns2 shape: %+v", turns2)
	}

	hash1 := turns1[0].ToolCalls[0].InputHash
	hash2 := turns2[0].ToolCalls[0].InputHash
	if hash1 == "" || hash2 == "" {
		t.Fatalf("expected non-empty hashes for a non-zero digest, got %q and %q", hash1, hash2)
	}
	if hash1 == hash2 {
		t.Errorf("two stores with different salts hashed the same digest to the same value: %q", hash1)
	}

	// A ToolCall with no digest at all (every ToolCall literal elsewhere in
	// this package's tests, and anything the parsers never touched) reads
	// back an empty hash, never a hash of nothing.
	noDigest := &model.Transcript{
		Session: model.Session{ID: "s3", Source: model.SourceClaudeCode, Path: "/x/s3", StartedAt: ts, EndedAt: ts},
		Turns: []model.Turn{{
			SessionID: "s3", ID: "t1", Timestamp: ts, Model: "claude-opus-5",
			ToolCalls: []model.ToolCall{{ID: "c1", Name: "Read"}},
		}},
	}
	s3 := mustOpen(t)
	if err := s3.ReplaceTranscript(noDigest, 1, ts); err != nil {
		t.Fatalf("ReplaceTranscript(s3): %v", err)
	}
	turns3, err := s3.Turns("s3")
	if err != nil {
		t.Fatalf("Turns(s3): %v", err)
	}
	if got := turns3[0].ToolCalls[0].InputHash; got != "" {
		t.Errorf("ToolCall with no digest got InputHash %q, want empty", got)
	}
}
