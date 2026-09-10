package codex

import (
	"path/filepath"
	"testing"

	"github.com/magna-nz/tallybook/internal/model"
)

const (
	thr0001 = "testdata/sessions/2026/09/01/rollout-2026-09-01T11-00-00-thr_0001.jsonl"
	thr0002 = "testdata/sessions/2026/09/02/rollout-2026-09-02T09-00-00-thr_0002.jsonl"
)

func TestParseThr0001TokenCountDeltas(t *testing.T) {
	tr, err := Parse(thr0001)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if got, want := tr.Session.ID, "thr_0001"; got != want {
		t.Errorf("Session.ID = %q, want %q", got, want)
	}
	if got, want := tr.Session.Project, "/home/user/project"; got != want {
		t.Errorf("Session.Project = %q, want %q", got, want)
	}
	if got, want := tr.Session.GitBranch, "main"; got != want {
		t.Errorf("Session.GitBranch = %q, want %q", got, want)
	}
	if got, want := tr.Session.CLIVersion, "0.130.0"; got != want {
		t.Errorf("Session.CLIVersion = %q, want %q", got, want)
	}
	if tr.Session.Source != model.SourceCodex {
		t.Errorf("Session.Source = %q, want %q", tr.Session.Source, model.SourceCodex)
	}

	if got, want := len(tr.Turns), 3; got != want {
		t.Fatalf("len(Turns) = %d, want %d", got, want)
	}

	turn1, turn2, turn3 := tr.Turns[0], tr.Turns[1], tr.Turns[2]

	// Turn 1: 12000 input, 9000 cached, 300 output, 120 reasoning (baseline 0).
	if got, want := turn1.Usage.Input, int64(3000); got != want {
		t.Errorf("turn1 Usage.Input = %d, want %d", got, want)
	}
	if got, want := turn1.Usage.CacheRead, int64(9000); got != want {
		t.Errorf("turn1 Usage.CacheRead = %d, want %d", got, want)
	}
	if got, want := turn1.Usage.Output, int64(300); got != want {
		t.Errorf("turn1 Usage.Output = %d, want %d", got, want)
	}
	if got, want := turn1.Usage.Thinking, int64(120); got != want {
		t.Errorf("turn1 Usage.Thinking = %d, want %d", got, want)
	}
	if got, want := turn1.Model, "gpt-5.5"; got != want {
		t.Errorf("turn1 Model = %q, want %q", got, want)
	}
	if got, want := turn1.Effort, "medium"; got != want {
		t.Errorf("turn1 Effort = %q, want %q", got, want)
	}
	if got, want := len(turn1.ToolCalls), 1; got != want {
		t.Fatalf("len(turn1.ToolCalls) = %d, want %d", got, want)
	}
	if got, want := turn1.ToolCalls[0].Name, "exec_command"; got != want {
		t.Errorf("turn1.ToolCalls[0].Name = %q, want %q", got, want)
	}

	// Turn 2: delta 500 input / 11800 cached / 220 output; the duplicate
	// token_count event that immediately follows must not double count.
	if got, want := turn2.Usage.Input, int64(500); got != want {
		t.Errorf("turn2 Usage.Input = %d, want %d", got, want)
	}
	if got, want := turn2.Usage.CacheRead, int64(11800); got != want {
		t.Errorf("turn2 Usage.CacheRead = %d, want %d", got, want)
	}
	if got, want := turn2.Usage.Output, int64(220); got != want {
		t.Errorf("turn2 Usage.Output = %d, want %d", got, want)
	}
	if got, want := len(turn2.ToolCalls), 1; got != want {
		t.Fatalf("len(turn2.ToolCalls) = %d, want %d", got, want)
	}
	if got, want := turn2.ToolCalls[0].Name, "apply_patch"; got != want {
		t.Errorf("turn2.ToolCalls[0].Name = %q, want %q", got, want)
	}

	// Turn 3: delta 100 input / 12200 cached / 80 output, plus the visible
	// assistant reply's character count.
	if got, want := turn3.Usage.Input, int64(100); got != want {
		t.Errorf("turn3 Usage.Input = %d, want %d", got, want)
	}
	if got, want := turn3.Usage.CacheRead, int64(12200); got != want {
		t.Errorf("turn3 Usage.CacheRead = %d, want %d", got, want)
	}
	if got, want := turn3.Usage.Output, int64(80); got != want {
		t.Errorf("turn3 Usage.Output = %d, want %d", got, want)
	}
	if got, want := turn3.TextChars, 24; got != want {
		t.Errorf("turn3.TextChars = %d, want %d", got, want)
	}

	if got, want := len(tr.ToolResults), 2; got != want {
		t.Fatalf("len(ToolResults) = %d, want %d", got, want)
	}
	byCallID := map[string]model.ToolResult{}
	for _, res := range tr.ToolResults {
		byCallID[res.ToolCallID] = res
	}
	call2, ok := byCallID["call_2"]
	if !ok {
		t.Fatalf("no ToolResult for call_2")
	}
	if !call2.IsError {
		t.Errorf("call_2.IsError = false, want true")
	}
	call1, ok := byCallID["call_1"]
	if !ok {
		t.Fatalf("no ToolResult for call_1")
	}
	if call1.Chars <= 4000 {
		t.Errorf("call_1.Chars = %d, want > 4000", call1.Chars)
	}
}

func TestParseThr0002TokenUsageRecord(t *testing.T) {
	tr, err := Parse(thr0002)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if got, want := len(tr.Turns), 2; got != want {
		t.Fatalf("len(Turns) = %d, want %d", got, want)
	}

	respA, respB := tr.Turns[0], tr.Turns[1]
	if got, want := respA.ID, "resp_a"; got != want {
		t.Errorf("Turns[0].ID = %q, want %q", got, want)
	}
	if got, want := respB.ID, "resp_b"; got != want {
		t.Errorf("Turns[1].ID = %q, want %q", got, want)
	}

	for _, turn := range tr.Turns {
		if got, want := turn.Model, "gpt-5.4-mini"; got != want {
			t.Errorf("Turn %q Model = %q, want %q", turn.ID, got, want)
		}
		if got, want := turn.Effort, "low"; got != want {
			t.Errorf("Turn %q Effort = %q, want %q", turn.ID, got, want)
		}
	}

	if got, want := respA.Usage.CacheWrite5m, int64(2500); got != want {
		t.Errorf("resp_a Usage.CacheWrite5m = %d, want %d", got, want)
	}
	if got, want := len(respA.ToolCalls), 1; got != want {
		t.Fatalf("len(resp_a.ToolCalls) = %d, want %d", got, want)
	}
	if got, want := respA.ToolCalls[0].Name, "local_shell"; got != want {
		t.Errorf("resp_a.ToolCalls[0].Name = %q, want %q", got, want)
	}
}

func TestDiscoverSortedAndDeduplicated(t *testing.T) {
	paths, err := Discover("testdata/sessions")
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	want := []string{
		filepath.FromSlash(thr0001),
		filepath.FromSlash(thr0002),
	}
	if len(paths) != len(want) {
		t.Fatalf("Discover returned %d paths, want %d: %v", len(paths), len(want), paths)
	}
	for i, p := range paths {
		if p != want[i] {
			t.Errorf("paths[%d] = %q, want %q", i, p, want[i])
		}
	}
}
