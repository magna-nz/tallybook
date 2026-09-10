package config

import (
	"os"
	"path/filepath"
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
}

func TestLoadFileAndEnv(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.toml")
	body := "plan = \"subscription\"\n[findings]\nmin_runs = 9\ndisabled = [\"x\"]\n[prices.\"m\"]\ninput = 1\noutput = 2\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TALLYBOOK_DB", "~/x.db")
	t.Setenv("TALLYBOOK_CLAUDE_ROOTS", "/a:/b")
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
	if _, err := LoadFrom(p); err != nil {
		t.Fatal(err)
	}
}
