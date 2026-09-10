package main

import (
	"fmt"
	"github.com/magna-nz/tallybook/internal/model"
	"os"
	"path/filepath"
	"time"

	"github.com/magna-nz/tallybook/internal/config"
	"github.com/magna-nz/tallybook/internal/ingest"
	"github.com/magna-nz/tallybook/internal/ledger"
	"github.com/magna-nz/tallybook/internal/pricing"
	"github.com/magna-nz/tallybook/internal/store"
)

// usageError marks an error caused by bad input (a flag or argument), so
// main can exit 2 instead of 1.
type usageError struct{ err error }

func (e usageError) Error() string { return e.err.Error() }
func (e usageError) Unwrap() error { return e.err }

func usageErrorf(format string, a ...interface{}) error {
	return usageError{fmt.Errorf(format, a...)}
}

// globalFlags holds the persistent flag values shared by every command.
type globalFlags struct {
	since    string
	project  string
	json     bool
	noIngest bool
	currency string
	db       string
	claude   bool // scope to Claude Code sessions
	codex    bool // scope to Codex sessions
}

// source turns the --claude/--codex pair into a filter value. Both or
// neither means every source.
func (f *globalFlags) source() model.Source {
	switch {
	case f.claude && !f.codex:
		return model.SourceClaudeCode
	case f.codex && !f.claude:
		return model.SourceCodex
	}
	return ""
}

// appContext is the state most commands need: an open store, the resolved
// price table and plan, and the reporting window/filter derived from the
// persistent flags.
type appContext struct {
	cfg    config.Config
	st     *store.Store
	prices *pricing.Table

	plan    config.Plan
	planWhy string

	sinceFlag string
	window    ledger.Window
	filter    store.Filter

	haveIngest   bool
	ingestResult ingest.Result
}

// openApp loads the config, opens the store, runs ingest unless disabled,
// and resolves the plan and reporting window. Callers must call ctx.close()
// when done.
func openApp(flags *globalFlags) (*appContext, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	if flags.db != "" {
		cfg.DBPath = flags.db
	}

	prices := pricing.Default()
	for id, r := range cfg.Prices {
		prices.Set(id, pricing.Rate{
			Input:        r.Input,
			CacheRead:    r.CacheRead,
			CacheWrite5m: r.CacheWrite5m,
			CacheWrite1h: r.CacheWrite1h,
			Output:       r.Output,
		})
	}

	if err := os.MkdirAll(filepath.Dir(cfg.DBPath), 0o755); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		return nil, err
	}

	ctx := &appContext{cfg: cfg, st: st, prices: prices}

	if !flags.noIngest {
		res, err := ingest.SyncSource(cfg, st, flags.source())
		if err != nil {
			st.Close()
			return nil, err
		}
		ctx.ingestResult = res
		ctx.haveIngest = true
	}

	plan, why := cfg.Resolve()
	switch flags.currency {
	case "":
		// keep the resolved plan
	case "usd":
		plan, why = config.PlanAPI, "set by --currency usd"
	case "share":
		plan, why = config.PlanSubscription, "set by --currency share"
	default:
		st.Close()
		return nil, usageErrorf("--currency must be \"usd\" or \"share\", got %q", flags.currency)
	}
	ctx.plan = plan
	ctx.planWhy = why

	since := flags.since
	if since == "" {
		since = cfg.DefaultSince
	}
	now := time.Now()
	window, err := ledger.ParseSince(since, now)
	if err != nil {
		st.Close()
		return nil, usageError{err}
	}
	if since == "all" {
		if earliest := ledger.AllSince(st); !earliest.IsZero() {
			window.Days = now.Sub(earliest).Hours() / 24
			window.Label = earliest.Format("Jan 2") + " – " + now.Format("Jan 2")
		}
	}
	ctx.sinceFlag = since
	ctx.window = window
	ctx.filter = store.Filter{Since: window.Since, Until: window.Until, Project: flags.project, Source: flags.source()}

	return ctx, nil
}

func (a *appContext) close() {
	a.st.Close()
}
