package store_test

import (
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/magna-nz/tallybook/internal/model"
	"github.com/magna-nz/tallybook/internal/store"
)

func mustOpen(t *testing.T) *store.Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "tallybook.db")
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// buildTranscripts returns the parent ("p1") and child ("p1/agent-ag1")
// transcripts used across the tests below.
func buildTranscripts() (parent, child *model.Transcript) {
	base := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)

	parentTurns := []model.Turn{
		{
			SessionID: "p1",
			ID:        "t1",
			Timestamp: base,
			Model:     "claude-3-opus",
			Usage:     model.Usage{Input: 100, CacheRead: 10, CacheWrite5m: 5, Output: 50, Thinking: 5},
			TextChars: 120,
			ToolCalls: []model.ToolCall{
				{
					ID:         "tc1",
					Name:       "Agent",
					InputChars: 42,
					Agent: &model.AgentLaunch{
						SubagentType:   "researcher",
						RequestedModel: "sonnet",
						ResolvedModel:  "claude-opus-5",
						AgentID:        "ag1",
						Description:    "look into it",
					},
				},
			},
		},
		{
			SessionID: "p1",
			ID:        "t2",
			Timestamp: base.Add(1 * time.Minute),
			Model:     "claude-3-sonnet",
			Usage:     model.Usage{Input: 200, CacheWrite1h: 20, Output: 80},
			TextChars: 40,
		},
		{
			SessionID: "p1",
			ID:        "t3",
			Timestamp: base.Add(2 * time.Minute),
			Model:     "claude-3-opus",
			Usage:     model.Usage{Input: 150, CacheRead: 5, Output: 70, Thinking: 2},
			TextChars: 30,
		},
	}

	parent = &model.Transcript{
		Session: model.Session{
			ID:        "p1",
			Source:    model.SourceClaudeCode,
			Path:      "/fake/p1.jsonl",
			Project:   "/home/user/proj",
			StartedAt: base,
			EndedAt:   base.Add(5 * time.Minute),
		},
		Turns: parentTurns,
		ToolResults: []model.ToolResult{
			{SessionID: "p1", ToolCallID: "tc1", Timestamp: base.Add(30 * time.Second), Chars: 500, IsError: false},
			{SessionID: "p1", ToolCallID: "tc1", Timestamp: base.Add(45 * time.Second), Chars: 20, IsError: true},
		},
	}

	childBase := base.Add(24 * time.Hour)
	child = &model.Transcript{
		Session: model.Session{
			ID:              "p1/agent-ag1",
			Source:          model.SourceClaudeCode,
			Path:            "/fake/p1/agent-ag1.jsonl",
			Project:         "/home/user/proj/pkg",
			StartedAt:       childBase,
			EndedAt:         childBase.Add(2 * time.Minute),
			ParentSessionID: "p1",
			AgentID:         "ag1",
		},
		Turns: []model.Turn{
			{
				SessionID: "p1/agent-ag1",
				ID:        "ct1",
				Timestamp: childBase,
				Model:     "claude-opus-5",
				Usage:     model.Usage{Input: 10, Output: 5},
			},
			{
				SessionID: "p1/agent-ag1",
				ID:        "ct2",
				Timestamp: childBase.Add(1 * time.Minute),
				Model:     "claude-opus-5",
				Usage:     model.Usage{Input: 20, Output: 8},
			},
		},
	}

	return parent, child
}

func sumUsage(turns []model.Turn) model.Usage {
	var u model.Usage
	for _, t := range turns {
		u.Input += t.Usage.Input
		u.CacheRead += t.Usage.CacheRead
		u.CacheWrite5m += t.Usage.CacheWrite5m
		u.CacheWrite1h += t.Usage.CacheWrite1h
		u.Output += t.Usage.Output
		u.Thinking += t.Usage.Thinking
	}
	return u
}

func distinctSortedModels(turns []model.Turn) []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range turns {
		if t.Model == "" || seen[t.Model] {
			continue
		}
		seen[t.Model] = true
		out = append(out, t.Model)
	}
	sort.Strings(out)
	return out
}

func TestStore(t *testing.T) {
	s := mustOpen(t)

	parent, child := buildTranscripts()

	parentMTime := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	if err := s.ReplaceTranscript(parent, 4096, parentMTime); err != nil {
		t.Fatalf("ReplaceTranscript(parent): %v", err)
	}
	childMTime := time.Date(2026, 1, 2, 0, 5, 0, 0, time.UTC)
	if err := s.ReplaceTranscript(child, 1024, childMTime); err != nil {
		t.Fatalf("ReplaceTranscript(child): %v", err)
	}

	t.Run("Sessions returns both rows with rollups", func(t *testing.T) {
		rows, err := s.Sessions(store.Filter{})
		if err != nil {
			t.Fatalf("Sessions: %v", err)
		}
		if len(rows) != 2 {
			t.Fatalf("len(rows) = %d, want 2", len(rows))
		}

		var parentRow, childRow *store.SessionRow
		for i := range rows {
			switch rows[i].ID {
			case "p1":
				parentRow = &rows[i]
			case "p1/agent-ag1":
				childRow = &rows[i]
			}
		}
		if parentRow == nil || childRow == nil {
			t.Fatalf("missing expected session rows: %+v", rows)
		}

		if childRow.AgentType != "researcher" {
			t.Errorf("childRow.AgentType = %q, want researcher", childRow.AgentType)
		}
		if childRow.RequestedModel != "sonnet" {
			t.Errorf("childRow.RequestedModel = %q, want sonnet", childRow.RequestedModel)
		}
		if childRow.ResolvedModel != "claude-opus-5" {
			t.Errorf("childRow.ResolvedModel = %q, want claude-opus-5", childRow.ResolvedModel)
		}

		if parentRow.Turns != 3 {
			t.Errorf("parentRow.Turns = %d, want 3", parentRow.Turns)
		}
		if parentRow.ToolErrors != 1 {
			t.Errorf("parentRow.ToolErrors = %d, want 1", parentRow.ToolErrors)
		}
		if parentRow.ToolCalls != 1 {
			t.Errorf("parentRow.ToolCalls = %d, want 1", parentRow.ToolCalls)
		}

		wantUsage := sumUsage(parent.Turns)
		if parentRow.Usage != wantUsage {
			t.Errorf("parentRow.Usage = %+v, want %+v", parentRow.Usage, wantUsage)
		}

		wantModels := distinctSortedModels(parent.Turns)
		if len(parentRow.Models) != len(wantModels) {
			t.Fatalf("parentRow.Models = %v, want %v", parentRow.Models, wantModels)
		}
		for i := range wantModels {
			if parentRow.Models[i] != wantModels[i] {
				t.Errorf("parentRow.Models = %v, want %v", parentRow.Models, wantModels)
				break
			}
		}
	})

	t.Run("Session fetches a single row", func(t *testing.T) {
		row, err := s.Session("p1")
		if err != nil {
			t.Fatalf("Session: %v", err)
		}
		if row == nil {
			t.Fatal("Session(p1) = nil, want a row")
		}
		if row.Turns != 3 {
			t.Errorf("row.Turns = %d, want 3", row.Turns)
		}

		missing, err := s.Session("does-not-exist")
		if err != nil {
			t.Fatalf("Session(missing): %v", err)
		}
		if missing != nil {
			t.Errorf("Session(missing) = %+v, want nil", missing)
		}
	})

	t.Run("Turns attaches tool calls and agent launch data", func(t *testing.T) {
		turns, err := s.Turns("p1")
		if err != nil {
			t.Fatalf("Turns: %v", err)
		}
		if len(turns) != 3 {
			t.Fatalf("len(turns) = %d, want 3", len(turns))
		}
		if turns[0].ID != "t1" {
			t.Fatalf("turns[0].ID = %q, want t1", turns[0].ID)
		}
		if len(turns[0].ToolCalls) != 1 {
			t.Fatalf("len(turns[0].ToolCalls) = %d, want 1", len(turns[0].ToolCalls))
		}
		tc := turns[0].ToolCalls[0]
		if tc.Name != "Agent" || tc.ID != "tc1" {
			t.Fatalf("unexpected tool call: %+v", tc)
		}
		if tc.Agent == nil {
			t.Fatal("tc.Agent = nil, want AgentLaunch")
		}
		if tc.Agent.SubagentType != "researcher" || tc.Agent.RequestedModel != "sonnet" || tc.Agent.ResolvedModel != "claude-opus-5" {
			t.Errorf("unexpected agent launch: %+v", tc.Agent)
		}
	})

	t.Run("ToolResults returns results in order", func(t *testing.T) {
		results, err := s.ToolResults("p1")
		if err != nil {
			t.Fatalf("ToolResults: %v", err)
		}
		if len(results) != 2 {
			t.Fatalf("len(results) = %d, want 2", len(results))
		}
		if results[0].IsError {
			t.Errorf("results[0].IsError = true, want false")
		}
		if !results[1].IsError {
			t.Errorf("results[1].IsError = false, want true")
		}
	})

	t.Run("ToolCounts", func(t *testing.T) {
		counts, err := s.ToolCounts(store.Filter{})
		if err != nil {
			t.Fatalf("ToolCounts: %v", err)
		}
		if counts["p1"]["Agent"] != 1 {
			t.Errorf(`counts["p1"]["Agent"] = %d, want 1`, counts["p1"]["Agent"])
		}
	})

	t.Run("Launches joins the child session", func(t *testing.T) {
		launches, err := s.Launches(store.Filter{})
		if err != nil {
			t.Fatalf("Launches: %v", err)
		}
		if len(launches) != 1 {
			t.Fatalf("len(launches) = %d, want 1", len(launches))
		}
		l := launches[0]
		if l.ParentSessionID != "p1" {
			t.Errorf("l.ParentSessionID = %q, want p1", l.ParentSessionID)
		}
		if l.ChildSessionID != "p1/agent-ag1" {
			t.Errorf("l.ChildSessionID = %q, want p1/agent-ag1", l.ChildSessionID)
		}
		if l.SubagentType != "researcher" {
			t.Errorf("l.SubagentType = %q, want researcher", l.SubagentType)
		}
	})

	t.Run("FileState round-trips size and mtime", func(t *testing.T) {
		size, mtime, found, err := s.FileState("/fake/p1.jsonl")
		if err != nil {
			t.Fatalf("FileState: %v", err)
		}
		if !found {
			t.Fatal("FileState found = false, want true")
		}
		if size != 4096 {
			t.Errorf("size = %d, want 4096", size)
		}
		if !mtime.Equal(parentMTime) {
			t.Errorf("mtime = %v, want %v", mtime, parentMTime)
		}

		_, _, found, err = s.FileState("/does/not/exist.jsonl")
		if err != nil {
			t.Fatalf("FileState(missing): %v", err)
		}
		if found {
			t.Error("FileState(missing) found = true, want false")
		}
	})

	t.Run("Filter Since/Until narrows by StartedAt", func(t *testing.T) {
		cutoff := parent.Session.StartedAt.Add(12 * time.Hour)
		rows, err := s.Sessions(store.Filter{Since: cutoff})
		if err != nil {
			t.Fatalf("Sessions(Since): %v", err)
		}
		if len(rows) != 1 || rows[0].ID != "p1/agent-ag1" {
			t.Fatalf("Sessions(Since=%v) = %+v, want only child", cutoff, rows)
		}

		rows, err = s.Sessions(store.Filter{Until: cutoff})
		if err != nil {
			t.Fatalf("Sessions(Until): %v", err)
		}
		if len(rows) != 1 || rows[0].ID != "p1" {
			t.Fatalf("Sessions(Until=%v) = %+v, want only parent", cutoff, rows)
		}
	})

	t.Run("Filter Project exact vs prefix", func(t *testing.T) {
		rows, err := s.Sessions(store.Filter{Project: "/home/user/proj"})
		if err != nil {
			t.Fatalf("Sessions(Project exact): %v", err)
		}
		if len(rows) != 1 || rows[0].ID != "p1" {
			t.Fatalf("Sessions(Project=/home/user/proj) = %+v, want only p1", rows)
		}

		rows, err = s.Sessions(store.Filter{Project: "/home/user/"})
		if err != nil {
			t.Fatalf("Sessions(Project prefix): %v", err)
		}
		if len(rows) != 2 {
			t.Fatalf("Sessions(Project=/home/user/) = %+v, want both sessions", rows)
		}
	})

	t.Run("ReplaceTranscript is idempotent and leaves no orphans", func(t *testing.T) {
		replacement := &model.Transcript{
			Session: model.Session{
				ID:        "p1",
				Source:    model.SourceClaudeCode,
				Path:      "/fake/p1.jsonl",
				Project:   "/home/user/proj",
				StartedAt: parent.Session.StartedAt,
				EndedAt:   parent.Session.EndedAt,
			},
			Turns: []model.Turn{
				{
					SessionID: "p1",
					ID:        "solo",
					Timestamp: parent.Session.StartedAt,
					Model:     "claude-3-opus",
					Usage:     model.Usage{Input: 1, Output: 1},
				},
			},
		}
		if err := s.ReplaceTranscript(replacement, 2048, parentMTime); err != nil {
			t.Fatalf("ReplaceTranscript(replacement): %v", err)
		}

		turns, err := s.Turns("p1")
		if err != nil {
			t.Fatalf("Turns: %v", err)
		}
		if len(turns) != 1 {
			t.Fatalf("len(turns) = %d, want 1", len(turns))
		}
		if len(turns[0].ToolCalls) != 0 {
			t.Errorf("turns[0].ToolCalls = %v, want none (orphan)", turns[0].ToolCalls)
		}

		counts, err := s.ToolCounts(store.Filter{})
		if err != nil {
			t.Fatalf("ToolCounts: %v", err)
		}
		if len(counts["p1"]) != 0 {
			t.Errorf("counts[p1] = %v, want empty (orphan tool_calls)", counts["p1"])
		}

		results, err := s.ToolResults("p1")
		if err != nil {
			t.Fatalf("ToolResults: %v", err)
		}
		if len(results) != 0 {
			t.Errorf("results = %v, want none (orphan tool_results)", results)
		}
	})

	t.Run("Stats", func(t *testing.T) {
		stats, err := s.Stats()
		if err != nil {
			t.Fatalf("Stats: %v", err)
		}
		if stats.Files != 2 {
			t.Errorf("stats.Files = %d, want 2", stats.Files)
		}
		if stats.Sessions != 2 {
			t.Errorf("stats.Sessions = %d, want 2", stats.Sessions)
		}
		if stats.Turns != 3 {
			t.Errorf("stats.Turns = %d, want 3 (1 parent + 2 child)", stats.Turns)
		}
		if stats.DBBytes <= 0 {
			t.Errorf("stats.DBBytes = %d, want > 0", stats.DBBytes)
		}
	})
}
