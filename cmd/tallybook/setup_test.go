package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// runSetup executes the real CLI in-process with args, the way run() in
// e2e_test.go does for other commands.
func runSetup(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := newRoot()
	var out strings.Builder
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

// setupTestHome points a fresh, temp-dir HOME at the caller so
// claudeSettingsPath() (via os.UserHomeDir) never touches the real user's
// ~/.claude.
func setupTestHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	assertUnderTempDir(t, home)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // os.UserHomeDir on Windows
	return home
}

func TestSetupHookWithoutWriteCreatesNothing(t *testing.T) {
	home := setupTestHome(t)
	settingsPath := filepath.Join(home, ".claude", "settings.json")

	out, err := runSetup(t, "setup", "hook")
	if err != nil {
		t.Fatalf("setup hook: %v\noutput:\n%s", err, out)
	}

	if _, err := os.Stat(settingsPath); !os.IsNotExist(err) {
		t.Fatalf("setup hook without --write created %s (stat err: %v)", settingsPath, err)
	}
	if !strings.Contains(out, `"tallybook hook session-end"`) {
		t.Errorf("setup hook output missing the hook command: %q", out)
	}
	if !strings.Contains(out, settingsPath) {
		t.Errorf("setup hook output missing the settings path %q: %q", settingsPath, out)
	}
}

func TestSetupHookWriteAddsHookAndPreservesExistingKeys(t *testing.T) {
	home := setupTestHome(t)
	settingsPath := filepath.Join(home, ".claude", "settings.json")

	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	original := `{
  "model": "opusplan",
  "permissions": {
    "allow": ["Bash(git *)"]
  },
  "hooks": {
    "PreToolUse": [
      { "matcher": "Bash", "hooks": [ { "type": "command", "command": "echo hi" } ] }
    ]
  }
}`
	if err := os.WriteFile(settingsPath, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := runSetup(t, "setup", "hook", "--write")
	if err != nil {
		t.Fatalf("setup hook --write: %v\noutput:\n%s", err, out)
	}

	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read %s: %v", settingsPath, err)
	}

	var doc struct {
		Model       string `json:"model"`
		Permissions struct {
			Allow []string `json:"allow"`
		} `json:"permissions"`
		Hooks struct {
			PreToolUse []struct {
				Matcher string `json:"matcher"`
				Hooks   []struct {
					Type    string `json:"type"`
					Command string `json:"command"`
				} `json:"hooks"`
			} `json:"PreToolUse"`
			Stop []struct {
				Hooks []struct {
					Type    string `json:"type"`
					Command string `json:"command"`
				} `json:"hooks"`
			} `json:"SessionEnd"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("settings.json did not parse after write: %v\n%s", err, data)
	}

	// Pre-existing keys survive byte-for-byte in value.
	if doc.Model != "opusplan" {
		t.Errorf("model = %q, want %q", doc.Model, "opusplan")
	}
	if len(doc.Permissions.Allow) != 1 || doc.Permissions.Allow[0] != "Bash(git *)" {
		t.Errorf("permissions.allow = %v, want [\"Bash(git *)\"]", doc.Permissions.Allow)
	}
	if len(doc.Hooks.PreToolUse) != 1 || doc.Hooks.PreToolUse[0].Matcher != "Bash" ||
		len(doc.Hooks.PreToolUse[0].Hooks) != 1 || doc.Hooks.PreToolUse[0].Hooks[0].Command != "echo hi" {
		t.Errorf("PreToolUse hook was not preserved: %+v", doc.Hooks.PreToolUse)
	}

	// The Stop hook was added.
	found := false
	for _, group := range doc.Hooks.Stop {
		for _, h := range group.Hooks {
			if h.Type == "command" && h.Command == "tallybook hook session-end" {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("SessionEnd hooks do not include tallybook: %+v", doc.Hooks.Stop)
	}

	if !strings.Contains(out, settingsPath) {
		t.Errorf("setup hook --write output missing the settings path: %q", out)
	}
}

func TestSetupHookWriteTwiceDoesNotDuplicate(t *testing.T) {
	setupTestHome(t)

	if _, err := runSetup(t, "setup", "hook", "--write"); err != nil {
		t.Fatalf("first setup hook --write: %v", err)
	}
	out2, err := runSetup(t, "setup", "hook", "--write")
	if err != nil {
		t.Fatalf("second setup hook --write: %v\noutput:\n%s", err, out2)
	}
	if !strings.Contains(out2, "already present") {
		t.Errorf("second setup hook --write output missing an already-present notice: %q", out2)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil {
		t.Fatalf("read settings.json: %v", err)
	}
	if n := strings.Count(string(data), "tallybook hook session-end"); n != 1 {
		t.Errorf("settings.json contains the hook command %d times, want 1:\n%s", n, data)
	}
}

func TestSetupHookWriteWithInvalidJSONFailsAndLeavesFileUntouched(t *testing.T) {
	home := setupTestHome(t)
	settingsPath := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	const broken = `{ "hooks": { "SessionEnd": [ oops`
	if err := os.WriteFile(settingsPath, []byte(broken), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := runSetup(t, "setup", "hook", "--write")
	if err == nil {
		t.Fatalf("setup hook --write over invalid JSON succeeded, want an error\noutput:\n%s", out)
	}
	if !strings.Contains(err.Error(), "not valid JSON") {
		t.Errorf("error does not say the file is not valid JSON: %v", err)
	}

	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read %s: %v", settingsPath, err)
	}
	if string(data) != broken {
		t.Errorf("settings.json was modified despite the parse error:\nwant %q\ngot  %q", broken, data)
	}

	// No stray temp files left behind in the directory either.
	entries, err := os.ReadDir(filepath.Dir(settingsPath))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "settings.json" {
			t.Errorf("unexpected file left in .claude: %s", e.Name())
		}
	}
}

// settings.json can hold secrets in its env block. A user who set it to 0600
// meant it, and writing the file back must not widen that.
func TestSetupHookPreservesFilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		// Windows has no Unix permission bits; Go reports 0666 for any
		// writable file, so there is nothing here to preserve or to check.
		t.Skip("file modes are not meaningful on Windows")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte(`{"model":"opus"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := run(t, "setup", "hook", "--write"); err != nil {
		t.Fatalf("setup hook --write: %v", err)
	}

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Errorf("permissions widened from 0600 to %#o", got)
	}
}
