package claude

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/magna-nz/tallybook/internal/model"
)

const testdataRoot = "testdata/projects"

func TestDiscover(t *testing.T) {
	got, err := Discover(testdataRoot)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	want := []string{
		filepath.Join(testdataRoot, "-home-user-project", "sess-0001.jsonl"),
		filepath.Join(testdataRoot, "-home-user-project", "sess-0001", "subagents", "agent-agent01.jsonl"),
	}
	// Discover must be sorted; sort the expectation the same way so the test
	// doesn't hard-code an ordering assumption beyond "sorted, deterministic".
	if len(got) != len(want) {
		t.Fatalf("Discover returned %d paths, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		found := false
		for _, g := range got {
			if g == want[i] {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Discover missing expected path %s; got %v", want[i], got)
		}
	}
	for i := 1; i < len(got); i++ {
		if got[i-1] >= got[i] {
			t.Errorf("Discover result not sorted: %v", got)
		}
	}
}

func findTurn(t *testing.T, turns []model.Turn, id string) model.Turn {
	t.Helper()
	for _, tr := range turns {
		if tr.ID == id {
			return tr
		}
	}
	t.Fatalf("turn %s not found", id)
	return model.Turn{}
}

func TestParseSession(t *testing.T) {
	path := filepath.Join(testdataRoot, "-home-user-project", "sess-0001.jsonl")
	tr, err := Parse(path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if len(tr.Turns) != 4 {
		t.Fatalf("got %d turns, want 4: %+v", len(tr.Turns), tr.Turns)
	}

	msg1 := findTurn(t, tr.Turns, "msg_1")
	if msg1.Usage.Input != 2 {
		t.Errorf("msg_1 Input = %d, want 2", msg1.Usage.Input)
	}
	if msg1.Usage.CacheRead != 34538 {
		t.Errorf("msg_1 CacheRead = %d, want 34538", msg1.Usage.CacheRead)
	}
	if msg1.Usage.CacheWrite1h != 18179 {
		t.Errorf("msg_1 CacheWrite1h = %d, want 18179", msg1.Usage.CacheWrite1h)
	}
	if msg1.Usage.CacheWrite5m != 0 {
		t.Errorf("msg_1 CacheWrite5m = %d, want 0", msg1.Usage.CacheWrite5m)
	}
	if msg1.Usage.Output != 176 {
		t.Errorf("msg_1 Output = %d, want 176", msg1.Usage.Output)
	}
	if len(msg1.ToolCalls) != 1 || msg1.ToolCalls[0].Name != "Read" {
		t.Errorf("msg_1 ToolCalls = %+v, want exactly one Read", msg1.ToolCalls)
	}

	msg2 := findTurn(t, tr.Turns, "msg_2")
	if len(msg2.ToolCalls) != 1 {
		t.Fatalf("msg_2 ToolCalls = %+v, want exactly one", msg2.ToolCalls)
	}
	agentCall := msg2.ToolCalls[0]
	if agentCall.Name != "Agent" {
		t.Errorf("msg_2 tool call name = %q, want Agent", agentCall.Name)
	}
	if agentCall.Agent == nil {
		t.Fatalf("msg_2 tool call has no Agent launch")
	}
	if agentCall.Agent.SubagentType != "researcher" {
		t.Errorf("Agent.SubagentType = %q, want researcher", agentCall.Agent.SubagentType)
	}
	if agentCall.Agent.RequestedModel != "sonnet" {
		t.Errorf("Agent.RequestedModel = %q, want sonnet", agentCall.Agent.RequestedModel)
	}
	if agentCall.Agent.ResolvedModel != "claude-opus-5" {
		t.Errorf("Agent.ResolvedModel = %q, want claude-opus-5", agentCall.Agent.ResolvedModel)
	}
	if agentCall.Agent.AgentID != "agent01" {
		t.Errorf("Agent.AgentID = %q, want agent01", agentCall.Agent.AgentID)
	}
	if msg2.Usage.Thinking != 120 {
		t.Errorf("msg_2 Usage.Thinking = %d, want 120", msg2.Usage.Thinking)
	}

	if len(tr.ToolResults) != 3 {
		t.Fatalf("got %d tool results, want 3: %+v", len(tr.ToolResults), tr.ToolResults)
	}
	errorCount := 0
	for _, res := range tr.ToolResults {
		if res.IsError {
			errorCount++
			if res.Chars <= 30000 {
				t.Errorf("error tool result Chars = %d, want > 30000", res.Chars)
			}
		}
	}
	if errorCount != 1 {
		t.Errorf("got %d error tool results, want exactly 1", errorCount)
	}

	if tr.Session.Project != "/home/user/project" {
		t.Errorf("Session.Project = %q, want /home/user/project", tr.Session.Project)
	}
	if tr.Session.GitBranch != "main" {
		t.Errorf("Session.GitBranch = %q, want main", tr.Session.GitBranch)
	}
	if tr.Session.CLIVersion != "2.1.237" {
		t.Errorf("Session.CLIVersion = %q, want 2.1.237", tr.Session.CLIVersion)
	}
	if tr.Session.ID != "sess-0001" {
		t.Errorf("Session.ID = %q, want sess-0001", tr.Session.ID)
	}
}

func TestParseSubAgentSession(t *testing.T) {
	path := filepath.Join(testdataRoot, "-home-user-project", "sess-0001", "subagents", "agent-agent01.jsonl")
	tr, err := Parse(path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if tr.Session.AgentID != "agent01" {
		t.Errorf("Session.AgentID = %q, want agent01", tr.Session.AgentID)
	}
	if tr.Session.ParentSessionID != "sess-0001" {
		t.Errorf("Session.ParentSessionID = %q, want sess-0001", tr.Session.ParentSessionID)
	}
	if tr.Session.ID != "sess-0001/agent-agent01" {
		t.Errorf("Session.ID = %q, want sess-0001/agent-agent01", tr.Session.ID)
	}
	if len(tr.Turns) != 3 {
		t.Fatalf("got %d turns, want 3: %+v", len(tr.Turns), tr.Turns)
	}

	var names []string
	for _, turn := range tr.Turns {
		for _, tc := range turn.ToolCalls {
			names = append(names, tc.Name)
		}
	}
	if len(names) != 2 || names[0] != "Grep" || names[1] != "Read" {
		t.Errorf("tool names = %v, want [Grep Read]", names)
	}
}

// TestParseCompactionMarkers exercises both shapes Claude Code uses to record
// a context compaction: a system record with subtype "compact_boundary", and
// a user record with a top-level "isCompactSummary". Either must set
// CompactionBefore on the next new message.id seen after it, and only that
// one turn.
func TestParseCompactionMarkers(t *testing.T) {
	path := filepath.Join("testdata", "compaction", "sess-compact.jsonl")
	tr, err := Parse(path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if len(tr.Turns) != 4 {
		t.Fatalf("got %d turns, want 4: %+v", len(tr.Turns), tr.Turns)
	}

	want := map[string]bool{
		"msg_1": false, // before any compaction marker
		"msg_2": true,  // right after the compact_boundary system record
		"msg_3": true,  // right after the isCompactSummary user record
		"msg_4": false, // the flag must not leak past the turn it was set on
	}
	for id, wantCompaction := range want {
		turn := findTurn(t, tr.Turns, id)
		if turn.CompactionBefore != wantCompaction {
			t.Errorf("turn %s CompactionBefore = %v, want %v", id, turn.CompactionBefore, wantCompaction)
		}
	}
}

// An image block in a tool result is sized at a fixed allowance, not at the
// length of its base64, which is what the API charges for and what stops a
// screenshot from being counted as 150,000 tokens of context.
func TestToolResultCharsSizesImagesByAllowance(t *testing.T) {
	base64 := strings.Repeat("A", 600_000)
	raw := []byte(`[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"` + base64 + `"}}]`)
	if got := toolResultChars(raw); got != imageResultChars {
		t.Errorf("image-only result Chars = %d, want %d", got, imageResultChars)
	}
	mixed := []byte(`[{"type":"text","text":"hello"},{"type":"image","source":{"data":"` + base64 + `"}}]`)
	if got := toolResultChars(mixed); got != 5+imageResultChars {
		t.Errorf("text+image result Chars = %d, want %d", got, 5+imageResultChars)
	}
}
