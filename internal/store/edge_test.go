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
