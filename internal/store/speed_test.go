package store_test

import (
	"database/sql"
	"testing"
	"time"

	"github.com/magna-nz/tallybook/internal/model"
	"github.com/magna-nz/tallybook/internal/pricing"
	"github.com/magna-nz/tallybook/internal/store"
)

// TestSpeedRoundTrips checks that the speed a parser reads off a turn
// survives a write and a read back. It has to: fast mode bills at double,
// and a turn that loses its speed on the way through the store is priced at
// half what it cost, with nothing to show anything went wrong.
func TestSpeedRoundTrips(t *testing.T) {
	st, err := store.Open(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ts := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	tr := &model.Transcript{
		Session: model.Session{ID: "s", Source: model.SourceClaudeCode, Path: "/x/s", StartedAt: ts, EndedAt: ts},
		Turns: []model.Turn{
			{SessionID: "s", ID: "t1", Timestamp: ts, Model: "claude-opus-5", Speed: "fast",
				Usage: model.Usage{Input: 1_000_000, Output: 1_000_000}},
			{SessionID: "s", ID: "t2", Timestamp: ts.Add(time.Minute), Model: "claude-opus-5", Speed: "standard",
				Usage: model.Usage{Input: 1_000_000, Output: 1_000_000}},
			{SessionID: "s", ID: "t3", Timestamp: ts.Add(2 * time.Minute), Model: "claude-opus-5",
				Usage: model.Usage{Input: 1_000_000, Output: 1_000_000}},
		},
	}
	if err := st.ReplaceTranscript(tr, 1, ts); err != nil {
		t.Fatal(err)
	}
	turns, err := st.Turns("s")
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 3 {
		t.Fatalf("len(turns) = %d, want 3", len(turns))
	}
	for i, want := range []string{"fast", "standard", ""} {
		if got := turns[i].Speed; got != want {
			t.Errorf("turns[%d].Speed = %q, want %q", i, got, want)
		}
	}

	// The point of carrying speed at all: the fast turn must price double.
	tab := pricing.Default()
	fastUSD, ok := tab.CostTurn(turns[0])
	if !ok {
		t.Fatal("fast turn not priced")
	}
	stdUSD, ok := tab.CostTurn(turns[1])
	if !ok {
		t.Fatal("standard turn not priced")
	}
	if fastUSD != 2*stdUSD {
		t.Errorf("after a round trip fast = %v, standard = %v; want fast to be double", fastUSD, stdUSD)
	}
}

// A NULL in the speed column must not take the whole command down with it.
// The CREATE TABLE once declared the column nullable while the migration
// declared it NOT NULL DEFAULT ”, so a database written by that build can
// hold NULLs; scanning one into a string failed every cost path at once.
// The schema no longer produces NULLs, so this reproduces the shape that
// build left behind and checks the read survives it.
func TestNullSpeedDoesNotBreakReads(t *testing.T) {
	dbPath := t.TempDir() + "/t.db"
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	ts := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	tr := &model.Transcript{
		Session: model.Session{ID: "s", Source: model.SourceClaudeCode, Path: "/x/s", StartedAt: ts, EndedAt: ts},
		Turns: []model.Turn{
			{SessionID: "s", ID: "t1", Timestamp: ts, Model: "claude-opus-5", Speed: "fast",
				Usage: model.Usage{Input: 1_000_000, Output: 1_000_000}},
		},
	}
	if err := st.ReplaceTranscript(tr, 1, ts); err != nil {
		t.Fatal(err)
	}
	st.Close()

	// Recreate the nullable column the buggy schema declared, then write the
	// row a binary with no speed column would have written.
	raw, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`ALTER TABLE turns DROP COLUMN speed`,
		`ALTER TABLE turns ADD COLUMN speed TEXT`,
	} {
		if _, err := raw.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	_, err = raw.Exec(`INSERT INTO turns(session_id, id, ts, model, effort, input, cache_read,
		cache_write_5m, cache_write_1h, output, thinking, text_chars, compaction_before)
		VALUES('s','old',?,'claude-opus-5','',1000,0,0,0,10,0,5,0)`, ts.Add(time.Minute).UnixNano())
	if err != nil {
		t.Fatal(err)
	}
	var nulls int
	if err := raw.QueryRow(`SELECT COUNT(*) FROM turns WHERE speed IS NULL`).Scan(&nulls); err != nil {
		t.Fatal(err)
	}
	raw.Close()
	if nulls == 0 {
		t.Fatal("test premise: wanted at least one NULL speed row")
	}

	reopened, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	turns, err := reopened.Turns("s")
	if err != nil {
		t.Fatalf("a NULL speed broke the read: %v", err)
	}
	if len(turns) != 2 {
		t.Fatalf("len(turns) = %d, want 2", len(turns))
	}
	for _, tn := range turns {
		if tn.ID == "old" && tn.Speed != "" {
			t.Errorf("NULL speed read back as %q, want \"\"", tn.Speed)
		}
	}
}

// A freshly created database must declare the column the same way the
// migration does, or new installs and migrated ones diverge.
func TestSpeedColumnIsNotNullable(t *testing.T) {
	dbPath := t.TempDir() + "/t.db"
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	st.Close()
	raw, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	rows, err := raw.Query(`PRAGMA table_info(turns)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var found bool
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			t.Fatal(err)
		}
		if name == "speed" {
			found = true
			if notnull != 1 {
				t.Errorf("speed column is nullable; the migration declares it NOT NULL")
			}
		}
	}
	if !found {
		t.Fatal("fresh schema has no speed column")
	}
}
