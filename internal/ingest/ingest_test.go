package ingest_test

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/magna-nz/tallybook/internal/config"
	"github.com/magna-nz/tallybook/internal/ingest"
	"github.com/magna-nz/tallybook/internal/store"
)

// testdataRoots resolves the claude and codex testdata roots relative to
// this test file, so the test works regardless of the working directory it
// is run from.
func testdataRoots(t *testing.T) (claudeRoot, codexRoot string) {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	internalDir := filepath.Dir(filepath.Dir(file)) // .../internal
	claudeRoot = filepath.Join(internalDir, "transcript", "claude", "testdata", "projects")
	codexRoot = filepath.Join(internalDir, "transcript", "codex", "testdata", "sessions")
	return claudeRoot, codexRoot
}

func TestSyncIngestsAndIsIdempotent(t *testing.T) {
	claudeRoot, codexRoot := testdataRoots(t)

	cfg := config.Default()
	cfg.ClaudeRoots = []string{claudeRoot}
	cfg.CodexRoots = []string{codexRoot}

	dbPath := filepath.Join(t.TempDir(), "tallybook.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	res, err := ingest.Sync(cfg, st)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if res.Scanned != 4 {
		t.Errorf("first sync Scanned = %d, want 4 (%+v)", res.Scanned, res)
	}
	if res.Ingested != 4 {
		t.Errorf("first sync Ingested = %d, want 4 (%+v)", res.Ingested, res)
	}
	if res.Failed != 0 {
		t.Errorf("first sync Failed = %d, want 0 (%+v)", res.Failed, res)
	}

	res2, err := ingest.Sync(cfg, st)
	if err != nil {
		t.Fatalf("second Sync: %v", err)
	}
	if res2.Scanned != 4 {
		t.Errorf("second sync Scanned = %d, want 4 (%+v)", res2.Scanned, res2)
	}
	if res2.Unchanged != 4 {
		t.Errorf("second sync Unchanged = %d, want 4 (%+v)", res2.Unchanged, res2)
	}
	if res2.Ingested != 0 {
		t.Errorf("second sync Ingested = %d, want 0 (%+v)", res2.Ingested, res2)
	}
}

func TestSyncMissingRootsAreNotErrors(t *testing.T) {
	cfg := config.Default()
	cfg.ClaudeRoots = []string{filepath.Join(t.TempDir(), "does-not-exist")}
	cfg.CodexRoots = []string{filepath.Join(t.TempDir(), "also-missing")}

	dbPath := filepath.Join(t.TempDir(), "tallybook.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	res, err := ingest.Sync(cfg, st)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if res.Scanned != 0 || res.Failed != 0 {
		t.Errorf("Sync over missing roots = %+v, want a no-op", res)
	}
}
