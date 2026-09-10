// Package codex discovers and parses OpenAI Codex CLI rollout transcripts
// into the vendor-neutral shapes defined by package model.
package codex

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// DefaultRoots returns the session directories to scan: $CODEX_HOME or
// ~/.codex, then <that>/sessions and <that>/archived_sessions (only those
// that exist).
func DefaultRoots() ([]string, error) {
	home := os.Getenv("CODEX_HOME")
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		home = filepath.Join(h, ".codex")
	}

	var roots []string
	for _, sub := range []string{"sessions", "archived_sessions"} {
		p := filepath.Join(home, sub)
		if info, err := os.Stat(p); err == nil && info.IsDir() {
			roots = append(roots, p)
		}
	}
	return roots, nil
}

// Discover walks each root recursively and returns every rollout-*.jsonl
// path, sorted. If the same relative path exists under both sessions/ and
// archived_sessions/, keep only the sessions/ copy.
func Discover(roots ...string) ([]string, error) {
	type found struct {
		path      string
		preferred bool
	}
	seen := make(map[string]found)

	for _, root := range roots {
		if _, err := os.Stat(root); err != nil {
			// Root does not exist (or is inaccessible); skip it rather than
			// failing the whole scan.
			continue
		}

		preferred := filepath.Base(filepath.Clean(root)) != "archived_sessions"

		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			name := d.Name()
			if !strings.HasPrefix(name, "rollout-") || !strings.HasSuffix(name, ".jsonl") {
				return nil
			}

			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				rel = path
			}

			if existing, ok := seen[rel]; !ok || (preferred && !existing.preferred) {
				seen[rel] = found{path: path, preferred: preferred}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	paths := make([]string, 0, len(seen))
	for _, f := range seen {
		paths = append(paths, f.path)
	}
	sort.Strings(paths)
	return paths, nil
}
