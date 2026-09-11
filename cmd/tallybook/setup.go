package main

import (
	"encoding/json"
	"fmt"
	"github.com/magna-nz/tallybook/internal/config"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// sessionEndHookCommand is the command tallybook's SessionEnd hook entry runs.
const sessionEndHookCommand = "tallybook hook session-end"

// hookBlock is the exact JSON block `setup hook` tells the user to add to
// ~/.claude/settings.json, verified against Claude Code's hooks reference
// (code.claude.com/docs/en/hooks, checked 2026-09-11): "SessionEnd" hooks take no
// matcher, so each entry in the array is just a { "hooks": [...] } group.
const hookBlock = `{
  "hooks": {
    "SessionEnd": [
      { "hooks": [ { "type": "command", "command": "tallybook hook session-end" } ] }
    ]
  }
}`

const hookRemovalNote = "Remove it later by deleting that entry from the \"SessionEnd\" list in settings.json."

// hookPurpose says what the hook is for. Claude Code does not show a hook's
// output to the person at the keyboard, so it is worth being explicit that the
// summary goes to a file rather than the terminal.
func hookPurpose() string {
	return "What it does: records each session the moment it ends, so `tallybook` is instant when you\n" +
		"next run it, and appends one line per session to " + filepath.Join(config.Dir(), sessionLogName) + ".\n" +
		"Claude Code does not show a hook's output, so that file is where the line goes."
}

func newSetupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Wire tallybook into a coding agent CLI",
	}
	cmd.AddCommand(newSetupHookCmd())
	return cmd
}

func newSetupHookCmd() *cobra.Command {
	var write bool
	cmd := &cobra.Command{
		Use:   "hook",
		Short: "Print (or add) the Claude Code hook that records each session as it ends",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSetupHook(cmd, write)
		},
	}
	cmd.Flags().BoolVar(&write, "write", false, "merge the hook into ~/.claude/settings.json")
	return cmd
}

// claudeSettingsPath is ~/.claude/settings.json for the current user.
func claudeSettingsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find home directory: %w", err)
	}
	return filepath.Join(home, ".claude", "settings.json"), nil
}

func runSetupHook(cmd *cobra.Command, write bool) error {
	out := cmd.OutOrStdout()

	path, err := claudeSettingsPath()
	if err != nil {
		return err
	}

	if !write {
		fmt.Fprintln(out, hookBlock)
		fmt.Fprintln(out)
		fmt.Fprintln(out, "Add that to", path)
		fmt.Fprintln(out, hookRemovalNote)
		fmt.Fprintln(out)
		fmt.Fprintln(out, hookPurpose())
		return nil
	}

	doc, err := readSettings(path)
	if err != nil {
		return err
	}

	already, err := doc.hasTallybookHook()
	if err != nil {
		return err
	}
	if already {
		fmt.Fprintln(out, "A SessionEnd hook running tallybook is already present in", path)
		fmt.Fprintln(out, "Not writing.")
		return nil
	}

	if err := doc.addHook(); err != nil {
		return err
	}

	if err := doc.writeAtomically(path); err != nil {
		return err
	}

	fmt.Fprintln(out, "Added a SessionEnd hook to", path)
	fmt.Fprintln(out, hookRemovalNote)
	return nil
}

// settingsDoc holds a Claude Code settings.json file loaded at just enough
// resolution to touch "hooks"."SessionEnd" and leave everything else unchanged in value (encoding/json re-indents and sorts keys)
// as encoding/json can preserve it.
type settingsDoc struct {
	top        map[string]json.RawMessage
	hooks      map[string]json.RawMessage
	sessionEnd []json.RawMessage
}

// readSettings loads path, or starts an empty document if it does not
// exist. A file that exists but is not valid JSON is a clear error, and is
// never touched.
func readSettings(path string) (*settingsDoc, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &settingsDoc{top: map[string]json.RawMessage{}, hooks: map[string]json.RawMessage{}}, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return nil, fmt.Errorf("%s is not valid JSON: %w", path, err)
	}
	if top == nil {
		top = map[string]json.RawMessage{}
	}

	hooks := map[string]json.RawMessage{}
	if rawHooks, ok := top["hooks"]; ok {
		if err := json.Unmarshal(rawHooks, &hooks); err != nil {
			return nil, fmt.Errorf(`%s: "hooks" is not a JSON object: %w`, path, err)
		}
	}

	var sessionEnd []json.RawMessage
	if rawSessionEnd, ok := hooks["SessionEnd"]; ok {
		if err := json.Unmarshal(rawSessionEnd, &sessionEnd); err != nil {
			return nil, fmt.Errorf(`%s: "hooks"."SessionEnd" is not a JSON array: %w`, path, err)
		}
	}

	return &settingsDoc{top: top, hooks: hooks, sessionEnd: sessionEnd}, nil
}

// hookHandler is the fields of a command hook handler this command cares
// about. Fields it does not model (args, async, shell, ...) are preserved
// automatically because existing entries are carried as raw JSON, never
// round-tripped through this struct.
type hookHandler struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

// hookGroup is one entry of a "SessionEnd" array: { "matcher"?, "hooks": [...] }.
type hookGroup struct {
	Hooks []json.RawMessage `json:"hooks"`
}

// hasTallybookHook reports whether any existing "SessionEnd" entry already
// runs tallybook.
func (d *settingsDoc) hasTallybookHook() (bool, error) {
	for _, rawGroup := range d.sessionEnd {
		var group hookGroup
		if err := json.Unmarshal(rawGroup, &group); err != nil {
			continue // not a shape we understand; not a match either
		}
		for _, rawHandler := range group.Hooks {
			var h hookHandler
			if err := json.Unmarshal(rawHandler, &h); err != nil {
				continue
			}
			if h.Type == "command" && strings.Contains(h.Command, "tallybook") {
				return true, nil
			}
		}
	}
	return false, nil
}

// addHook appends the tallybook SessionEnd hook group to the document.
func (d *settingsDoc) addHook() error {
	handler, err := json.Marshal(hookHandler{Type: "command", Command: sessionEndHookCommand})
	if err != nil {
		return err
	}
	group, err := json.Marshal(hookGroup{Hooks: []json.RawMessage{handler}})
	if err != nil {
		return err
	}
	d.sessionEnd = append(d.sessionEnd, group)

	rawSessionEnd, err := json.Marshal(d.sessionEnd)
	if err != nil {
		return err
	}
	d.hooks["SessionEnd"] = rawSessionEnd

	rawHooks, err := json.Marshal(d.hooks)
	if err != nil {
		return err
	}
	d.top["hooks"] = rawHooks

	return nil
}

// writeAtomically renders the document and writes it to path via a temp
// file in the same directory followed by a rename, so a crash mid-write can
// never leave a truncated settings.json behind.
func (d *settingsDoc) writeAtomically(path string) error {
	out, err := json.MarshalIndent(d.top, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, ".settings.json.tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file in %s: %w", dir, err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) // no-op once the rename below succeeds

	if _, err := tmp.Write(out); err != nil {
		tmp.Close()
		return fmt.Errorf("write %s: %w", tmpPath, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", tmpPath, err)
	}
	// Keep whatever permissions the file already had: settings.json may hold
	// secrets in its env block, and a user who set it to 0600 meant it.
	mode := os.FileMode(0o600)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	if err := os.Chmod(tmpPath, mode); err != nil {
		return fmt.Errorf("chmod %s: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename %s to %s: %w", tmpPath, path, err)
	}
	return nil
}
