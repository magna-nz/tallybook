package codex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/magna-nz/tallybook/internal/model"
)

func writeRollout(t *testing.T, lines ...string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "2026", "09", "03", "rollout-2026-09-03T10-00-00-thr_x.jsonl")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

const meta = `{"timestamp":"2026-09-03T10:00:00.000Z","type":"session_meta","payload":{"id":"thr_x","session_id":"thr_x","timestamp":"2026-09-03T10:00:00.000Z","cwd":"/home/user/p","originator":"codex_cli_rs","cli_version":"0.140.0"}}`
const ctx = `{"timestamp":"2026-09-03T10:00:01.000Z","type":"turn_context","payload":{"turn_id":"t1","cwd":"/home/user/p","approval_policy":"on-request","sandbox_policy":{"type":"workspace-write"},"network":null,"model":"gpt-5.5","personality":null}}`

func count(ts string, total, last string) string {
	return `{"timestamp":"` + ts + `","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":` + total + `,"last_token_usage":` + last + `,"model_context_window":400000},"rate_limits":null}}`
}

const u1 = `{"input_tokens":1000,"cached_input_tokens":200,"cache_write_input_tokens":0,"output_tokens":50,"reasoning_output_tokens":0,"total_tokens":1050}`
const u2 = `{"input_tokens":2500,"cached_input_tokens":1200,"cache_write_input_tokens":0,"output_tokens":90,"reasoning_output_tokens":0,"total_tokens":2590}`

func TestNullLocalShellCallIDsDoNotCollide(t *testing.T) {
	shell := `{"timestamp":"2026-09-03T10:00:02.000Z","type":"response_item","payload":{"type":"local_shell_call","call_id":null,"status":"completed","action":{"type":"exec","command":["ls","-la"]}}}`
	p := writeRollout(t, meta, ctx, shell, shell, count("2026-09-03T10:00:03.000Z", u1, u1))
	tr, err := Parse(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(tr.Turns) != 1 || len(tr.Turns[0].ToolCalls) != 2 {
		t.Fatalf("want one turn with two calls, got %+v", tr.Turns)
	}
	a, b := tr.Turns[0].ToolCalls[0], tr.Turns[0].ToolCalls[1]
	if a.ID == "" || a.ID == b.ID {
		t.Fatalf("null call ids must become distinct ids, got %q and %q", a.ID, b.ID)
	}
	if a.Class != model.ClassRead {
		t.Fatalf("ls -la should classify as read, got %q", a.Class)
	}
}

func TestShellArgumentsAreClassifiedAcrossShapes(t *testing.T) {
	cases := map[string]string{
		`{"cmd":"git status"}`:                       model.ClassRead,
		`{"command":"rm -rf build"}`:                 model.ClassWrite,
		`{"command":["bash","-lc","go test ./..."]}`: model.ClassRead,
		`{"command":["bash","-lc","npm install"]}`:   model.ClassWrite,
		`{"command":"python3 x.py"}`:                 "",
	}
	for args, want := range cases {
		if got := classifyShellArgs("exec_command", []byte(args)); got != want {
			t.Errorf("classifyShellArgs(%s) = %q, want %q", args, got, want)
		}
	}
	if got := classifyShellArgs("some_other_tool", []byte(`{"cmd":"rm -rf /"}`)); got != "" {
		t.Errorf("non-shell tools must not be classified, got %q", got)
	}
}

func TestTokenCountDeltasAcrossTurns(t *testing.T) {
	p := writeRollout(t, meta, ctx,
		count("2026-09-03T10:00:03.000Z", u1, u1),
		count("2026-09-03T10:00:04.000Z", u1, u1), // duplicate, same total
		count("2026-09-03T10:00:05.000Z", u2, u1), // advanced total
	)
	tr, err := Parse(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(tr.Turns) != 2 {
		t.Fatalf("want 2 turns (duplicate ignored), got %d", len(tr.Turns))
	}
	second := tr.Turns[1].Usage
	if second.Input != 500 || second.CacheRead != 1000 || second.Output != 40 {
		t.Fatalf("second turn should be the delta of totals, got %+v", second)
	}
}

func TestMissingSessionMetaIsAnError(t *testing.T) {
	p := writeRollout(t, ctx, count("2026-09-03T10:00:03.000Z", u1, u1))
	if _, err := Parse(p); err == nil {
		t.Fatal("expected an error without session_meta")
	}
}
