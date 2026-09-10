package ledger_test

import (
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/magna-nz/tallybook/internal/config"
	"github.com/magna-nz/tallybook/internal/ingest"
	"github.com/magna-nz/tallybook/internal/ledger"
	"github.com/magna-nz/tallybook/internal/pricing"
	"github.com/magna-nz/tallybook/internal/store"
)

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

func mustIngest(t *testing.T) *store.Store {
	t.Helper()
	claudeRoot, codexRoot := testdataRoots(t)

	cfg := config.Default()
	cfg.ClaudeRoots = []string{claudeRoot}
	cfg.CodexRoots = []string{codexRoot}

	dbPath := filepath.Join(t.TempDir(), "tallybook.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	if _, err := ingest.Sync(cfg, st); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	return st
}

func TestSessionsAfterIngest(t *testing.T) {
	st := mustIngest(t)
	pr := pricing.Default()

	rows, err := ledger.Sessions(st, pr, store.Filter{})
	if err != nil {
		t.Fatalf("Sessions: %v", err)
	}
	if len(rows) != 4 {
		t.Fatalf("Sessions returned %d rows, want 4", len(rows))
	}

	var parent, codexParent *ledger.SessionCost
	for i := range rows {
		r := &rows[i]
		if r.ID == "sess-0001" {
			parent = r
		}
		if r.ID == "thr_0001" {
			codexParent = r
		}
	}

	if parent == nil {
		t.Fatal("Claude parent session sess-0001 not found")
	}
	if parent.USD <= 0 {
		t.Errorf("Claude parent USD = %v, want > 0", parent.USD)
	}

	if codexParent == nil {
		t.Fatal("Codex session thr_0001 not found")
	}
	if codexParent.USD <= 0 {
		t.Errorf("Codex thr_0001 USD = %v, want > 0", codexParent.USD)
	}
	if !codexParent.Known {
		t.Errorf("Codex thr_0001 Known = false, want true")
	}
}

func TestTotal(t *testing.T) {
	st := mustIngest(t)
	pr := pricing.Default()

	rows, err := ledger.Sessions(st, pr, store.Filter{})
	if err != nil {
		t.Fatalf("Sessions: %v", err)
	}
	tot, err := ledger.Total(rows, st, pr)
	if err != nil {
		t.Fatalf("Total: %v", err)
	}
	if tot.Sessions != 4 {
		t.Errorf("Totals.Sessions = %d, want 4", tot.Sessions)
	}
	if tot.USD <= 0 {
		t.Errorf("Totals.USD = %v, want > 0", tot.USD)
	}
	if tot.Subagents == 0 {
		t.Errorf("Totals.Subagents = 0, want at least the one Claude sub-agent session")
	}
	if got, want := tot.MainUSD+tot.SubagentUSD, tot.USD; got < want-0.001 || got > want+0.001 {
		t.Errorf("MainUSD + SubagentUSD = %v, want %v", got, want)
	}
}

func TestParseSince(t *testing.T) {
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)

	for _, s := range []string{"7d", "30d", "90d"} {
		w, err := ledger.ParseSince(s, now)
		if err != nil {
			t.Fatalf("ParseSince(%q): %v", s, err)
		}
		if w.Until != now {
			t.Errorf("ParseSince(%q).Until = %v, want %v", s, w.Until, now)
		}
		if w.Since.After(now) || w.Since.Equal(now) {
			t.Errorf("ParseSince(%q).Since = %v, want before %v", s, w.Since, now)
		}
		if w.Days <= 0 {
			t.Errorf("ParseSince(%q).Days = %v, want > 0", s, w.Days)
		}
	}

	all, err := ledger.ParseSince("all", now)
	if err != nil {
		t.Fatalf("ParseSince(all): %v", err)
	}
	if !all.Since.IsZero() {
		t.Errorf("ParseSince(all).Since = %v, want zero", all.Since)
	}

	dated, err := ledger.ParseSince("2026-08-01", now)
	if err != nil {
		t.Fatalf("ParseSince(date): %v", err)
	}
	if dated.Since.Format("2006-01-02") != "2026-08-01" {
		t.Errorf("ParseSince(date).Since = %v, want 2026-08-01", dated.Since)
	}

	if _, err := ledger.ParseSince("not-a-window", now); err == nil {
		t.Error("ParseSince(garbage) = nil error, want an error")
	}
}

func TestAllSince(t *testing.T) {
	st := mustIngest(t)
	earliest := ledger.AllSince(st)
	if earliest.IsZero() {
		t.Fatal("AllSince returned zero time")
	}
	if earliest.Year() != 2026 {
		t.Errorf("AllSince = %v, want a 2026 date", earliest)
	}
}
