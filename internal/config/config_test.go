package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLoadMissingFileGivesDefaults(t *testing.T) {
	t.Setenv("TALLYBOOK_PLAN", "")
	t.Setenv("TALLYBOOK_DB", "")
	cfg, err := LoadFrom(filepath.Join(t.TempDir(), "nope.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Plan != PlanAuto || cfg.Findings.MinRuns != 3 || cfg.DefaultSince != "30d" {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
	if cfg.Findings.MinSavingUSD != 1.0 || cfg.Findings.ReportLimit != 5 {
		t.Fatalf("unexpected report defaults: %+v", cfg.Findings)
	}
}

func TestReportThresholdsFromFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	body := "[findings]\nmin_saving_usd = 2.5\nreport_limit = 3\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFrom(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Findings.MinSavingUSD != 2.5 || cfg.Findings.ReportLimit != 3 {
		t.Fatalf("report thresholds not applied: %+v", cfg.Findings)
	}
}

func TestPlanPriceFromFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	body := "[findings]\nplan_price = 200\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFrom(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Findings.PlanPriceUSD != 200 {
		t.Fatalf("plan_price not applied: %+v", cfg.Findings)
	}
}

func TestPlanPriceDefaultsToZero(t *testing.T) {
	cfg := Default()
	if cfg.Findings.PlanPriceUSD != 0 {
		t.Fatalf("PlanPriceUSD default = %v, want 0", cfg.Findings.PlanPriceUSD)
	}
}

func TestLoadFileAndEnv(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.toml")
	body := "plan = \"subscription\"\n[findings]\nmin_runs = 9\ndisabled = [\"x\"]\n[prices.\"m\"]\ninput = 1\noutput = 2\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TALLYBOOK_DB", "~/x.db")
	t.Setenv("TALLYBOOK_CLAUDE_ROOTS", "/a"+string(os.PathListSeparator)+"/b")
	cfg, err := LoadFrom(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Plan != PlanSubscription || cfg.Findings.MinRuns != 9 || cfg.Findings.Disabled[0] != "x" {
		t.Fatalf("file not applied: %+v", cfg)
	}
	if cfg.Prices["m"].Output != 2 {
		t.Fatalf("price override missing: %+v", cfg.Prices)
	}
	if filepath.Base(cfg.DBPath) != "x.db" || filepath.IsAbs(cfg.DBPath) == false {
		t.Fatalf("home not expanded: %s", cfg.DBPath)
	}
	if len(cfg.ClaudeRoots) != 2 || cfg.ClaudeRoots[1] != "/b" {
		t.Fatalf("roots env not applied: %v", cfg.ClaudeRoots)
	}
}

func TestBadPlanRejected(t *testing.T) {
	t.Setenv("TALLYBOOK_PLAN", "gold")
	if _, err := LoadFrom(filepath.Join(t.TempDir(), "nope.toml")); err == nil {
		t.Fatal("expected error for bad plan")
	}
}

func TestExampleParses(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte(Example), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TALLYBOOK_PLAN", "")
	cfg, err := LoadFrom(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Findings.MinSavingUSD != 1.0 || cfg.Findings.ReportLimit != 5 {
		t.Fatalf("example does not carry the report thresholds: %+v", cfg.Findings)
	}
}

// A Windows user writes "~\path". Leaving it literal would create a directory
// called "~" wherever the tool happened to be run from.
func TestExpandHomeAcceptsBothSeparators(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("TALLYBOOK_PLAN", "")

	for _, raw := range []string{"~/tallybook/x.db", `~\tallybook\x.db`} {
		t.Setenv("TALLYBOOK_DB", raw)
		cfg, err := LoadFrom(filepath.Join(t.TempDir(), "nope.toml"))
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(cfg.DBPath, "~") {
			t.Errorf("%q was left unexpanded: %q", raw, cfg.DBPath)
		}
		if filepath.Base(cfg.DBPath) != "x.db" {
			t.Errorf("%q expanded to %q, which does not end at the file", raw, cfg.DBPath)
		}
	}
}

// A backslash is an ordinary character in a Unix filename. Rewriting it as a
// separator turns one file into a directory that does not exist.
func TestExpandHomeKeepsBackslashesInAUnixPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a backslash is a separator on Windows, so there is nothing to preserve")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // os.UserHomeDir reads this on Windows
	t.Setenv("TALLYBOOK_PLAN", "")
	t.Setenv("TALLYBOOK_DB", `~/odd\name.db`)

	cfg, err := LoadFrom(filepath.Join(t.TempDir(), "nope.toml"))
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, `odd\name.db`)
	if cfg.DBPath != want {
		t.Errorf("DBPath = %q, want %q: the backslash is part of the file name", cfg.DBPath, want)
	}
}
