// Package query is the application layer both of tallybook's long-lived
// servers are built on: tallybook-mcp, which speaks the Model Context
// Protocol on stdio, and tallybook --serve, which speaks HTTP. Each binary
// owns its transport — locking, schemas, routing, content types — and this
// package owns everything behind it, so the two cannot drift into reporting
// different numbers for the same window.
//
// App mirrors openApp in cmd/tallybook/common.go step for step. That file is
// package main in another binary, so it cannot be imported; the ordering is
// copied so the CLI and the servers report the same numbers for the same
// window. The one deliberate difference: the CLI runs the whole sequence per
// invocation, while a server lives for a client session, so config, prices
// and plan are re-read on every scan rather than once at startup.
//
// Nothing here locks. A server holds App's mutex around a call, because the
// store is a single SQLite connection and a transport may dispatch calls
// concurrently.
package query

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
)

// ErrBadInput, ErrNotFound and ErrAmbiguous classify the errors a query
// returns for the caller's own input, so a transport can choose a status code
// with errors.Is instead of reading the message. Any other error is a fault
// on this side. The message itself is unchanged: it is what the CLI prints
// and what an MCP client shows.
var (
	ErrBadInput  = errors.New("bad input")
	ErrNotFound  = errors.New("not found")
	ErrAmbiguous = errors.New("ambiguous")
)

// inputError pairs one of the sentinels above with the message the person
// should read. errors.Is matches the sentinel; Error prints the message.
type inputError struct {
	kind error
	err  error
}

func (e inputError) Error() string        { return e.err.Error() }
func (e inputError) Unwrap() error        { return e.err }
func (e inputError) Is(target error) bool { return target == e.kind }

func badInput(format string, a ...any) error {
	return inputError{ErrBadInput, fmt.Errorf(format, a...)}
}

func notFound(format string, a ...any) error {
	return inputError{ErrNotFound, fmt.Errorf(format, a...)}
}

func ambiguous(format string, a ...any) error {
	return inputError{ErrAmbiguous, fmt.Errorf(format, a...)}
}

// RefreshAfter is how stale the last transcript scan may be before a call
// triggers a new one. Scanning walks every transcript on disk, which takes
// about 1.5s per 200 sessions, so it is not done per call.
const RefreshAfter = 60 * time.Second

// CurrencyUSD and CurrencyEquivalent are the two values of the "currency"
// field every money-bearing answer carries. On an API plan the dollar
// figures are what was charged. On a subscription they are what the same
// usage would have cost at API list price; the subscriber paid a flat fee.
const (
	CurrencyUSD        = "usd"
	CurrencyEquivalent = "list_price_equivalent"
)

// App is the long-lived state behind every query: an open store, the price
// table with the user's overrides applied, the resolved plan, and the time
// of the last transcript scan. Its embedded mutex is the one the server
// holds around a call; App itself never locks, so a server is free to do the
// freshness check and the query under a single acquisition.
type App struct {
	sync.Mutex

	cfg    config.Config
	st     *store.Store
	prices *pricing.Table

	plan    config.Plan
	planWhy string

	// dbPath is the database that was opened. The config is re-read on every
	// scan, and a db_path edited in the meantime, or an override given at
	// open time, would otherwise leave Config reporting a file this process
	// has never opened.
	dbPath string

	// LastAttemptAt is when a scan last started, successful or not, and is
	// what the throttle keys on. LastIngestAt is when one last succeeded.
	// Both are exported so a test can drive the throttle with a fake clock.
	LastAttemptAt time.Time
	LastIngestAt  time.Time
	lastError     string
	// results holds the last successful scan's counts per source, so a
	// source-filtered answer reports only that source's skipped files.
	results map[model.Source]ingest.Result
	// Now is the clock every window, freshness figure and finding is dated
	// against. Tests replace it; nothing else should.
	Now func() time.Time
}

// New loads the config, applies price overrides, opens the store and
// resolves the plan. It does not scan transcripts: the first query, or
// Warm, does that, so a server can answer its handshake immediately. The
// caller must call Close.
func New() (*App, error) { return NewWithDBPath("") }

// NewWithDBPath is New against a database the caller names, the way the CLI's
// --db flag names one. An empty path uses the configured location.
func NewWithDBPath(dbPath string) (*App, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	if dbPath != "" {
		cfg.DBPath = dbPath
	}

	if err := os.MkdirAll(filepath.Dir(cfg.DBPath), 0o755); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		return nil, err
	}

	a := &App{st: st, dbPath: cfg.DBPath, Now: time.Now, results: map[model.Source]ingest.Result{}}
	a.apply(cfg)
	return a, nil
}

// Close releases the database handle. It takes the lock, so a scan or query
// still running on another goroutine finishes first instead of finding the
// connection gone under it. An App is not usable afterwards.
func (a *App) Close() {
	a.Lock()
	defer a.Unlock()
	a.st.Close()
}

// Config is the configuration the last successful load produced, so a caller
// can report what the answers were computed under.
func (a *App) Config() config.Config { return a.cfg }

// Store is the open database. It is exposed so a server can run a query this
// package does not wrap; the caller must hold the lock, as it would for any
// other call.
func (a *App) Store() *store.Store { return a.st }

// PriceTable is the price table with the user's overrides already applied.
// It is not called Prices because that name belongs to the query that
// returns the table as an answer, which is the one callers ask for by name.
func (a *App) PriceTable() *pricing.Table { return a.prices }

// Plan is the resolved plan and the reason it was chosen, both re-derived on
// every scan because logging in or out changes the answer.
func (a *App) Plan() (config.Plan, string) { return a.plan, a.planWhy }

// LastIngest is when a transcript scan last succeeded, or the zero time when
// none has.
func (a *App) LastIngest() time.Time { return a.LastIngestAt }

// LastError is why the most recent scan attempt failed, empty when it
// succeeded. Figures are still served while it is set: they are the last
// successful scan's.
func (a *App) LastError() string { return a.lastError }

// SetLastError records a scan failure without performing one. It exists so a
// test can exercise the stale-data path without an unreadable directory on
// the machine running it.
func (a *App) SetLastError(msg string) { a.lastError = msg }

// apply installs a freshly loaded config: the price table with overrides,
// in the same order openApp applies them, and the resolved plan. The
// database path is fixed at open time; a later change to it is ignored, and
// the path actually open is put back so Config never reports another file.
func (a *App) apply(cfg config.Config) {
	cfg.DBPath = a.dbPath
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

// Ingest re-reads the config and plan, then scans every transcript root
// for new or changed sessions, one source at a time so the counts stay
// separable. A failed config reload keeps the previous config. The caller
// must hold the lock.
func (a *App) Ingest() error {
	a.LastAttemptAt = a.Now()

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
	a.LastIngestAt = a.Now()
	return nil
}

// Warm runs the first scan. A server starts it in the background so the
// handshake is not held up by a large corpus; a call that arrives first
// simply does the scan itself. Unlike the rest of this package it takes the
// lock, because nothing is holding it on the goroutine Warm runs on.
func (a *App) Warm() {
	a.Lock()
	defer a.Unlock()
	a.EnsureFresh()
}

// EnsureFresh re-scans when the last attempt is older than RefreshAfter. A
// failed re-scan is recorded in LastError and the previous data served, so
// one unreadable directory cannot take every query down, and the throttle
// still applies so it is not retried on every call. The caller must hold the
// lock.
func (a *App) EnsureFresh() {
	if !a.LastAttemptAt.IsZero() && a.Now().Sub(a.LastAttemptAt) < RefreshAfter {
		return
	}
	_ = a.Ingest() // already logged and recorded
}

// IngestResult sums the last successful scan across sources, or, when src
// is set, returns that source's counts alone.
func (a *App) IngestResult(src model.Source) ingest.Result {
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

// scope is the reporting window and filter one call works over, derived from
// the common inputs the same way openApp derives them from flags.
type scope struct {
	sinceFlag string
	window    ledger.Window
	filter    store.Filter
	plan      config.Plan
	planWhy   string
}

// Filter is the store filter this scope resolved to. The struct stays
// private — nothing outside this package should be able to assemble a scope
// that the inputs could not produce — but a caller that wants to re-derive
// an answer from the ledger needs the same filter the answer used.
func (s scope) Filter() store.Filter { return s.filter }

// NewScope validates the common inputs and builds the window, filter and
// plan for one call. An empty since uses the config default (30d). currency
// overrides the plan the same way --currency does on the CLI.
func (a *App) NewScope(in Scope) (scope, error) {
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
		return scope{}, badInput("currency must be \"usd\" or \"share\", got %q", in.Currency)
	}

	since := in.Since
	if since == "" {
		since = a.cfg.DefaultSince
	}
	now := a.Now()
	window, err := ledger.ParseSince(since, now)
	if err != nil {
		return scope{}, inputError{ErrBadInput, err} // the person typed the window
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
	return "", badInput("source must be %q or %q, got %q", model.SourceClaudeCode, model.SourceCodex, s)
}

// sessionsAndTotals runs the ledger queries in the one order that keeps
// them consistent, exactly as the CLI does.
func (a *App) sessionsAndTotals(sc scope) ([]ledger.SessionCost, ledger.Totals, error) {
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
func (a *App) findingsFor(sc scope, totals ledger.Totals) ([]findings.Finding, error) {
	in := findings.Input{
		Store:      a.st,
		Prices:     a.prices,
		Agents:     agentfile.Load(a.projectRoots(sc)...),
		Cfg:        a.cfg.Findings,
		Plan:       sc.plan,
		Filter:     sc.filter,
		Now:        a.Now(),
		TotalUSD:   totals.USD,
		WindowDays: sc.window.Days,
	}
	return findings.Run(in)
}

// projectRoots is every project directory seen in the window, so advice can
// be checked against those projects' own agent files.
func (a *App) projectRoots(sc scope) []string {
	rows, err := a.st.Sessions(store.Filter{Since: sc.filter.Since, Until: sc.filter.Until, Project: sc.filter.Project})
	if err != nil {
		return nil
	}
	return DistinctProjects(rows)
}

// resolveSessionID is ResolveSessionID against this App's store.
func (a *App) resolveSessionID(idOrPrefix string) (string, error) {
	return ResolveSessionID(a.st, idOrPrefix)
}

// ResolveSessionID finds the session with an exact id match, or the unique
// session whose id starts with idOrPrefix. It is shared by the CLI, the MCP
// server and the web UI so `tallybook session 81fd6a4a` and a click on the
// page name the same session. An empty id is ErrBadInput, no match is
// ErrNotFound, and several matches are ErrAmbiguous with every candidate
// listed in the message.
func ResolveSessionID(st *store.Store, idOrPrefix string) (string, error) {
	if idOrPrefix == "" {
		return "", badInput("id is required")
	}
	if s, err := st.Session(idOrPrefix); err != nil {
		return "", err
	} else if s != nil {
		return s.ID, nil
	}

	rows, err := st.Sessions(store.Filter{})
	if err != nil {
		return "", err
	}
	// A sub-agent's id is its parent's id with "/agent-<id>" appended, so a
	// prefix of the parent matches every run it launched too. Main sessions
	// win: a person typing the first eight characters of a session id means
	// that session, and a sub-agent run is reached by its own longer prefix.
	var mains, subs []string
	for _, r := range rows {
		if !strings.HasPrefix(r.ID, idOrPrefix) {
			continue
		}
		if r.ParentSessionID == "" {
			mains = append(mains, r.ID)
		} else {
			subs = append(subs, r.ID)
		}
	}
	matches := mains
	if len(matches) == 0 {
		matches = subs
	}
	switch len(matches) {
	case 0:
		return "", notFound("no session matches %q", idOrPrefix)
	case 1:
		return matches[0], nil
	default:
		return "", ambiguous("%q is ambiguous, matches: %s", idOrPrefix, strings.Join(matches, ", "))
	}
}

// DistinctProjects is every project directory among rows, in first-seen
// order, with the empty project skipped. The CLI, the findings rules and the
// web page's project picker all want this list.
func DistinctProjects(rows []store.SessionRow) []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range rows {
		if r.Project == "" || seen[r.Project] {
			continue
		}
		seen[r.Project] = true
		out = append(out, r.Project)
	}
	return out
}

// Money is the block every answer that carries dollar figures opens with, so
// a caller can never mistake a list-price equivalent for a bill.
type Money struct {
	Plan       string `json:"plan" jsonschema:"\"api\" or \"subscription\", re-detected on every scan"`
	PlanReason string `json:"plan_reason" jsonschema:"why that plan was chosen"`
	Currency   string `json:"currency" jsonschema:"\"usd\" when the figures were charged, \"list_price_equivalent\" when a subscription covered them and the figures are what the usage would have cost on the API"`
}

func money(sc scope) Money {
	m := Money{Plan: string(sc.plan), PlanReason: sc.planWhy, Currency: CurrencyUSD}
	if sc.plan == config.PlanSubscription {
		m.Currency = CurrencyEquivalent
	}
	return m
}

// MoneyNote is the sentence the text content opens with, so a model reading
// the prose sees the plan caveat before any number. The two sentences share
// no distinctive phrase, so a test can tell them apart.
func MoneyNote(sc scope) string {
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

// Freshness is the block every answer carries about the data behind it.
func (a *App) Freshness() Freshness {
	f := Freshness{ScanError: a.lastError}
	if !a.LastIngestAt.IsZero() {
		f.IngestedAt = a.LastIngestAt.Format(time.RFC3339)
		f.AgeSeconds = int64(a.Now().Sub(a.LastIngestAt).Seconds())
	}
	return f
}

// FreshnessNote is the sentence prepended to the text content when the last
// scan attempt failed, so the staleness is visible in the prose too.
func (a *App) FreshnessNote() string {
	if a.lastError == "" {
		return ""
	}
	return "Warning: the most recent transcript scan failed (" + a.lastError + "); figures are from the last successful scan.\n"
}

// FmtUSD formats a dollar amount for the text content.
func FmtUSD(v float64) string {
	if v < 0.01 && v > 0 {
		return fmt.Sprintf("$%.4f", v)
	}
	return fmt.Sprintf("$%.2f", v)
}
