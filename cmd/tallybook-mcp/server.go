package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/magna-nz/tallybook/internal/agentfile"
	"github.com/magna-nz/tallybook/internal/config"
	"github.com/magna-nz/tallybook/internal/findings"
	"github.com/magna-nz/tallybook/internal/ingest"
	"github.com/magna-nz/tallybook/internal/ledger"
	"github.com/magna-nz/tallybook/internal/model"
	"github.com/magna-nz/tallybook/internal/pricing"
	"github.com/magna-nz/tallybook/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// refreshAfter is how stale the last transcript scan may be before a tool
// call triggers a new one. Scanning walks every transcript on disk, which
// takes about 1.5s per 200 sessions, so it is not done per call.
const refreshAfter = 60 * time.Second

// currencyUSD and currencyEquivalent are the two values of the "currency"
// field every money-bearing tool returns. On an API plan the dollar figures
// are what was charged. On a subscription they are what the same usage
// would have cost at API list price; the subscriber paid a flat fee.
const (
	currencyUSD        = "usd"
	currencyEquivalent = "list_price_equivalent"
)

// app is the long-lived state behind every tool: an open store, the price
// table with the user's overrides applied, the resolved plan, and the time
// of the last transcript scan. One mutex serialises tool calls, because the
// MCP SDK may dispatch them concurrently and the store is a single SQLite
// connection.
//
// It mirrors openApp in cmd/tallybook/common.go step for step. That file is
// package main in another binary, so it cannot be imported; the ordering is
// copied so the two surfaces report the same numbers for the same window.
// The one deliberate difference: the CLI runs the whole sequence per
// invocation, while this process lives for a client session, so config,
// prices and plan are re-read on every scan rather than once at startup.
type app struct {
	mu sync.Mutex

	cfg    config.Config
	st     *store.Store
	prices *pricing.Table

	plan    config.Plan
	planWhy string

	// lastAttempt is when a scan last started, successful or not, and is
	// what the throttle keys on. lastIngest is when one last succeeded.
	lastAttempt time.Time
	lastIngest  time.Time
	lastError   string
	// results holds the last successful scan's counts per source, so a
	// source-filtered answer reports only that source's skipped files.
	results map[model.Source]ingest.Result
	now     func() time.Time
}

// newApp loads the config, applies price overrides, opens the store and
// resolves the plan. It does not scan transcripts: the first tool call, or
// warm, does that, so the server can answer initialize immediately. The
// caller must call close.
func newApp() (*app, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}

	if err := os.MkdirAll(filepath.Dir(cfg.DBPath), 0o755); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		return nil, err
	}

	a := &app{st: st, now: time.Now, results: map[model.Source]ingest.Result{}}
	a.apply(cfg)
	return a, nil
}

func (a *app) close() {
	a.st.Close()
}

// apply installs a freshly loaded config: the price table with overrides,
// in the same order openApp applies them, and the resolved plan. The
// database path is fixed at open time; a later change to it is ignored.
func (a *app) apply(cfg config.Config) {
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
	a.cfg = cfg
	a.prices = prices
	a.plan, a.planWhy = cfg.Resolve()
}

// ingest re-reads the config and plan, then scans every transcript root
// for new or changed sessions, one source at a time so the counts stay
// separable. A failed config reload keeps the previous config. The caller
// must hold a.mu.
func (a *app) ingest() error {
	a.lastAttempt = a.now()

	if cfg, err := config.Load(); err != nil {
		log.Printf("config: %v (keeping the previous config)", err)
	} else {
		a.apply(cfg)
	}

	results := map[model.Source]ingest.Result{}
	for _, src := range []model.Source{model.SourceClaudeCode, model.SourceCodex} {
		res, err := ingest.SyncSource(a.cfg, a.st, src)
		if err != nil {
			a.lastError = err.Error()
			log.Printf("ingest: %v", err)
			return err
		}
		for _, e := range res.Errors {
			log.Printf("ingest: %s", e)
		}
		results[src] = res
	}
	a.results = results
	a.lastError = ""
	a.lastIngest = a.now()
	return nil
}

// warm runs the first scan. main starts it in the background so the
// handshake is not held up by a large corpus; a tool call that arrives
// first simply does the scan itself.
func (a *app) warm() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.ensureFresh()
}

// ensureFresh re-scans when the last attempt is older than refreshAfter. A
// failed re-scan is recorded in lastError and the previous data served, so
// one unreadable directory cannot take every tool down, and the throttle
// still applies so it is not retried on every call. The caller must hold
// a.mu.
func (a *app) ensureFresh() {
	if !a.lastAttempt.IsZero() && a.now().Sub(a.lastAttempt) < refreshAfter {
		return
	}
	_ = a.ingest() // already logged and recorded
}

// ingestResult sums the last successful scan across sources, or, when src
// is set, returns that source's counts alone.
func (a *app) ingestResult(src model.Source) ingest.Result {
	if src != "" {
		return a.results[src]
	}
	var sum ingest.Result
	for _, r := range a.results {
		sum.Scanned += r.Scanned
		sum.Ingested += r.Ingested
		sum.Unchanged += r.Unchanged
		sum.Failed += r.Failed
		sum.Errors = append(sum.Errors, r.Errors...)
		sum.Elapsed += r.Elapsed
	}
	return sum
}

// scope is the reporting window and filter one tool call works over,
// derived from the common inputs the same way openApp derives them from
// flags.
type scope struct {
	sinceFlag string
	window    ledger.Window
	filter    store.Filter
	plan      config.Plan
	planWhy   string
}

// newScope validates the common inputs and builds the window, filter and
// plan for one call. An empty since uses the config default (30d). currency
// overrides the plan the same way --currency does on the CLI.
func (a *app) newScope(in Scope) (scope, error) {
	src, err := parseSource(in.Source)
	if err != nil {
		return scope{}, err
	}

	plan, why := a.plan, a.planWhy
	switch in.Currency {
	case "":
		// keep the resolved plan
	case "usd":
		plan, why = config.PlanAPI, "set by the currency input"
	case "share":
		plan, why = config.PlanSubscription, "set by the currency input"
	default:
		return scope{}, fmt.Errorf("currency must be \"usd\" or \"share\", got %q", in.Currency)
	}

	since := in.Since
	if since == "" {
		since = a.cfg.DefaultSince
	}
	now := a.now()
	window, err := ledger.ParseSince(since, now)
	if err != nil {
		return scope{}, err
	}
	if since == "all" {
		if earliest := ledger.AllSince(a.st); !earliest.IsZero() {
			window.Days = now.Sub(earliest).Hours() / 24
			window.Label = earliest.Format("Jan 2") + " – " + now.Format("Jan 2")
		}
	}

	return scope{
		sinceFlag: since,
		window:    window,
		filter:    store.Filter{Since: window.Since, Until: window.Until, Project: in.Project, Source: src},
		plan:      plan,
		planWhy:   why,
	}, nil
}

// parseSource turns the optional source input into a filter value.
func parseSource(s string) (model.Source, error) {
	switch s {
	case "":
		return "", nil
	case string(model.SourceClaudeCode):
		return model.SourceClaudeCode, nil
	case string(model.SourceCodex):
		return model.SourceCodex, nil
	}
	return "", fmt.Errorf("source must be %q or %q, got %q", model.SourceClaudeCode, model.SourceCodex, s)
}

// sessionsAndTotals runs the ledger queries in the one order that keeps
// them consistent, exactly as the CLI does.
func (a *app) sessionsAndTotals(sc scope) ([]ledger.SessionCost, ledger.Totals, error) {
	sessions, err := ledger.Sessions(a.st, a.prices, sc.filter)
	if err != nil {
		return nil, ledger.Totals{}, err
	}
	totals, err := ledger.Total(sessions, a.st, a.prices)
	if err != nil {
		return nil, ledger.Totals{}, err
	}
	return sessions, totals, nil
}

// findingsFor runs every enabled rule over the scope's window.
func (a *app) findingsFor(sc scope, totals ledger.Totals) ([]findings.Finding, error) {
	in := findings.Input{
		Store:      a.st,
		Prices:     a.prices,
		Agents:     agentfile.Load(a.projectRoots(sc)...),
		Cfg:        a.cfg.Findings,
		Plan:       sc.plan,
		Filter:     sc.filter,
		Now:        a.now(),
		TotalUSD:   totals.USD,
		WindowDays: sc.window.Days,
	}
	return findings.Run(in)
}

// projectRoots is every project directory seen in the window, so advice can
// be checked against those projects' own agent files.
func (a *app) projectRoots(sc scope) []string {
	rows, err := a.st.Sessions(store.Filter{Since: sc.filter.Since, Until: sc.filter.Until, Project: sc.filter.Project})
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var roots []string
	for _, r := range rows {
		if r.Project == "" || seen[r.Project] {
			continue
		}
		seen[r.Project] = true
		roots = append(roots, r.Project)
	}
	return roots
}

// resolveSessionID finds the session with an exact id match, or the unique
// session whose id starts with idOrPrefix. Like the CLI, the prefix path
// lists every session; see cmd/tallybook/session.go.
func (a *app) resolveSessionID(idOrPrefix string) (string, error) {
	if idOrPrefix == "" {
		return "", errors.New("id is required")
	}
	if s, err := a.st.Session(idOrPrefix); err != nil {
		return "", err
	} else if s != nil {
		return s.ID, nil
	}

	rows, err := a.st.Sessions(store.Filter{})
	if err != nil {
		return "", err
	}
	var matches []string
	for _, r := range rows {
		if strings.HasPrefix(r.ID, idOrPrefix) {
			matches = append(matches, r.ID)
		}
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("no session matches %q", idOrPrefix)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("%q is ambiguous, matches: %s", idOrPrefix, strings.Join(matches, ", "))
	}
}

// Money is the block every tool that returns dollar figures carries, so a
// caller can never mistake a list-price equivalent for a bill.
type Money struct {
	Plan       string `json:"plan" jsonschema:"\"api\" or \"subscription\", re-detected on every scan"`
	PlanReason string `json:"plan_reason" jsonschema:"why that plan was chosen"`
	Currency   string `json:"currency" jsonschema:"\"usd\" when the figures were charged, \"list_price_equivalent\" when a subscription covered them and the figures are what the usage would have cost on the API"`
}

func money(sc scope) Money {
	m := Money{Plan: string(sc.plan), PlanReason: sc.planWhy, Currency: currencyUSD}
	if sc.plan == config.PlanSubscription {
		m.Currency = currencyEquivalent
	}
	return m
}

// moneyNote is the sentence the text content opens with, so a model
// reading the prose sees the plan caveat before any number. The two
// sentences share no distinctive phrase, so a test can tell them apart.
func moneyNote(sc scope) string {
	if sc.plan == config.PlanSubscription {
		return "Subscription plan: every dollar figure below is what this usage would have cost at API list price, not what was billed."
	}
	return "API plan: dollar figures are what the usage cost as charged."
}

// Freshness says when the data behind an answer was last scanned from disk.
type Freshness struct {
	IngestedAt string `json:"ingested_at" jsonschema:"RFC 3339 time of the last successful transcript scan; empty if none has succeeded yet"`
	AgeSeconds int64  `json:"age_seconds" jsonschema:"seconds since that scan; the server rescans when a tool is called more than 60 seconds after the last attempt, or on refresh"`
	ScanError  string `json:"scan_error,omitempty" jsonschema:"why the most recent scan attempt failed, in which case the figures are from the last successful scan"`
}

func (a *app) freshness() Freshness {
	f := Freshness{ScanError: a.lastError}
	if !a.lastIngest.IsZero() {
		f.IngestedAt = a.lastIngest.Format(time.RFC3339)
		f.AgeSeconds = int64(a.now().Sub(a.lastIngest).Seconds())
	}
	return f
}

// freshnessNote is the sentence appended to the text content when the
// last scan attempt failed, so the staleness is visible in the prose too.
func (a *app) freshnessNote() string {
	if a.lastError == "" {
		return ""
	}
	return "Warning: the most recent transcript scan failed (" + a.lastError + "); figures are from the last successful scan.\n"
}

// fmtUSD formats a dollar amount for the text content.
func fmtUSD(v float64) string {
	if v < 0.01 && v > 0 {
		return fmt.Sprintf("$%.4f", v)
	}
	return fmt.Sprintf("$%.2f", v)
}

// textResult wraps human-readable text as a tool result. The structured
// value is passed back separately by the typed handler.
func textResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}
}

// newServer builds the MCP server with every tool registered against a.
func newServer(a *app, version string) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "tallybook", Version: version}, nil)
	registerTools(s, a)
	return s
}
