package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/magna-nz/tallybook/internal/transcript/claude"
)

// runHook is like the run helper in e2e_test.go but lets the caller supply
// stdin, which "hook session-end" reads.
func runHook(t *testing.T, stdin string, args ...string) (string, error) {
	t.Helper()
	root := newRoot()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetIn(strings.NewReader(stdin))
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

// TestHookSessionEndWithTranscriptPrintsSummary feeds "hook session-end" a SessionEnd JSON
// payload naming the checked-in Claude Code fixture on stdin, and checks it
// prints exactly one line containing a dollar figure.
func TestHookSessionEndWithTranscriptPrintsSummary(t *testing.T) {
	e2eEnv(t)

	transcriptPath := filepath.Join(repoRoot(t), "internal", "transcript", "claude", "testdata", "projects", "-home-user-project", "sess-0001.jsonl")
	stdin := `{
		"session_id": "sess-0001",
		"transcript_path": "` + filepath.ToSlash(transcriptPath) + `",
		"cwd": "/home/user/project",
		"hook_event_name": "SessionEnd", "reason": "prompt_input_exit"
	}`

	out, err := runHook(t, stdin, "hook", "session-end")
	if err != nil {
		t.Fatalf("hook session-end: %v\noutput:\n%s", err, out)
	}

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 1 || lines[0] == "" {
		t.Fatalf("hook session-end printed %d lines, want exactly 1: %q", len(lines), out)
	}
	if !usdAmountRe.MatchString(out) && !strings.Contains(out, "$0.00") {
		t.Errorf("hook session-end output has no dollar figure: %q", out)
	}
	if !strings.Contains(out, "tallybook:") {
		t.Errorf("hook session-end output missing \"tallybook:\": %q", out)
	}
}

// TestHookSessionEndNoStdinEmptyDBPrintsNothing checks that with no stdin and
// nothing ingested, "hook session-end" prints nothing and reports success.
func TestHookSessionEndNoStdinEmptyDBPrintsNothing(t *testing.T) {
	dir := t.TempDir()
	assertUnderTempDir(t, dir)
	t.Setenv("TALLYBOOK_DIR", dir)
	t.Setenv("TALLYBOOK_DB", filepath.Join(dir, "tallybook.db"))
	// Point both roots at empty directories so ingest finds nothing: the
	// database really is empty, not just unscanned.
	emptyRoot := filepath.Join(t.TempDir(), "empty")
	t.Setenv("TALLYBOOK_CLAUDE_ROOTS", emptyRoot)
	t.Setenv("TALLYBOOK_CODEX_ROOTS", emptyRoot)
	t.Setenv("TALLYBOOK_PLAN", "api")

	out, err := runHook(t, "", "hook", "session-end")
	if err != nil {
		t.Fatalf("hook session-end with no stdin: %v\noutput:\n%s", err, out)
	}
	if out != "" {
		t.Errorf("hook session-end with no stdin and an empty db printed %q, want nothing", out)
	}
}

// TestHookSessionEndNeverFails exercises inputs that would make most commands
// return an error, and checks that "hook session-end" instead exits cleanly every
// time: malformed JSON on stdin, and a --db path that cannot be created.
func TestHookSessionEndNeverFails(t *testing.T) {
	t.Run("malformed stdin JSON", func(t *testing.T) {
		e2eEnv(t)
		out, err := runHook(t, "{not valid json", "hook", "session-end")
		if err != nil {
			t.Fatalf("hook session-end with malformed stdin returned an error: %v\noutput:\n%s", err, out)
		}
	})

	t.Run("unusable --db path", func(t *testing.T) {
		dir := t.TempDir()
		// A regular file where a directory is needed makes os.MkdirAll (and
		// so openApp) fail for every other command.
		blocker := filepath.Join(dir, "blocker")
		if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		badDB := filepath.Join(blocker, "sub", "tallybook.db")

		out, err := runHook(t, "", "--db", badDB, "hook", "session-end")
		if err != nil {
			t.Fatalf("hook session-end with an unusable --db path returned an error: %v\noutput:\n%s", err, out)
		}
		if out != "" {
			t.Errorf("hook session-end with an unusable --db path printed %q, want nothing", out)
		}
	})
}

// TestClaudeParsingIsSeparatorAgnostic is part of the Windows audit for this
// change: it builds a Claude Code transcript tree with filepath.Join
// (native separators, whatever they are on the OS running the test) instead
// of hardcoded "/", and checks that discovery and sub-agent detection still
// work. internal/transcript/claude/discover.go and parse.go both normalise
// a path with filepath.ToSlash before splitting or matching it against
// "/subagents/", so this exercises exactly the code hook.go depends on to
// resolve a session from --transcript-path. It lives here, rather than in
// the claude package's own tests, because this change's scope only allows
// creating new files under cmd/tallybook.
func TestClaudeParsingIsSeparatorAgnostic(t *testing.T) {
	root := t.TempDir()
	assertUnderTempDir(t, root)

	slugDir := filepath.Join(root, "proj-slug")
	mainDir := filepath.Join(slugDir, "sess-main")
	subDir := filepath.Join(mainDir, "subagents")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}

	mainPath := filepath.Join(slugDir, "sess-main.jsonl")
	mainLines := strings.Join([]string{
		`{"type":"user","sessionId":"sess-main","cwd":"/some/project","timestamp":"2026-09-01T10:00:00.000Z","message":{"role":"user","content":"hi"}}`,
		`{"type":"assistant","sessionId":"sess-main","timestamp":"2026-09-01T10:00:01.000Z","requestId":"req_1","message":{"id":"m1","model":"claude-opus-5","role":"assistant","type":"message","content":[{"type":"text","text":"hi"}],"usage":{"input_tokens":10,"output_tokens":5}}}`,
		"",
	}, "\n")
	if err := os.WriteFile(mainPath, []byte(mainLines), 0o644); err != nil {
		t.Fatal(err)
	}

	subPath := filepath.Join(subDir, "agent-child1.jsonl")
	subLines := strings.Join([]string{
		`{"type":"user","sessionId":"sess-main","agentId":"child1","isSidechain":true,"cwd":"/some/project","timestamp":"2026-09-01T10:00:02.000Z","message":{"role":"user","content":"go"}}`,
		`{"type":"assistant","sessionId":"sess-main","agentId":"child1","isSidechain":true,"timestamp":"2026-09-01T10:00:03.000Z","requestId":"req_2","message":{"id":"m2","model":"claude-sonnet-5","role":"assistant","type":"message","content":[{"type":"text","text":"done"}],"usage":{"input_tokens":4,"output_tokens":2}}}`,
		"",
	}, "\n")
	if err := os.WriteFile(subPath, []byte(subLines), 0o644); err != nil {
		t.Fatal(err)
	}

	paths, err := claude.Discover(root)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(paths) != 2 {
		t.Fatalf("Discover found %d paths, want 2: %v", len(paths), paths)
	}

	mainT, err := claude.Parse(mainPath)
	if err != nil {
		t.Fatalf("Parse(main): %v", err)
	}
	if mainT.Session.AgentID != "" {
		t.Errorf("main session AgentID = %q, want empty (not a sub-agent)", mainT.Session.AgentID)
	}
	if mainT.Session.ID != "sess-main" {
		t.Errorf("main session ID = %q, want %q", mainT.Session.ID, "sess-main")
	}
	if mainT.Session.Project != "/some/project" {
		t.Errorf("main session Project = %q, want %q (unmodified from cwd)", mainT.Session.Project, "/some/project")
	}

	subT, err := claude.Parse(subPath)
	if err != nil {
		t.Fatalf("Parse(sub-agent): %v", err)
	}
	if subT.Session.AgentID != "child1" {
		t.Errorf("sub-agent AgentID = %q, want %q", subT.Session.AgentID, "child1")
	}
	if subT.Session.ParentSessionID != "sess-main" {
		t.Errorf("sub-agent ParentSessionID = %q, want %q", subT.Session.ParentSessionID, "sess-main")
	}
	if subT.Session.ID != "sess-main/agent-child1" {
		t.Errorf("sub-agent ID = %q, want %q", subT.Session.ID, "sess-main/agent-child1")
	}
}

// A SessionEnd hook has about 1.5 seconds before Claude Code gives up on it,
// and no hook's stdout reaches the person at the keyboard. So the hook must be
// quick, and the line has to land somewhere the user can actually read.
func TestHookSessionEndIsFastAndLogsToAFile(t *testing.T) {
	e2eEnv(t)
	fixture := filepath.Join(repoRoot(t), "internal", "transcript", "claude",
		"testdata", "projects", "-home-user-project", "sess-0001.jsonl")

	input := `{"session_id":"sess-0001","transcript_path":"` + fixture +
		`","hook_event_name":"SessionEnd","reason":"prompt_input_exit"}`

	start := time.Now()
	out, err := runHook(t, input, "hook", "session-end")
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("a hook must never fail the session: %v", err)
	}
	if elapsed > time.Second {
		t.Errorf("took %s; a SessionEnd hook has about 1.5s before it is abandoned", elapsed)
	}
	if !strings.Contains(out, "$") {
		t.Errorf("expected a cost in the summary line, got %q", out)
	}

	logPath := filepath.Join(os.Getenv("TALLYBOOK_DIR"), "sessions.log")
	body, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("the hook must write the line somewhere readable: %v", err)
	}
	if !strings.Contains(string(body), "$") {
		t.Errorf("log line missing the summary: %q", string(body))
	}

	// Running again appends rather than replacing, so the log is a history.
	if _, err := runHook(t, input, "hook", "session-end"); err != nil {
		t.Fatal(err)
	}
	body2, _ := os.ReadFile(logPath)
	if strings.Count(string(body2), "\n") != 2 {
		t.Errorf("expected two log lines after two runs, got:\n%s", string(body2))
	}
}

// The hook ingests only the transcript it was handed, so a large history does
// not blow the timeout. With --no-ingest implied, a session it was never told
// about must still not appear.
func TestHookSessionEndIngestsOnlyTheNamedTranscript(t *testing.T) {
	e2eEnv(t)
	fixture := filepath.Join(repoRoot(t), "internal", "transcript", "claude",
		"testdata", "projects", "-home-user-project", "sess-0001.jsonl")

	input := `{"transcript_path":"` + fixture + `","hook_event_name":"SessionEnd"}`
	if _, err := runHook(t, input, "hook", "session-end"); err != nil {
		t.Fatal(err)
	}

	out, err := run(t, "--no-ingest", "--since", "all", "--json", "sessions")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "sess-0001") {
		t.Errorf("the named transcript should have been recorded: %s", out)
	}
	if strings.Contains(out, "thr_0001") {
		t.Errorf("a Codex session was ingested that the hook was never given: %s", out)
	}
}
