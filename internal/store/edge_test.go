package store_test

import (
	"testing"
	"time"

	"github.com/magna-nz/tallybook/internal/model"
	"github.com/magna-nz/tallybook/internal/store"
)

func TestDuplicateRowsWithinOneTranscriptDoNotDropTheSession(t *testing.T) {
	st, err := store.Open(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ts := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	tr := &model.Transcript{
		Session: model.Session{ID: "dup", Source: model.SourceCodex, Path: "/x/dup.jsonl", Project: "/p", StartedAt: ts, EndedAt: ts},
		Turns: []model.Turn{
			{SessionID: "dup", ID: "r1", Timestamp: ts, Model: "gpt-5.5", Usage: model.Usage{Input: 10}, ToolCalls: []model.ToolCall{{ID: "c", Name: "exec_command"}}},
			{SessionID: "dup", ID: "r1", Timestamp: ts.Add(time.Minute), Model: "gpt-5.5", Usage: model.Usage{Input: 20}, ToolCalls: []model.ToolCall{{ID: "c", Name: "exec_command"}}},
		},
	}
	if err := st.ReplaceTranscript(tr, 1, ts); err != nil {
		t.Fatalf("a duplicate id inside one transcript must not fail ingest: %v", err)
	}
	row, err := st.Session("dup")
	if err != nil || row == nil {
		t.Fatalf("session missing after ingest: %v", err)
	}
	if row.Turns != 1 || row.ToolCalls != 1 {
		t.Fatalf("duplicates should collapse to one row each, got turns=%d calls=%d", row.Turns, row.ToolCalls)
	}
}

func TestToolCountsCarryClassSuffix(t *testing.T) {
	st, err := store.Open(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ts := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	tr := &model.Transcript{
		Session: model.Session{ID: "s", Source: model.SourceClaudeCode, Path: "/x/s.jsonl", StartedAt: ts, EndedAt: ts},
		Turns: []model.Turn{{SessionID: "s", ID: "m1", Timestamp: ts, Model: "claude-opus-5", ToolCalls: []model.ToolCall{
			{ID: "a", Name: "Bash", Class: model.ClassRead},
			{ID: "b", Name: "Bash", Class: model.ClassWrite},
			{ID: "c", Name: "Bash"},
			{ID: "d", Name: "Read"},
		}}},
	}
	if err := st.ReplaceTranscript(tr, 1, ts); err != nil {
		t.Fatal(err)
	}
	counts, err := st.ToolCounts(store.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	got := counts["s"]
	if got["Bash(read)"] != 1 || got["Bash(write)"] != 1 || got["Bash"] != 1 || got["Read"] != 1 {
		t.Fatalf("unexpected tool counts: %v", got)
	}
}

// TestCompactionBeforeRoundTrips checks that the flag a parser sets on a
// Turn to say "context was compacted right before this one" survives a
// write and a read back through the store.
func TestCompactionBeforeRoundTrips(t *testing.T) {
	st, err := store.Open(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ts := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	tr := &model.Transcript{
		Session: model.Session{ID: "s", Source: model.SourceClaudeCode, Path: "/x/s", StartedAt: ts, EndedAt: ts},
		Turns: []model.Turn{
			{SessionID: "s", ID: "t1", Timestamp: ts, Model: "claude-opus-5"},
			{SessionID: "s", ID: "t2", Timestamp: ts.Add(time.Minute), Model: "claude-opus-5", CompactionBefore: true},
		},
	}
	if err := st.ReplaceTranscript(tr, 1, ts); err != nil {
		t.Fatal(err)
	}
	turns, err := st.Turns("s")
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 2 {
		t.Fatalf("len(turns) = %d, want 2", len(turns))
	}
	if turns[0].CompactionBefore {
		t.Errorf("t1.CompactionBefore = true, want false")
	}
	if !turns[1].CompactionBefore {
		t.Errorf("t2.CompactionBefore = false, want true")
	}
}

func TestSourceFilter(t *testing.T) {
	st, err := store.Open(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ts := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	for _, s := range []model.Session{
		{ID: "c1", Source: model.SourceClaudeCode, Path: "/c1", StartedAt: ts, EndedAt: ts},
		{ID: "x1", Source: model.SourceCodex, Path: "/x1", StartedAt: ts, EndedAt: ts},
	} {
		if err := st.ReplaceTranscript(&model.Transcript{Session: s}, 1, ts); err != nil {
			t.Fatal(err)
		}
	}
	for src, want := range map[model.Source]int{model.SourceClaudeCode: 1, model.SourceCodex: 1, "": 2} {
		rows, err := st.Sessions(store.Filter{Source: src})
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != want {
			t.Errorf("Source=%q: got %d sessions, want %d", src, len(rows), want)
		}
	}
}

// A trailing separator on --project asks for everything under that directory.
// Windows users type a backslash, and an earlier version silently returned
// nothing for them.
func TestProjectPrefixFilterAcceptsBothSeparators(t *testing.T) {
	st, err := store.Open(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ts := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)

	for i, project := range []string{"/repos/app/one", "/repos/app/two", "/repos/other"} {
		s := model.Session{
			ID: string(rune('a' + i)), Source: model.SourceClaudeCode,
			Path: "/x/" + string(rune('a'+i)), Project: project, StartedAt: ts, EndedAt: ts,
		}
		if err := st.ReplaceTranscript(&model.Transcript{Session: s}, 1, ts); err != nil {
			t.Fatal(err)
		}
	}

	for _, filter := range []string{"/repos/app/", `/repos/app\`} {
		rows, err := st.Sessions(store.Filter{Project: filter})
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 2 {
			t.Errorf("Project %q matched %d sessions, want the 2 under /repos/app", filter, len(rows))
		}
	}

	// Without a trailing separator it is still an exact match, not a prefix.
	rows, err := st.Sessions(store.Filter{Project: "/repos/app"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Errorf("an exact match on a directory with no sessions of its own should find none, got %d", len(rows))
	}
}

// The stored project is a session's working directory, so which separator it
// holds depends on the machine that recorded it. A prefix filter has to match
// either, whichever machine is running the query.
func TestProjectPrefixMatchesEitherStoredSeparator(t *testing.T) {
	st, err := store.Open(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ts := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)

	// Two sessions recorded on different machines, under the same project.
	for i, project := range []string{`C:\repos\myapp\sub`, "/repos/myapp/sub"} {
		s := model.Session{
			ID: string(rune('a' + i)), Source: model.SourceClaudeCode,
			Path: "/x/" + string(rune('a'+i)), Project: project, StartedAt: ts, EndedAt: ts,
		}
		if err := st.ReplaceTranscript(&model.Transcript{Session: s}, 1, ts); err != nil {
			t.Fatal(err)
		}
	}

	for _, filter := range []string{`C:\repos\myapp\`, `C:\repos\myapp/`} {
		rows, err := st.Sessions(store.Filter{Project: filter})
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 1 {
			t.Errorf("Project %q matched %d sessions, want the one stored with backslashes", filter, len(rows))
		}
	}
	rows, err := st.Sessions(store.Filter{Project: "/repos/myapp/"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Errorf("a forward-slash filter matched %d sessions, want 1", len(rows))
	}
}

// An underscore is a wildcard in SQL LIKE and an ordinary character in a
// directory name. Without escaping, one project's filter silently swallows
// another project's sessions and every total for the window is wrong.
func TestProjectPrefixDoesNotTreatUnderscoreAsAWildcard(t *testing.T) {
	st, err := store.Open(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ts := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)

	for i, project := range []string{"/repos/my_app/sub", "/repos/myXapp/sub", "/repos/my%app/sub"} {
		s := model.Session{
			ID: string(rune('a' + i)), Source: model.SourceClaudeCode,
			Path: "/x/" + string(rune('a'+i)), Project: project, StartedAt: ts, EndedAt: ts,
		}
		if err := st.ReplaceTranscript(&model.Transcript{Session: s}, 1, ts); err != nil {
			t.Fatal(err)
		}
	}

	rows, err := st.Sessions(store.Filter{Project: "/repos/my_app/"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Errorf("my_app matched %d sessions; the underscore was treated as a wildcard", len(rows))
	}
	if len(rows) == 1 && rows[0].Project != "/repos/my_app/sub" {
		t.Errorf("matched the wrong project: %q", rows[0].Project)
	}

	pct, err := st.Sessions(store.Filter{Project: "/repos/my%app/"})
	if err != nil {
		t.Fatal(err)
	}
	if len(pct) != 1 {
		t.Errorf("a percent sign in a directory name matched %d sessions, want 1", len(pct))
	}
}
