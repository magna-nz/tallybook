package claude

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/magna-nz/tallybook/internal/model"
)

// writeTranscript writes lines to a file laid out like a real transcript
// directory and returns its path.
func writeTranscript(t *testing.T, rel string, lines ...string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

const base = `"cwd":"/home/user/p","sessionId":"s1","userType":"external","version":"2.1.237","isSidechain":false`

func asst(uuid, msgID, model, block, usage string) string {
	return `{"type":"assistant","uuid":"` + uuid + `","parentUuid":null,"timestamp":"2026-09-01T10:00:00.000Z","requestId":"req_` + uuid + `",` + base +
		`,"message":{"id":"` + msgID + `","model":"` + model + `","role":"assistant","type":"message","content":[` + block + `],"usage":` + usage + `}}`
}

const usage1 = `{"input_tokens":10,"cache_creation_input_tokens":0,"cache_read_input_tokens":100,"output_tokens":5}`
const zeroUsage = `{"input_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":0}`

func TestSyntheticRecordsAreSkipped(t *testing.T) {
	p := writeTranscript(t, "proj/s1.jsonl",
		`{"type":"user","uuid":"u1","parentUuid":null,"timestamp":"2026-09-01T09:59:00.000Z",`+base+`,"message":{"role":"user","content":"hi"}}`,
		asst("a1", "msg_1", "claude-opus-5", `{"type":"text","text":"ok"}`, usage1),
		asst("a2", "msg_2", "<synthetic>", `{"type":"text","text":"API Error: your computer went to sleep"}`, zeroUsage),
	)
	tr, err := Parse(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(tr.Turns) != 1 || tr.Turns[0].Model != "claude-opus-5" {
		t.Fatalf("want one real turn, got %+v", tr.Turns)
	}
}

func TestDuplicateToolUseIDsStayDistinct(t *testing.T) {
	block := `{"type":"tool_use","id":"toolu_same","name":"Read","input":{"file_path":"/x"}}`
	p := writeTranscript(t, "proj/s1.jsonl",
		asst("a1", "msg_1", "claude-opus-5", block, usage1),
		asst("a2", "msg_2", "claude-opus-5", block, usage1),
	)
	tr, err := Parse(p)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, tn := range tr.Turns {
		for _, tc := range tn.ToolCalls {
			ids = append(ids, tc.ID)
		}
	}
	if len(ids) != 2 || ids[0] == ids[1] {
		t.Fatalf("duplicate tool ids were not made distinct: %v", ids)
	}
}

func TestTurnIDIsMessageIDNotRequestID(t *testing.T) {
	// Two responses can share a requestId after a retry; message ids never collide.
	a := asst("a1", "msg_1", "claude-opus-5", `{"type":"text","text":"one"}`, usage1)
	b := strings.Replace(asst("a2", "msg_2", "claude-opus-5", `{"type":"text","text":"two"}`, usage1), `"requestId":"req_a2"`, `"requestId":"req_a1"`, 1)
	p := writeTranscript(t, "proj/s1.jsonl", a, b)
	tr, err := Parse(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(tr.Turns) != 2 || tr.Turns[0].ID == tr.Turns[1].ID {
		t.Fatalf("turns sharing a requestId must keep distinct ids: %+v", tr.Turns)
	}
	if tr.Turns[0].Usage.Input+tr.Turns[1].Usage.Input != 20 {
		t.Fatalf("usage should be counted once per message: %+v", tr.Turns)
	}
}

func TestAgentIDFallsBackToFileName(t *testing.T) {
	line := strings.Replace(asst("a1", "msg_1", "claude-opus-5", `{"type":"text","text":"x"}`, usage1), `"isSidechain":false`, `"isSidechain":true`, 1)
	p := writeTranscript(t, "proj/s1/subagents/agent-abc123.jsonl", line)
	tr, err := Parse(p)
	if err != nil {
		t.Fatal(err)
	}
	if tr.Session.AgentID != "abc123" || tr.Session.ParentSessionID != "s1" || tr.Session.ID != "s1/agent-abc123" {
		t.Fatalf("agent id not taken from file name: %+v", tr.Session)
	}
}

func TestBashCommandsAreClassifiedWithoutStoringText(t *testing.T) {
	read := `{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"git status && ls"}}`
	write := `{"type":"tool_use","id":"t2","name":"Bash","input":{"command":"rm -rf dist"}}`
	p := writeTranscript(t, "proj/s1.jsonl",
		asst("a1", "msg_1", "claude-opus-5", read, usage1),
		asst("a2", "msg_2", "claude-opus-5", write, usage1),
	)
	tr, err := Parse(p)
	if err != nil {
		t.Fatal(err)
	}
	got := []string{tr.Turns[0].ToolCalls[0].Class, tr.Turns[1].ToolCalls[0].Class}
	if got[0] != model.ClassRead || got[1] != model.ClassWrite {
		t.Fatalf("classes = %v, want [read write]", got)
	}
}
