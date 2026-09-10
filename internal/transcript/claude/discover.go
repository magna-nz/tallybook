// Package claude discovers and parses Claude Code session transcripts
// (~/.claude/projects/<slug>/<session>.jsonl and their sub-agent files) into
// the vendor-neutral shapes defined by internal/model.
package claude

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// DefaultRoot returns the default Claude Code transcript root,
// ~/.claude/projects, expanded for the current user.
func DefaultRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "projects"), nil
}

// Discover walks root and returns every transcript path it finds: files
// matching <root>/<slug>/<sessionId>.jsonl and
// <root>/<slug>/<sessionId>/subagents/agent-<id>.jsonl. The result is sorted
// and deterministic. Entries that cannot be read (permission errors,
// vanishing files, etc.) are skipped silently.
func Discover(root string) ([]string, error) {
	var out []string

	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// Unreadable entry: skip it, but keep walking siblings.
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".jsonl") {
			return nil
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		parts := strings.Split(filepath.ToSlash(rel), "/")

		switch len(parts) {
		case 2:
			// <slug>/<sessionId>.jsonl
			out = append(out, path)
		case 4:
			// <slug>/<sessionId>/subagents/agent-<id>.jsonl
			if parts[2] == "subagents" && strings.HasPrefix(parts[3], "agent-") {
				out = append(out, path)
			}
		}
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}

	sort.Strings(out)
	return out, nil
}
