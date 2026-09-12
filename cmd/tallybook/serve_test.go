// Tests for the local web server. Each one runs the real handler over the
// checked-in transcript fixtures, with a temp TALLYBOOK_DIR, a temp database
// and a temp HOME, so nothing here can read or write the user's own
// ~/.claude, ~/.codex or ~/.config.
package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/magna-nz/tallybook/internal/config"
	"github.com/magna-nz/tallybook/internal/query"
)

// serveTest starts the handler on a loopback test server with the fixtures
// already ingested, and returns it with the fake HOME the hook routes write
// into.
func serveTest(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	e2eEnv(t)
	home := setupTestHome(t)

	a, err := query.New()
	if err != nil {
		t.Fatalf("open app: %v", err)
	}
	t.Cleanup(a.Close)
	a.Warm()

	srv := httptest.NewServer(newWebHandler(a, "test", "http://127.0.0.1:7477", true))
	t.Cleanup(srv.Close)
	return srv, home
}

// getJSON does a GET and decodes the body into out when out is not nil. It
// returns the status and the raw body, because several tests assert on the
// shape of the JSON rather than on values.
func getJSON(t *testing.T, srv *httptest.Server, path string, out any) (int, []byte) {
	t.Helper()
	res, err := srv.Client().Get(srv.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if out != nil {
		if err := json.Unmarshal(body, out); err != nil {
			t.Fatalf("GET %s did not return JSON: %v\n%s", path, err, body)
		}
	}
	return res.StatusCode, body
}

// postJSON does a POST, with the request header unless withHeader is false.
func postJSON(t *testing.T, srv *httptest.Server, path string, withHeader bool, out any) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srv.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if withHeader {
		req.Header.Set(requestHeader, "1")
	}
	res, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if out != nil {
		if err := json.Unmarshal(body, out); err != nil {
			t.Fatalf("POST %s did not return JSON: %v\n%s", path, err, body)
		}
	}
	return res.StatusCode, body
}

// errorMessage is the body every failed request carries.
type errorMessage struct {
	Error string `json:"error"`
}

// wantStatus fails when the status is not the expected one, printing the body
// so the reason is in the failure rather than in a second run.
func wantStatus(t *testing.T, got, want int, path string, body []byte) {
	t.Helper()
	if got != want {
		t.Fatalf("%s = %d, want %d\n%s", path, got, want, body)
	}
}

func TestServeIndexIsTheEmbeddedPage(t *testing.T) {
	srv, _ := serveTest(t)

	res, err := srv.Client().Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)

	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET / = %d, want 200", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("content type = %q, want text/html; charset=utf-8", ct)
	}
	if cc := res.Header.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("cache control = %q, want no-store", cc)
	}
	if n := res.Header.Get("X-Content-Type-Options"); n != "nosniff" {
		t.Errorf("x-content-type-options = %q, want nosniff", n)
	}
	if !strings.Contains(string(body), "<title>Tallybook</title>") {
		t.Errorf("GET / did not serve the page (%d bytes)", len(body))
	}
}

func TestServeReport(t *testing.T) {
	srv, _ := serveTest(t)

	var out struct {
		Sessions int    `json:"sessions"`
		Currency string `json:"currency"`
		USD      float64
	}
	status, body := getJSON(t, srv, "/api/report?since=all", &out)
	wantStatus(t, status, http.StatusOK, "/api/report", body)

	if out.Sessions <= 0 {
		t.Errorf("sessions = %d, want more than 0\n%s", out.Sessions, body)
	}
	if out.Currency == "" {
		t.Errorf("report carries no currency field:\n%s", body)
	}
}

func TestServeReportCompareWithAllIsRejected(t *testing.T) {
	srv, _ := serveTest(t)

	var out errorMessage
	status, body := getJSON(t, srv, "/api/report?since=all&compare=1", &out)
	wantStatus(t, status, http.StatusBadRequest, "/api/report?compare=1&since=all", body)
	if out.Error == "" {
		t.Errorf("400 carried no message:\n%s", body)
	}
}

func TestServeBadScopeIsRejected(t *testing.T) {
	srv, _ := serveTest(t)

	for _, path := range []string{
		"/api/report?since=yesterday",
		"/api/report?currency=euros",
		"/api/agents?source=cursor",
		"/api/sessions?sort=project",
	} {
		var out errorMessage
		status, body := getJSON(t, srv, path, &out)
		wantStatus(t, status, http.StatusBadRequest, path, body)
		if out.Error == "" {
			t.Errorf("%s: 400 carried no message", path)
		}
	}
}

func TestServeUnknownFindingIs404(t *testing.T) {
	srv, _ := serveTest(t)

	var out errorMessage
	status, body := getJSON(t, srv, "/api/finding/nope", &out)
	wantStatus(t, status, http.StatusNotFound, "/api/finding/nope", body)
	if !strings.Contains(out.Error, "nope") {
		t.Errorf("404 message does not name the id: %q", out.Error)
	}
}

func TestServeSession(t *testing.T) {
	srv, _ := serveTest(t)

	var out struct {
		ID        string `json:"id"`
		TurnCount int    `json:"turn_count"`
		Turns     []struct {
			Index int `json:"index"`
		} `json:"turns"`
		Subagents []struct {
			ID        string `json:"id"`
			AgentType string `json:"agent_type"`
		} `json:"subagents"`
	}
	status, body := getJSON(t, srv, "/api/session?id=sess-0001", &out)
	wantStatus(t, status, http.StatusOK, "/api/session", body)

	if out.ID != "sess-0001" {
		t.Errorf("id = %q, want sess-0001", out.ID)
	}
	if out.TurnCount != len(out.Turns) {
		t.Errorf("turn_count = %d, len(turns) = %d", out.TurnCount, len(out.Turns))
	}
	if out.Subagents == nil {
		t.Errorf("subagents is null, want an array:\n%s", body)
	}
	if len(out.Subagents) == 0 {
		t.Errorf("sess-0001 launched a sub-agent in the fixtures, got none:\n%s", body)
	}
}

func TestServeUnknownSessionIs404(t *testing.T) {
	srv, _ := serveTest(t)

	status, body := getJSON(t, srv, "/api/session?id=no-such-session", nil)
	wantStatus(t, status, http.StatusNotFound, "/api/session?id=no-such-session", body)
}

func TestServeSessionsLimitZeroReturnsEveryRow(t *testing.T) {
	srv, _ := serveTest(t)

	var out struct {
		Total    int `json:"total"`
		Sessions []struct {
			ID string `json:"id"`
		} `json:"sessions"`
	}
	status, body := getJSON(t, srv, "/api/sessions?since=all&limit=0", &out)
	wantStatus(t, status, http.StatusOK, "/api/sessions", body)

	if out.Total == 0 {
		t.Fatalf("no sessions in the fixtures:\n%s", body)
	}
	if out.Total != len(out.Sessions) {
		t.Errorf("total = %d but %d rows returned with limit=0", out.Total, len(out.Sessions))
	}
}

func TestServeChangesRejectsMinRunsBelowOne(t *testing.T) {
	srv, _ := serveTest(t)

	var out errorMessage
	status, body := getJSON(t, srv, "/api/changes?min_runs=0", &out)
	wantStatus(t, status, http.StatusBadRequest, "/api/changes?min_runs=0", body)
	if !strings.Contains(out.Error, "min_runs") {
		t.Errorf("400 message does not mention min_runs: %q", out.Error)
	}
}

func TestServeStatus(t *testing.T) {
	srv, _ := serveTest(t)

	var raw map[string]json.RawMessage
	status, body := getJSON(t, srv, "/api/status", &raw)
	wantStatus(t, status, http.StatusOK, "/api/status", body)

	// The page's project picker iterates this without a null check.
	projects, ok := raw["projects"]
	if !ok || string(projects) == "null" {
		t.Errorf("projects is missing or null:\n%s", body)
	}
	var list []string
	if err := json.Unmarshal(projects, &list); err != nil {
		t.Errorf("projects is not an array: %v", err)
	}
	if len(list) == 0 {
		t.Errorf("no project paths in the ledger:\n%s", body)
	}

	var out struct {
		DefaultSince string `json:"default_since"`
		Version      string `json:"version"`
		Listen       string `json:"listen"`
		DBPath       string `json:"db_path"`
		ClaudeRoots  []string
		Plan         string `json:"plan"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	if out.DefaultSince == "" {
		t.Errorf("default_since is empty; the page falls back to it on boot:\n%s", body)
	}
	if out.Version != "test" || out.Listen != "http://127.0.0.1:7477" {
		t.Errorf("version/listen = %q/%q, want the values newWebHandler was given", out.Version, out.Listen)
	}
	if out.Plan == "" {
		t.Errorf("plan is empty:\n%s", body)
	}
}

func TestServeConfigTellsNoValuesAndNoHook(t *testing.T) {
	srv, home := serveTest(t)

	var out struct {
		Path string `json:"path"`
		Keys []struct {
			Key    string `json:"key"`
			Value  string `json:"value"`
			Source string `json:"source"`
		} `json:"keys"`
		Env  []map[string]json.RawMessage `json:"env"`
		Hook struct {
			Installed    bool   `json:"installed"`
			SettingsPath string `json:"settings_path"`
			Block        string `json:"block"`
			LogPath      string `json:"log_path"`
		} `json:"hook"`
	}
	status, body := getJSON(t, srv, "/api/config", &out)
	wantStatus(t, status, http.StatusOK, "/api/config", body)

	if len(out.Env) == 0 {
		t.Fatalf("no environment variables listed:\n%s", body)
	}
	for _, e := range out.Env {
		if len(e) != 2 {
			t.Errorf("env entry has %d fields, want only name and set: %v", len(e), e)
		}
		if _, ok := e["name"]; !ok {
			t.Errorf("env entry has no name: %v", e)
		}
		if _, ok := e["set"]; !ok {
			t.Errorf("env entry has no set: %v", e)
		}
	}
	// The fixture roots are passed in the environment, so a leaked value
	// would show up as a path in this document.
	if strings.Contains(string(body), "TALLYBOOK_CLAUDE_ROOTS\":\"") {
		t.Errorf("an environment value was returned:\n%s", body)
	}

	if len(out.Keys) == 0 {
		t.Errorf("no config keys listed:\n%s", body)
	}
	var sawPlan bool
	for _, k := range out.Keys {
		if k.Key == "plan" {
			sawPlan = true
			if k.Source != "env" { // e2eEnv sets TALLYBOOK_PLAN
				t.Errorf("plan source = %q, want env", k.Source)
			}
		}
		switch k.Source {
		case "default", "config.toml", "env":
		default:
			t.Errorf("key %s has source %q", k.Key, k.Source)
		}
	}
	if !sawPlan {
		t.Errorf("the plan key is missing from the config table")
	}

	if out.Hook.Installed {
		t.Errorf("hook reported as installed in a fresh HOME")
	}
	if want := filepath.Join(home, ".claude", "settings.json"); out.Hook.SettingsPath != want {
		t.Errorf("settings_path = %q, want %q", out.Hook.SettingsPath, want)
	}
	if !strings.Contains(out.Hook.Block, "tallybook hook session-end") {
		t.Errorf("hook block is not the one setup hook prints: %q", out.Hook.Block)
	}
	if out.Hook.LogPath == "" {
		t.Errorf("hook log path is empty")
	}
}

func TestServePricesMarkConfigOverrides(t *testing.T) {
	e2eEnv(t)
	setupTestHome(t)

	const overridden = "claude-opus-5"
	toml := "[prices.\"" + overridden + "\"]\ninput = 1\ncache_read = 0.1\ncache_write_5m = 1.25\ncache_write_1h = 2\noutput = 5\n"
	if err := os.WriteFile(config.Path(), []byte(toml), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	a, err := query.New()
	if err != nil {
		t.Fatalf("open app: %v", err)
	}
	t.Cleanup(a.Close)
	srv := httptest.NewServer(newWebHandler(a, "test", "http://127.0.0.1:7477", true))
	t.Cleanup(srv.Close)

	var out struct {
		Models []struct {
			ID       string  `json:"id"`
			Input    float64 `json:"input"`
			Override bool    `json:"override"`
		} `json:"models"`
	}
	status, body := getJSON(t, srv, "/api/prices", &out)
	wantStatus(t, status, http.StatusOK, "/api/prices", body)

	var seen bool
	for _, m := range out.Models {
		if m.ID == overridden {
			seen = true
			if !m.Override {
				t.Errorf("%s is not marked as an override:\n%s", m.ID, body)
			}
			if m.Input != 1 {
				t.Errorf("%s input = %v, want the configured 1", m.ID, m.Input)
			}
			continue
		}
		if m.Override {
			t.Errorf("%s is marked as an override but was not configured", m.ID)
		}
	}
	if !seen {
		t.Errorf("%s is missing from the price table:\n%s", overridden, body)
	}
}

func TestServeRefreshNeedsTheRequestHeader(t *testing.T) {
	srv, _ := serveTest(t)

	status, body := postJSON(t, srv, "/api/refresh", false, nil)
	wantStatus(t, status, http.StatusForbidden, "POST /api/refresh without the header", body)

	var out struct {
		Scanned   int      `json:"scanned"`
		Errors    []string `json:"errors"`
		ElapsedMS int64    `json:"elapsed_ms"`
	}
	status, body = postJSON(t, srv, "/api/refresh", true, &out)
	wantStatus(t, status, http.StatusOK, "POST /api/refresh", body)
	if out.Scanned == 0 {
		t.Errorf("refresh scanned nothing:\n%s", body)
	}
	if out.Errors == nil {
		t.Errorf("errors is null, want an array:\n%s", body)
	}
}

func TestServeHookWritesOnceIntoTheFakeHome(t *testing.T) {
	srv, home := serveTest(t)
	settingsPath := filepath.Join(home, ".claude", "settings.json")

	var first struct {
		Installed    bool   `json:"installed"`
		Already      bool   `json:"already"`
		SettingsPath string `json:"settings_path"`
	}
	status, body := postJSON(t, srv, "/api/hook", true, &first)
	wantStatus(t, status, http.StatusOK, "POST /api/hook", body)
	if !first.Installed || first.Already {
		t.Errorf("first POST = %+v, want installed without already", first)
	}
	if first.SettingsPath != settingsPath {
		t.Errorf("settings_path = %q, want %q", first.SettingsPath, settingsPath)
	}

	written, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read %s: %v", settingsPath, err)
	}
	if !strings.Contains(string(written), "tallybook hook session-end") {
		t.Errorf("settings.json does not hold the hook:\n%s", written)
	}

	var second struct {
		Already bool `json:"already"`
	}
	status, body = postJSON(t, srv, "/api/hook", true, &second)
	wantStatus(t, status, http.StatusOK, "second POST /api/hook", body)
	if !second.Already {
		t.Errorf("second POST = %+v, want already", second)
	}
	again, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != string(written) {
		t.Errorf("the second POST rewrote settings.json")
	}

	var cfg struct {
		Hook struct {
			Installed bool `json:"installed"`
		} `json:"hook"`
	}
	status, body = getJSON(t, srv, "/api/config", &cfg)
	wantStatus(t, status, http.StatusOK, "/api/config", body)
	if !cfg.Hook.Installed {
		t.Errorf("config still reports the hook as not installed:\n%s", body)
	}
}

func TestServeHookRejectsAGet(t *testing.T) {
	srv, _ := serveTest(t)

	status, body := getJSON(t, srv, "/api/hook", nil)
	wantStatus(t, status, http.StatusMethodNotAllowed, "GET /api/hook", body)
}

func TestServeRefusesAForeignHost(t *testing.T) {
	srv, _ := serveTest(t)

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/api/report", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "evil.example"

	res, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("GET with a foreign Host: %v", err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("Host: evil.example = %d, want 403\n%s", res.StatusCode, body)
	}
}

func TestServeUnknownPathIs404(t *testing.T) {
	srv, _ := serveTest(t)

	for _, path := range []string{"/api/nope", "/nope", "/web/index.html"} {
		status, body := getJSON(t, srv, path, nil)
		wantStatus(t, status, http.StatusNotFound, path, body)
	}
}

func TestRootParsesServeFlags(t *testing.T) {
	root := newRoot()
	if err := root.ParseFlags([]string{"--serve", "--port", "0"}); err != nil {
		t.Fatalf("parse --serve --port 0: %v", err)
	}
	serve, err := root.Flags().GetBool("serve")
	if err != nil || !serve {
		t.Errorf("serve = %v (%v), want true", serve, err)
	}
	port, err := root.Flags().GetInt("port")
	if err != nil || port != 0 {
		t.Errorf("port = %v (%v), want 0", port, err)
	}

	fresh := newRoot()
	if err := fresh.ParseFlags(nil); err != nil {
		t.Fatal(err)
	}
	if port, _ := fresh.Flags().GetInt("port"); port != defaultServePort {
		t.Errorf("default port = %d, want %d", port, defaultServePort)
	}
}

// TestServeStopsOnContextCancellation runs the real command on a free port
// with the context already cancelled, so it listens, prints its one line and
// shuts down without waiting for a signal.
func TestServeStopsOnContextCancellation(t *testing.T) {
	e2eEnv(t)
	setupTestHome(t)

	root := newRoot()
	var out strings.Builder
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"--serve", "--port", "0", "--no-ingest"})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan error, 1)
	go func() { done <- root.ExecuteContext(ctx) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("--serve: %v\noutput:\n%s", err, out.String())
		}
	case <-time.After(30 * time.Second):
		t.Fatal("--serve did not stop when its context was cancelled")
	}

	line := out.String()
	if !strings.HasPrefix(line, "Serving tallybook at http://127.0.0.1:") {
		t.Errorf("--serve printed %q", line)
	}
	if !strings.HasSuffix(strings.TrimSpace(line), "(press Ctrl-C to stop)") {
		t.Errorf("--serve printed %q", line)
	}
	if n := strings.Count(strings.TrimSpace(line), "\n"); n != 0 {
		t.Errorf("--serve printed %d lines, want one:\n%s", n+1, line)
	}
}

// TestLoopbackHost pins the Host forms the server accepts. Anything that
// resolves to this machine passes, in any spelling a browser or a curl might
// send; a name that could belong to an attacker's DNS never does.
func TestLoopbackHost(t *testing.T) {
	for _, tc := range []struct {
		host string
		ok   bool
	}{
		{"localhost", true}, {"localhost:7477", true}, {"LOCALHOST:7477", true}, {"localhost.", true},
		{"127.0.0.1", true}, {"127.0.0.1:7477", true}, {"127.1.2.3:80", true},
		{"[::1]", true}, {"[::1]:7477", true}, {"[0:0:0:0:0:0:0:1]:7477", true}, {"[::1%25lo0]:7477", true},
		{"", false}, {"evil.example", false}, {"evil.example:7477", false}, {"localhost.evil.example", false},
		{"127.0.0.1.evil.example", false}, {"[::ffff:127.0.0.1]:7477", true}, {"10.0.0.1:7477", false},
		{"[fe80::1]:7477", false}, {"0.0.0.0:7477", false},
	} {
		if got := loopbackHost(tc.host); got != tc.ok {
			t.Errorf("loopbackHost(%q) = %v, want %v", tc.host, got, tc.ok)
		}
	}
}

// TestServeHookNeedsTheRequestHeader is the hook's half of the CSRF guard;
// the refresh half has its own test.
func TestServeHookNeedsTheRequestHeader(t *testing.T) {
	srv, _ := serveTest(t)
	res, err := http.Post(srv.URL+"/api/hook", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("POST /api/hook without the header: status %d, want 403", res.StatusCode)
	}
}

// TestServeHeadIsAnsweredLikeGet: link checkers and health probes send HEAD.
func TestServeHeadIsAnsweredLikeGet(t *testing.T) {
	srv, _ := serveTest(t)
	for _, path := range []string{"/", "/api/status"} {
		res, err := http.Head(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Errorf("HEAD %s: status %d, want 200", path, res.StatusCode)
		}
		if got := res.Header.Get("X-Frame-Options"); got != "DENY" {
			t.Errorf("HEAD %s: X-Frame-Options = %q, want DENY", path, got)
		}
	}
}

// TestServeNoIngestNeverScansOnARequest: under --no-ingest the handler is
// built without autoRefresh, and no read may walk the transcript roots.
func TestServeNoIngestNeverScansOnARequest(t *testing.T) {
	e2eEnv(t)
	setupTestHome(t)
	a, err := query.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	srv := httptest.NewServer(newWebHandler(a, "test", "http://127.0.0.1:7477", false))
	t.Cleanup(srv.Close)

	var out query.ReportOut
	getJSON(t, srv, "/api/report?since=all", &out)
	if out.Sessions != 0 || out.IngestedAt != "" {
		t.Fatalf("report under --no-ingest scanned: sessions=%d ingested_at=%q", out.Sessions, out.IngestedAt)
	}
	// An explicit rescan is still allowed; that is what the button is for.
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/refresh", nil)
	req.Header.Set(requestHeader, "1")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("POST /api/refresh: status %d, want 200", res.StatusCode)
	}
	getJSON(t, srv, "/api/report?since=all", &out)
	if out.Sessions == 0 {
		t.Fatal("after an explicit rescan the report is still empty")
	}
}
