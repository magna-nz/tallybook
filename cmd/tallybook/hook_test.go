package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runHook is like the run helper in e2e_test.go but lets the caller supply
// stdin, which "hook stop" reads.
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

// TestHookStopWithTranscriptPrintsSummary feeds "hook stop" a Stop-hook JSON
// payload naming the checked-in Claude Code fixture on stdin, and checks it
// prints exactly one line containing a dollar figure.
func TestHookStopWithTranscriptPrintsSummary(t *testing.T) {
	e2eEnv(t)

	transcriptPath := filepath.Join(repoRoot(t), "internal", "transcript", "claude", "testdata", "projects", "-home-user-project", "sess-0001.jsonl")
	stdin := `{
		"session_id": "sess-0001",
		"transcript_path": "` + filepath.ToSlash(transcriptPath) + `",
		"cwd": "/home/user/project",
		"hook_event_name": "Stop"
	}`

	out, err := runHook(t, stdin, "hook", "stop")
	if err != nil {
		t.Fatalf("hook stop: %v\noutput:\n%s", err, out)
	}

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 1 || lines[0] == "" {
		t.Fatalf("hook stop printed %d lines, want exactly 1: %q", len(lines), out)
	}
	if !usdAmountRe.MatchString(out) && !strings.Contains(out, "$0.00") {
		t.Errorf("hook stop output has no dollar figure: %q", out)
	}
	if !strings.Contains(out, "tallybook:") {
		t.Errorf("hook stop output missing \"tallybook:\": %q", out)
	}
}

// TestHookStopNoStdinEmptyDBPrintsNothing checks that with no stdin and
// nothing ingested, "hook stop" prints nothing and reports success.
func TestHookStopNoStdinEmptyDBPrintsNothing(t *testing.T) {
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

	out, err := runHook(t, "", "hook", "stop")
	if err != nil {
		t.Fatalf("hook stop with no stdin: %v\noutput:\n%s", err, out)
	}
	if out != "" {
		t.Errorf("hook stop with no stdin and an empty db printed %q, want nothing", out)
	}
}

// TestHookStopNeverFails exercises inputs that would make most commands
// return an error, and checks that "hook stop" instead exits cleanly every
// time: malformed JSON on stdin, and a --db path that cannot be created.
func TestHookStopNeverFails(t *testing.T) {
	t.Run("malformed stdin JSON", func(t *testing.T) {
		e2eEnv(t)
		out, err := runHook(t, "{not valid json", "hook", "stop")
		if err != nil {
			t.Fatalf("hook stop with malformed stdin returned an error: %v\noutput:\n%s", err, out)
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

		out, err := runHook(t, "", "--db", badDB, "hook", "stop")
		if err != nil {
			t.Fatalf("hook stop with an unusable --db path returned an error: %v\noutput:\n%s", err, out)
		}
		if out != "" {
			t.Errorf("hook stop with an unusable --db path printed %q, want nothing", out)
		}
	})
}
