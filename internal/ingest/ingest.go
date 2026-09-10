// Package ingest walks the configured transcript roots, parses whatever has
// changed since the last run, and stores the result. It never fails the
// whole run for one bad file: parse errors are counted and reported, never
// returned as an error from Sync.
package ingest

import (
	"fmt"
	"os"
	"time"

	"github.com/magna-nz/tallybook/internal/config"
	"github.com/magna-nz/tallybook/internal/model"
	"github.com/magna-nz/tallybook/internal/store"
	"github.com/magna-nz/tallybook/internal/transcript/claude"
	"github.com/magna-nz/tallybook/internal/transcript/codex"
)

// maxErrors bounds how many failure messages Result.Errors keeps.
const maxErrors = 20

// Result summarises one Sync run.
type Result struct {
	Scanned   int
	Ingested  int
	Unchanged int
	Failed    int
	Errors    []string
	Elapsed   time.Duration
}

// parseFunc is the shape shared by claude.Parse and codex.Parse.
type parseFunc func(path string) (*model.Transcript, error)

// Sync discovers every transcript under cfg.ClaudeRoots (or claude.DefaultRoot
// when unset) and cfg.CodexRoots (or codex.DefaultRoots when unset), and for
// each file compares its current size and mtime against what st recorded at
// last ingest. Unchanged files are skipped; new or changed files are parsed
// and stored. A missing root is not an error. A file that fails to parse or
// store is counted in Failed and its path (with the error) is appended to
// Errors, capped at 20; it never aborts the run.
func Sync(cfg config.Config, st *store.Store) (Result, error) {
	return SyncSource(cfg, st, "")
}

// SyncSource is Sync restricted to one source. An empty source means all.
func SyncSource(cfg config.Config, st *store.Store, only model.Source) (Result, error) {
	start := time.Now()
	var res Result

	if only == "" || only == model.SourceClaudeCode {
		if err := syncClaude(cfg, st, &res); err != nil {
			return res, err
		}
	}
	if only == "" || only == model.SourceCodex {
		if err := syncCodex(cfg, st, &res); err != nil {
			return res, err
		}
	}
	res.Elapsed = time.Since(start)
	return res, nil
}

func syncClaude(cfg config.Config, st *store.Store, res *Result) error {
	claudeRoots := cfg.ClaudeRoots
	if len(claudeRoots) == 0 {
		root, err := claude.DefaultRoot()
		if err != nil {
			return fmt.Errorf("ingest: claude default root: %w", err)
		}
		claudeRoots = []string{root}
	}
	for _, root := range claudeRoots {
		paths, err := claude.Discover(root)
		if err != nil {
			return fmt.Errorf("ingest: discover claude transcripts under %s: %w", root, err)
		}
		syncPaths(st, paths, claude.Parse, res)
	}
	return nil
}

func syncCodex(cfg config.Config, st *store.Store, res *Result) error {
	codexRoots := cfg.CodexRoots
	if len(codexRoots) == 0 {
		roots, err := codex.DefaultRoots()
		if err != nil {
			return fmt.Errorf("ingest: codex default roots: %w", err)
		}
		codexRoots = roots
	}
	codexPaths, err := codex.Discover(codexRoots...)
	if err != nil {
		return fmt.Errorf("ingest: discover codex transcripts: %w", err)
	}
	syncPaths(st, codexPaths, codex.Parse, res)
	return nil
}

// syncPaths ingests one source's discovered paths into res.
func syncPaths(st *store.Store, paths []string, parse parseFunc, res *Result) {
	for _, path := range paths {
		res.Scanned++

		fi, err := os.Stat(path)
		if err != nil {
			res.Failed++
			addError(res, path, err)
			continue
		}

		size, mtime, found, err := st.FileState(path)
		if err == nil && found && size == fi.Size() && mtime.Equal(fi.ModTime()) {
			res.Unchanged++
			continue
		}

		t, err := parse(path)
		if err != nil {
			res.Failed++
			addError(res, path, err)
			continue
		}

		if err := st.ReplaceTranscript(t, fi.Size(), fi.ModTime()); err != nil {
			res.Failed++
			addError(res, path, err)
			continue
		}

		res.Ingested++
	}
}

// addError appends a "<path>: <err>" message to res.Errors, capped at
// maxErrors.
func addError(res *Result, path string, err error) {
	if len(res.Errors) >= maxErrors {
		return
	}
	res.Errors = append(res.Errors, fmt.Sprintf("%s: %v", path, err))
}
