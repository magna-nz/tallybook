package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/magna-nz/tallybook/internal/config"
	"github.com/magna-nz/tallybook/internal/query"
	"github.com/magna-nz/tallybook/internal/store"
	"github.com/magna-nz/tallybook/internal/transcript/claude"
	"github.com/magna-nz/tallybook/internal/transcript/codex"
	"github.com/spf13/cobra"
)

// defaultServePort is the port --serve uses unless told otherwise. It is in
// the IANA dynamic range and spells "tally" on a phone keypad closely enough
// to be memorable.
const defaultServePort = 7477

// requestHeader is the header a POST must carry. A page on another origin can
// send a form POST to this server but cannot set a custom header without a
// successful CORS preflight, which nothing here answers, so requiring one
// keeps a foreign page from triggering a scan or a settings write.
const requestHeader = "X-Tallybook-Request"

// indexHTML is the whole frontend: one file, no build step, no CDN. It is
// embedded so a released binary serves the page it was tested against.
//
//go:embed web/index.html
var indexHTML []byte

// runServe answers the same queries the MCP server does, over HTTP on
// loopback, and serves the page that consumes them. The reporting flags are
// deliberately ignored: the page has its own controls for the window, the
// project, the source and the currency, and a flag that disagreed with the
// control would be a second source of truth.
func runServe(cmd *cobra.Command, flags *globalFlags) error {
	a, err := query.NewWithDBPath(flags.db)
	if err != nil {
		return err
	}
	defer a.Close()

	// Scanning a large corpus takes a second or two; the page should render
	// before it finishes, so the first scan runs alongside the listener. The
	// deferred wait runs before Close, so an interrupt during that scan lets
	// it finish rather than closing the database under it. With --no-ingest
	// nothing scans until the page's Rescan button asks.
	warmed := make(chan struct{})
	if flags.noIngest {
		close(warmed)
	} else {
		go func() {
			a.Warm()
			close(warmed)
		}()
	}
	defer func() { <-warmed }()

	// Literally loopback. Nothing about this server is authenticated, so the
	// address is not configurable: anything reachable from another machine
	// would be a ledger anyone on the network could read.
	ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(flags.port))
	if err != nil {
		if flags.port != 0 {
			return fmt.Errorf("cannot listen on 127.0.0.1:%d (%v); try --port with another number", flags.port, err)
		}
		return fmt.Errorf("cannot listen on 127.0.0.1: %w", err)
	}
	url := "http://127.0.0.1:" + strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)

	srv := &http.Server{
		Handler:           newWebHandler(a, version, url, !flags.noIngest),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()

	fmt.Fprintf(cmd.OutOrStdout(), "Serving tallybook at %s (press Ctrl-C to stop)\n", url)
	if flags.open {
		if err := openBrowser(url); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "could not open a browser (%v); go to %s\n", err, url)
		}
	}

	select {
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdown)
	}
}

// openBrowser asks the desktop to open url. Failing is not fatal: the address
// has already been printed, and a headless machine has nothing to open it
// with.
func openBrowser(url string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	default:
		return exec.Command("xdg-open", url).Start()
	}
}

// webServer is the HTTP transport over query.App. It owns routing, status
// codes and content types; every figure it returns comes from the query
// package, so the page and the MCP server cannot report different numbers for
// the same window.
type webServer struct {
	app     *query.App
	version string
	listen  string
	// autoRefresh is whether a read may trigger a transcript scan when the
	// last one is older than query.RefreshAfter, as the MCP tools do. It is
	// false under --no-ingest, when only an explicit Rescan may scan.
	autoRefresh bool
}

// newWebHandler builds the handler --serve mounts. listen is the URL the page
// shows for this server; it is display only, nothing routes on it.
func newWebHandler(a *query.App, version, listen string, autoRefresh bool) http.Handler {
	s := &webServer{app: a, version: version, listen: listen, autoRefresh: autoRefresh}
	return http.HandlerFunc(s.serveHTTP)
}

func (s *webServer) serveHTTP(w http.ResponseWriter, r *http.Request) {
	// Figures go stale the moment a transcript is written, and none of this is
	// worth a disk cache; nosniff because a response must never be run as
	// script because a browser guessed at its type.
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// The page has one button that writes. A foreign site could frame this
	// origin and bait a click on it, so no origin may frame it at all.
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Security-Policy", "frame-ancestors 'none'")

	// A page on any website can make the browser send a request here, and the
	// browser will happily resolve an attacker's hostname to 127.0.0.1. The
	// Host header is what survives that trick: it still carries the name the
	// page asked for, so refusing anything but loopback names ends it.
	if !loopbackHost(r.Host) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		writeJSON(w, http.StatusForbidden, errorOut{"this server answers only to localhost"})
		return
	}

	p := r.URL.Path
	if !strings.HasPrefix(p, "/api/") {
		s.serveStatic(w, r)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	switch {
	case p == "/api/report":
		s.get(w, r, true, s.report)
	case strings.HasPrefix(p, "/api/finding/"):
		s.get(w, r, true, s.finding)
	case p == "/api/agents":
		s.get(w, r, true, s.agents)
	case p == "/api/sessions":
		s.get(w, r, true, s.sessions)
	case p == "/api/session":
		s.get(w, r, true, s.session)
	case p == "/api/changes":
		s.get(w, r, true, s.changes)
	case p == "/api/prices":
		s.get(w, r, false, s.prices) // no figure here comes from a scan
	case p == "/api/status":
		s.get(w, r, false, s.status) // says when the last scan was; must not wait for one
	case p == "/api/config":
		s.get(w, r, false, s.config) // reads files, not the ledger
	case p == "/api/refresh":
		s.post(w, r, s.refresh)
	case p == "/api/hook":
		s.post(w, r, s.hook)
	default:
		writeJSON(w, http.StatusNotFound, errorOut{"no such endpoint: " + p})
	}
}

// serveStatic answers the one non-API route. Everything else is 404 rather
// than a directory listing or a fallback to the page, so a typo in a fetch
// shows up as a missing endpoint instead of a parse error.
func (s *webServer) serveStatic(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		writeJSON(w, http.StatusNotFound, errorOut{"no such page: " + r.URL.Path})
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Allow", http.MethodGet)
		writeJSON(w, http.StatusMethodNotAllowed, errorOut{r.Method + " is not allowed on /"})
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(indexHTML)
}

// loopbackHost reports whether the Host header names this machine. A port is
// allowed, and IPv6 arrives bracketed.
func loopbackHost(host string) bool {
	h := host
	if name, _, err := net.SplitHostPort(host); err == nil {
		h = name
	}
	h = strings.TrimSuffix(strings.TrimPrefix(h, "["), "]")
	// Host names are case-insensitive and may carry a trailing dot; an IPv6
	// literal may carry a zone. All of those are still this machine.
	h = strings.ToLower(strings.TrimSuffix(h, "."))
	if h == "localhost" {
		return true
	}
	if i := strings.IndexByte(h, '%'); i >= 0 {
		h = h[:i]
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// get runs a read-only body under the app's lock, the way the MCP call
// wrapper does: one acquisition covers the freshness check and the query, so
// two requests can never interleave on the single SQLite connection. fresh
// mirrors that wrapper's autoRefresh argument.
func (s *webServer) get(w http.ResponseWriter, r *http.Request, fresh bool, body func(*http.Request) (any, error)) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", http.MethodGet)
		writeJSON(w, http.StatusMethodNotAllowed, errorOut{r.Method + " is not allowed on " + r.URL.Path})
		return
	}
	s.app.Lock()
	defer s.app.Unlock()
	if fresh && s.autoRefresh {
		s.app.EnsureFresh()
	}
	out, err := body(r)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// post runs one of the two bodies that write: a scan into tallybook's own
// database, or the hook into Claude Code's settings. Both need the custom
// header, so neither can be triggered by a form on another page.
func (s *webServer) post(w http.ResponseWriter, r *http.Request, body func(*http.Request) (any, error)) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeJSON(w, http.StatusMethodNotAllowed, errorOut{r.Method + " is not allowed on " + r.URL.Path})
		return
	}
	if r.Header.Get(requestHeader) == "" {
		writeJSON(w, http.StatusForbidden, errorOut{"this endpoint needs the " + requestHeader + " header"})
		return
	}
	s.app.Lock()
	defer s.app.Unlock()
	out, err := body(r)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// ---- errors ----

// errorOut is the body of every failed request. The page reads .error and
// shows it verbatim, so the message has to be the one the CLI would print.
type errorOut struct {
	Error string `json:"error"`
}

// apiError carries the status a message deserves for the checks this
// transport makes itself (a parameter it could not parse, a missing id).
// Errors from the query package carry their own classification, which
// writeError reads with errors.Is; anything unclassified is a fault on this
// side and becomes a 500.
type apiError struct {
	status int
	err    error
}

func (e apiError) Error() string { return e.err.Error() }
func (e apiError) Unwrap() error { return e.err }

func badRequest(format string, a ...any) error {
	return apiError{http.StatusBadRequest, fmt.Errorf(format, a...)}
}

func notFound(format string, a ...any) error {
	return apiError{http.StatusNotFound, fmt.Errorf(format, a...)}
}

func writeError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	var ae apiError
	switch {
	case errors.As(err, &ae):
		status = ae.status
	case errors.Is(err, query.ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, query.ErrBadInput), errors.Is(err, query.ErrAmbiguous):
		status = http.StatusBadRequest
	}
	writeJSON(w, status, errorOut{err.Error()})
}

// writeJSON renders first and writes second, so a value that cannot be
// encoded fails before a status line has gone out.
func writeJSON(w http.ResponseWriter, status int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		body, status = []byte(`{"error":"could not encode the response"}`), http.StatusInternalServerError
	}
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// ---- inputs ----

// scopeOf reads the four window inputs, which are the query string spelling
// of the CLI's global options and of the MCP tools' common inputs. The query
// validates them and classifies a bad value as query.ErrBadInput, which
// writeError turns into a 400.
func scopeOf(r *http.Request) query.Scope {
	q := r.URL.Query()
	return query.Scope{
		Since:    q.Get("since"),
		Project:  q.Get("project"),
		Source:   q.Get("source"),
		Currency: q.Get("currency"),
	}
}

// boolParam reads a flag-shaped parameter. Absent is false; present but
// unparseable is the caller's mistake, not a default.
func boolParam(r *http.Request, name string) (bool, error) {
	v := r.URL.Query().Get(name)
	if v == "" {
		return false, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, badRequest("%s must be 1 or 0, got %q", name, v)
	}
	return b, nil
}

// intParam reads an optional whole number, returning nil when it is absent so
// the query layer applies its own default rather than a zero.
func intParam(r *http.Request, name string) (*int, error) {
	v := r.URL.Query().Get(name)
	if v == "" {
		return nil, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return nil, badRequest("%s must be a whole number, got %q", name, v)
	}
	return &n, nil
}

// ---- read routes ----

func (s *webServer) report(r *http.Request) (any, error) {
	compare, err := boolParam(r, "compare")
	if err != nil {
		return nil, err
	}
	_, out, err := s.app.Report(query.ReportIn{Scope: scopeOf(r), Compare: compare})
	return out, err
}

func (s *webServer) finding(r *http.Request) (any, error) {
	id := strings.TrimPrefix(r.URL.Path, "/api/finding/")
	if id == "" {
		return nil, badRequest("a finding id is required; the ids are in the report")
	}
	_, out, err := s.app.Finding(query.FindingIn{Scope: scopeOf(r), ID: id})
	return out, err
}

func (s *webServer) agents(r *http.Request) (any, error) {
	_, out, err := s.app.Agents(query.AgentsIn{Scope: scopeOf(r)})
	return out, err
}

func (s *webServer) sessions(r *http.Request) (any, error) {
	limit, err := intParam(r, "limit")
	if err != nil {
		return nil, err
	}
	_, out, err := s.app.Sessions(query.SessionsIn{Scope: scopeOf(r), Sort: r.URL.Query().Get("sort"), Limit: limit})
	return out, err
}

func (s *webServer) session(r *http.Request) (any, error) {
	q := r.URL.Query()
	id := q.Get("id")
	if id == "" {
		return nil, badRequest("id is required")
	}
	// A sub-agent id contains a slash, which is why this one route takes its
	// id in the query string. Currency is the only other input a single
	// session honours.
	_, out, err := s.app.Session(query.SessionIn{ID: id, Currency: q.Get("currency")})
	return out, err
}

func (s *webServer) changes(r *http.Request) (any, error) {
	minRuns, err := intParam(r, "min_runs")
	if err != nil {
		return nil, err
	}
	undecided, err := boolParam(r, "include_undecided")
	if err != nil {
		return nil, err
	}
	_, out, err := s.app.Changes(query.ChangesIn{Scope: scopeOf(r), MinRuns: minRuns, IncludeUndecided: undecided})
	return out, err
}

func (s *webServer) prices(*http.Request) (any, error) {
	_, out, err := s.app.Prices(query.PricesIn{})
	return out, err
}

// ---- status ----

// lastIngestOut is what the last successful scan did. It is a pointer in
// statusOut because "no scan has finished yet" and "a scan found nothing" are
// different things the page words differently.
type lastIngestOut struct {
	At        string   `json:"at"`
	Scanned   int      `json:"scanned"`
	Ingested  int      `json:"ingested"`
	Unchanged int      `json:"unchanged"`
	Failed    int      `json:"failed"`
	Errors    []string `json:"errors"`
	ElapsedMS int64    `json:"elapsed_ms"`
}

// statusOut is everything the page needs that is not a figure about spend:
// where the data lives, what the last scan did, and the two values the page
// boots with (the default window and the project list for its picker). The
// first two fields are query.Freshness's, spelled out so scan_error below can
// be present even when empty, which is how the page tells "no error" from
// "no scan yet".
type statusOut struct {
	IngestedAt     string         `json:"ingested_at"`
	AgeSeconds     int64          `json:"age_seconds"`
	Version        string         `json:"version"`
	Listen         string         `json:"listen"`
	DBPath         string         `json:"db_path"`
	DBBytes        int64          `json:"db_bytes"`
	ClaudeRoots    []string       `json:"claude_roots"`
	ClaudeSessions int            `json:"claude_sessions"`
	CodexRoots     []string       `json:"codex_roots"`
	CodexSessions  int            `json:"codex_sessions"`
	LastIngest     *lastIngestOut `json:"last_ingest"`
	ScanError      string         `json:"scan_error"`
	Plan           string         `json:"plan"`
	PlanReason     string         `json:"plan_reason"`
	PricesVerified string         `json:"prices_verified"`
	DefaultSince   string         `json:"default_since"`
	Home           string         `json:"home"`
	Projects       []string       `json:"projects"`
}

func (s *webServer) status(*http.Request) (any, error) {
	st := s.app.Store()
	stats, err := st.Stats()
	if err != nil {
		return nil, err
	}
	cfg := s.app.Config()
	plan, planWhy := s.app.Plan()
	rows, err := st.Sessions(store.Filter{})
	if err != nil {
		return nil, err
	}
	claudeCount, codexCount := sourceCounts(rows)
	projects := query.DistinctProjects(rows)
	sort.Strings(projects)
	if projects == nil {
		projects = []string{}
	}
	fresh := s.app.Freshness()

	out := statusOut{
		IngestedAt:     fresh.IngestedAt,
		AgeSeconds:     fresh.AgeSeconds,
		Version:        s.version,
		Listen:         s.listen,
		DBPath:         cfg.DBPath,
		DBBytes:        stats.DBBytes,
		ClaudeRoots:    resolvedRoots(cfg.ClaudeRoots, claudeDefaultRoots),
		ClaudeSessions: claudeCount,
		CodexRoots:     resolvedRoots(cfg.CodexRoots, codex.DefaultRoots),
		CodexSessions:  codexCount,
		ScanError:      s.app.LastError(),
		Plan:           string(plan),
		PlanReason:     planWhy,
		PricesVerified: s.app.PriceTable().Dated(),
		DefaultSince:   cfg.DefaultSince,
		Projects:       projects,
	}
	if home, err := os.UserHomeDir(); err == nil {
		out.Home = home
	}
	if at := s.app.LastIngest(); !at.IsZero() {
		res := s.app.IngestResult("")
		li := lastIngestOut{
			At: at.Format(time.RFC3339), Scanned: res.Scanned, Ingested: res.Ingested,
			Unchanged: res.Unchanged, Failed: res.Failed, Errors: res.Errors,
			ElapsedMS: res.Elapsed.Milliseconds(),
		}
		if li.Errors == nil {
			li.Errors = []string{}
		}
		out.LastIngest = &li
	}
	return out, nil
}

// claudeDefaultRoots adapts claude.DefaultRoot, which returns the one
// directory, to the shape codex.DefaultRoots has.
func claudeDefaultRoots() ([]string, error) {
	root, err := claude.DefaultRoot()
	if err != nil {
		return nil, err
	}
	return []string{root}, nil
}

// resolvedRoots is the directories a source will actually be scanned in: the
// configured ones, or the defaults that stand in for them, as runStatus
// reports them. The result is never nil, so the page can iterate it.
func resolvedRoots(configured []string, fallback func() ([]string, error)) []string {
	if len(configured) > 0 {
		return configured
	}
	if roots, err := fallback(); err == nil && roots != nil {
		return roots
	}
	return []string{}
}

// ---- config ----

type configKeyOut struct {
	Key     string `json:"key"`
	Default string `json:"default"`
	Value   string `json:"value"`
	Source  string `json:"source"`
}

// configEnvOut names one environment variable and says whether it is set.
// There is no value field on purpose: one of these variables is an API key,
// and a page that never receives a value cannot leak one.
type configEnvOut struct {
	Name string `json:"name"`
	Set  bool   `json:"set"`
}

type configHookOut struct {
	Installed    bool   `json:"installed"`
	SettingsPath string `json:"settings_path"`
	Block        string `json:"block"`
	LogPath      string `json:"log_path"`
}

type configOut struct {
	Path   string         `json:"path"`
	Exists bool           `json:"exists"`
	Keys   []configKeyOut `json:"keys"`
	Env    []configEnvOut `json:"env"`
	Hook   configHookOut  `json:"hook"`
}

// envNames is every variable documented as affecting tallybook, in the order
// the documentation lists them.
var envNames = []string{
	"TALLYBOOK_DIR", "TALLYBOOK_DB", "TALLYBOOK_PLAN",
	"TALLYBOOK_CLAUDE_ROOTS", "TALLYBOOK_CODEX_ROOTS",
	"XDG_CONFIG_HOME", "CLAUDE_CONFIG_DIR", "CODEX_HOME", "ANTHROPIC_API_KEY",
}

func (s *webServer) config(*http.Request) (any, error) {
	path := config.Path()
	_, statErr := os.Stat(path)
	out := configOut{
		Path:   path,
		Exists: statErr == nil,
		Keys:   configKeys(s.app.Config(), statErr == nil),
		Env:    make([]configEnvOut, 0, len(envNames)),
	}
	for _, name := range envNames {
		// Presence only, never os.Getenv's result. An empty value counts as
		// unset because that is how config.Load treats it.
		out.Env = append(out.Env, configEnvOut{Name: name, Set: os.Getenv(name) != ""})
	}

	settingsPath, err := claudeSettingsPath()
	if err != nil {
		return nil, err
	}
	out.Hook = configHookOut{
		SettingsPath: settingsPath,
		Block:        hookBlock,
		LogPath:      filepath.Join(config.Dir(), sessionLogName),
	}
	// A settings.json that will not parse reads as "not installed": the page
	// then offers to write it, and that POST returns the parse error rather
	// than this read making the whole config page fail.
	if doc, err := readSettings(settingsPath); err == nil {
		if installed, err := doc.hasTallybookHook(); err == nil {
			out.Hook.Installed = installed
		}
	}
	return out, nil
}

// configKeys is the documented configuration table with this machine's values
// filled in. Each row says where its value came from, which is the question
// people actually have when a setting is not doing what they expect.
func configKeys(cfg config.Config, fileExists bool) []configKeyOut {
	def := config.Default()
	keys := []configKeyOut{}
	add := func(key, defText, value, env string, changed bool) {
		keys = append(keys, configKeyOut{
			Key: key, Default: defText, Value: value,
			Source: keySource(env, changed, fileExists),
		})
	}

	add("plan", "auto", string(cfg.Plan), "TALLYBOOK_PLAN", cfg.Plan != def.Plan)
	add("default_since", "30d", cfg.DefaultSince, "", cfg.DefaultSince != def.DefaultSince)
	add("db_path", "<config dir>/tallybook.db", cfg.DBPath, "TALLYBOOK_DB", cfg.DBPath != def.DBPath)
	add("claude_roots", "~/.claude/projects",
		joinList(resolvedRoots(cfg.ClaudeRoots, claudeDefaultRoots)),
		"TALLYBOOK_CLAUDE_ROOTS", len(cfg.ClaudeRoots) > 0)
	add("codex_roots", "~/.codex/sessions, ~/.codex/archived_sessions",
		joinList(resolvedRoots(cfg.CodexRoots, codex.DefaultRoots)),
		"TALLYBOOK_CODEX_ROOTS", len(cfg.CodexRoots) > 0)

	f, fd := cfg.Findings, def.Findings
	add("findings.disabled", "[]", joinList(f.Disabled), "", len(f.Disabled) > 0)
	add("findings.min_runs", "3", strconv.Itoa(f.MinRuns), "", f.MinRuns != fd.MinRuns)
	add("findings.tool_output_share", "0.5", ftoa(f.ToolOutputShare), "", f.ToolOutputShare != fd.ToolOutputShare)
	add("findings.min_cache_rebuilds", "3", strconv.Itoa(f.MinCacheRebuilds), "", f.MinCacheRebuilds != fd.MinCacheRebuilds)
	add("findings.cache_gap_minutes", "5", strconv.Itoa(f.CacheGapMinutes), "", f.CacheGapMinutes != fd.CacheGapMinutes)
	add("findings.retry_threshold", "3", strconv.Itoa(f.RetryThreshold), "", f.RetryThreshold != fd.RetryThreshold)
	add("findings.thinking_relay_turns", "20", strconv.Itoa(f.ThinkingRelayTurns), "", f.ThinkingRelayTurns != fd.ThinkingRelayTurns)
	add("findings.main_session_turns", "10", strconv.Itoa(f.MainSessionTurns), "", f.MainSessionTurns != fd.MainSessionTurns)
	add("findings.long_context_tokens", "100000", strconv.FormatInt(f.LongContextTokens, 10), "", f.LongContextTokens != fd.LongContextTokens)
	add("findings.repeat_threshold", "3", strconv.Itoa(f.RepeatThreshold), "", f.RepeatThreshold != fd.RepeatThreshold)
	add("findings.min_saving_usd", "1.0", ftoa(f.MinSavingUSD), "", f.MinSavingUSD != fd.MinSavingUSD)
	add("findings.report_limit", "5", strconv.Itoa(f.ReportLimit), "", f.ReportLimit != fd.ReportLimit)

	planPrice := "unset"
	if f.PlanPriceUSD != 0 {
		planPrice = ftoa(f.PlanPriceUSD)
	}
	add("findings.plan_price", "unset", planPrice, "", f.PlanPriceUSD != fd.PlanPriceUSD)

	return keys
}

// keySource attributes a value to the layer that set it, in the precedence
// the documentation states: environment over file over default. A value that
// differs from the default with no file on disk can only have come from a
// flag or a default that depends on the machine, so it stays "default".
func keySource(env string, changed, fileExists bool) string {
	if env != "" && os.Getenv(env) != "" {
		return "env"
	}
	if changed && fileExists {
		return "config.toml"
	}
	return "default"
}

func joinList(v []string) string {
	if len(v) == 0 {
		return "[]"
	}
	return strings.Join(v, ", ")
}

func ftoa(v float64) string { return strconv.FormatFloat(v, 'g', -1, 64) }

// ---- write routes ----

func (s *webServer) refresh(*http.Request) (any, error) {
	_, out, err := s.app.Refresh(query.RefreshIn{})
	return out, err
}

// hookOut reports what the hook POST did. already distinguishes "we wrote it"
// from "it was there", because writing a second copy would run tallybook
// twice per session.
type hookOut struct {
	Installed    bool   `json:"installed"`
	Already      bool   `json:"already"`
	SettingsPath string `json:"settings_path"`
}

// hook adds the SessionEnd entry to Claude Code's settings.json, the same
// sequence `tallybook setup hook --write` runs. It is the only write this
// server makes outside tallybook's own directory.
func (s *webServer) hook(*http.Request) (any, error) {
	path, err := claudeSettingsPath()
	if err != nil {
		return nil, err
	}
	doc, err := readSettings(path)
	if err != nil {
		return nil, err
	}
	already, err := doc.hasTallybookHook()
	if err != nil {
		return nil, err
	}
	if already {
		return hookOut{Installed: true, Already: true, SettingsPath: path}, nil
	}
	if err := doc.addHook(); err != nil {
		return nil, err
	}
	if err := doc.writeAtomically(path); err != nil {
		return nil, err
	}
	return hookOut{Installed: true, SettingsPath: path}, nil
}
