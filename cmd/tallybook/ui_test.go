// Browser tests for the web UI: a headless Chrome drives the page served by
// newWebHandler against the checked-in transcript fixtures, with a temp
// database, a temp TALLYBOOK_DIR and a temp HOME. They check what a person
// sees and clicks, which the HTTP tests in serve_test.go cannot: the rail,
// the scope bar, the drill-downs, the JSON toggle and the two buttons that
// write. Skipped when no Chrome is installed, so `go test ./...` still
// passes on a machine without one.
package main

import (
	"context"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/magna-nz/tallybook/internal/query"
)

// chromePath returns an installed Chrome or Chromium, or "" when none is
// found. chromedp searches the same names; this check exists only so the
// suite can skip cleanly instead of failing on a machine without a browser.
func chromePath() string {
	names := []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "chrome", "headless_shell"}
	for _, n := range names {
		if p, err := exec.LookPath(n); err == nil {
			return p
		}
	}
	var fixed []string
	switch runtime.GOOS {
	case "darwin":
		fixed = []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
		}
	case "windows":
		fixed = []string{
			filepath.Join(os.Getenv("ProgramFiles"), "Google", "Chrome", "Application", "chrome.exe"),
			filepath.Join(os.Getenv("ProgramFiles(x86)"), "Google", "Chrome", "Application", "chrome.exe"),
			filepath.Join(os.Getenv("LocalAppData"), "Google", "Chrome", "Application", "chrome.exe"),
		}
	}
	for _, p := range fixed {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// uiEnv is e2eEnv plus a fake HOME, so the hook button in the Config screen
// writes a settings.json under the temp directory and never the real one.
func uiEnv(t *testing.T) {
	t.Helper()
	e2eEnv(t)
	home := t.TempDir()
	assertUnderTempDir(t, home)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
}

// browser starts the web handler on a loopback httptest server and a
// headless Chrome pointed at it. It returns the page context and the
// server's URL. Everything is torn down on cleanup.
func browser(t *testing.T) (context.Context, string) {
	t.Helper()
	chrome := chromePath()
	if chrome == "" {
		t.Skip("no Chrome or Chromium installed; UI tests need one")
	}
	uiEnv(t)
	// The fixtures are tiny, so the default floors keep every rule quiet.
	// Lowering them in the isolated config dir gives the Findings screens a
	// real finding to render, one with a patch.
	cfg := "[findings]\nmin_runs = 1\nmin_saving_usd = 0\n"
	if err := os.WriteFile(filepath.Join(os.Getenv("TALLYBOOK_DIR"), "config.toml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}

	a, err := query.New()
	if err != nil {
		t.Fatalf("query.New: %v", err)
	}
	t.Cleanup(a.Close)
	a.Warm() // scan the fixtures once so the first page load is deterministic

	srv := httptest.NewServer(newWebHandler(a, "test", "http://127.0.0.1:0", true))
	t.Cleanup(srv.Close)

	opts := append(chromedp.DefaultExecAllocatorOptions[:], chromedp.ExecPath(chrome))
	actx, cancelA := chromedp.NewExecAllocator(context.Background(), opts...)
	t.Cleanup(cancelA)
	ctx, cancelC := chromedp.NewContext(actx)
	t.Cleanup(cancelC)
	ctx, cancelT := context.WithTimeout(ctx, 90*time.Second)
	t.Cleanup(cancelT)

	if err := chromedp.Run(ctx, chromedp.EmulateViewport(1280, 900)); err != nil {
		t.Skipf("could not start Chrome at %s: %v", chrome, err)
	}
	return ctx, srv.URL
}

// open navigates to a hash route and waits until the view has finished
// loading (the router clears aria-busy when the screen is painted).
func open(t *testing.T, ctx context.Context, url, hash string) {
	t.Helper()
	if err := chromedp.Run(ctx,
		chromedp.Navigate(url+"/#/"+hash),
		chromedp.WaitReady("#view"),
		chromedp.WaitNotPresent(`#view[aria-busy="true"]`, chromedp.ByQuery),
		chromedp.WaitVisible("#view h1", chromedp.ByQuery),
	); err != nil {
		t.Fatalf("open %s: %v", hash, err)
	}
}

// text returns the text content of the first element matching sel.
func text(t *testing.T, ctx context.Context, sel string) string {
	t.Helper()
	var s string
	if err := chromedp.Run(ctx, chromedp.Text(sel, &s, chromedp.ByQuery)); err != nil {
		t.Fatalf("text %s: %v", sel, err)
	}
	return s
}

// count returns how many elements match sel.
func count(t *testing.T, ctx context.Context, sel string) int {
	t.Helper()
	var n int
	if err := chromedp.Run(ctx, chromedp.Evaluate(`document.querySelectorAll(`+quoteJS(sel)+`).length`, &n)); err != nil {
		t.Fatalf("count %s: %v", sel, err)
	}
	return n
}

// click clicks sel and waits for the router to repaint the view.
func click(t *testing.T, ctx context.Context, sel string) {
	t.Helper()
	if err := chromedp.Run(ctx,
		chromedp.Click(sel, chromedp.ByQuery),
		chromedp.Sleep(50*time.Millisecond),
		chromedp.WaitNotPresent(`#view[aria-busy="true"]`, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("click %s: %v", sel, err)
	}
}

func quoteJS(s string) string { return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"` }

func TestUIOverviewShowsTotalsAndFindings(t *testing.T) {
	ctx, url := browser(t)
	open(t, ctx, url, "report")

	if h := text(t, ctx, "#view h1"); h != "Overview" {
		t.Fatalf("h1 = %q, want Overview", h)
	}
	if n := count(t, ctx, ".tile"); n != 4 {
		t.Fatalf("tiles = %d, want 4 (total, main, sub-agents, cache hit rate)", n)
	}
	sub := text(t, ctx, ".page-head .sub")
	if !strings.Contains(sub, "sessions") || !strings.Contains(sub, "sub-agent runs") {
		t.Fatalf("scan summary %q does not name sessions and sub-agent runs", sub)
	}
	// The rail shows only the screen names now; the CLI equivalent lives in
	// the page head, where it names the command this screen mirrors.
	if n := count(t, ctx, "#nav a code"); n != 0 {
		t.Fatalf("rail carries %d command labels, want none", n)
	}
	if c := text(t, ctx, ".cli code"); c != "tallybook" {
		t.Fatalf("CLI equivalent = %q, want tallybook", c)
	}
	// Findings: either rows, or the explicit empty state. Never a blank panel.
	if count(t, ctx, ".finding-row") == 0 && count(t, ctx, ".findings-list .empty") == 0 {
		t.Fatal("overview shows neither findings nor the no-findings message")
	}
	if got := text(t, ctx, "#fresh-text"); !strings.Contains(got, "Scanned") {
		t.Fatalf("freshness = %q, want a Scanned … line", got)
	}
}

func TestUIScopeBarChangesTheCLIEquivalent(t *testing.T) {
	ctx, url := browser(t)
	open(t, ctx, url, "report")

	if err := chromedp.Run(ctx,
		chromedp.SetValue("#f-since", "90d", chromedp.ByQuery),
		chromedp.Sleep(50*time.Millisecond),
		chromedp.WaitNotPresent(`#view[aria-busy="true"]`, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("set window: %v", err)
	}
	if c := text(t, ctx, ".cli code"); c != "tallybook --since 90d" {
		t.Fatalf("after choosing 90 days the CLI equivalent is %q", c)
	}
	click(t, ctx, `#f-source button[data-v="claude-code"]`)
	if c := text(t, ctx, ".cli code"); !strings.Contains(c, "--claude") {
		t.Fatalf("after choosing Claude Code the CLI equivalent is %q", c)
	}
	click(t, ctx, `#f-currency button[data-v="usd"]`)
	if c := text(t, ctx, ".cli code"); !strings.Contains(c, "--currency usd") {
		t.Fatalf("after choosing USD the CLI equivalent is %q", c)
	}
	if k := text(t, ctx, ".tile .k"); !strings.Contains(k, "bill") {
		t.Fatalf("with --currency usd the total tile says %q, want it labelled as a bill", k)
	}
}

func TestUIJSONToggleShowsTheStructuredResponse(t *testing.T) {
	ctx, url := browser(t)
	open(t, ctx, url, "report")
	click(t, ctx, "#json-toggle")
	body := text(t, ctx, "pre.json")
	for _, key := range []string{`"cache_hit_rate"`, `"by_model"`, `"findings"`, `"currency"`} {
		if !strings.Contains(body, key) {
			t.Fatalf("JSON view lacks %s", key)
		}
	}
	if c := text(t, ctx, ".cli code"); !strings.HasSuffix(c, "--json") {
		t.Fatalf("CLI equivalent in JSON mode = %q, want it to end with --json", c)
	}
	click(t, ctx, "#json-toggle")
	if count(t, ctx, "pre.json") != 0 {
		t.Fatal("JSON view did not close")
	}
}

func TestUISessionsDrillDownToTurns(t *testing.T) {
	ctx, url := browser(t)
	open(t, ctx, url, "sessions")

	if n := count(t, ctx, "tr.row"); n == 0 {
		t.Fatal("sessions table is empty on the fixtures")
	}
	// The fixture sub-agent run is its own row, labelled with its agent type
	// and linked to its parent.
	if n := count(t, ctx, "tr.row .pill.accent"); n == 0 {
		t.Fatal("no sub-agent row is labelled in the sessions table")
	}
	click(t, ctx, `#view .seg button[data-sort="cost"]`)
	if c := text(t, ctx, ".cli code"); !strings.Contains(c, "--sort cost") {
		t.Fatalf("after sorting by cost the CLI equivalent is %q", c)
	}

	open(t, ctx, url, "session/sess-0001")
	if h := text(t, ctx, "#view h1"); !strings.HasPrefix(h, "Session sess-000") {
		t.Fatalf("session h1 = %q", h)
	}
	turns := count(t, ctx, "#view table tbody tr.row")
	if turns == 0 {
		t.Fatal("session detail shows no turns")
	}
	if n := count(t, ctx, ".subagent-line"); n == 0 {
		t.Fatal("the fixture's sub-agent launch is not shown under its turn")
	}
	if n := count(t, ctx, "svg[aria-label='Context size per turn']"); n != 1 {
		t.Fatalf("context chart count = %d, want 1", n)
	}
	// Follow the child link and come back to the parent through its own link.
	click(t, ctx, ".subagent-line a")
	if h := text(t, ctx, "#view h1"); !strings.Contains(h, "/") {
		t.Fatalf("child session h1 = %q, want a parent/agent id", h)
	}
	if got := text(t, ctx, ".page-head .sub"); !strings.Contains(got, "launched from") {
		t.Fatalf("child session subtitle %q does not name its parent", got)
	}
}

func TestUIUnknownSessionShowsAnError(t *testing.T) {
	ctx, url := browser(t)
	open(t, ctx, url, "session/zzzz-not-a-session")
	if n := count(t, ctx, ".banner.crit"); n != 1 {
		t.Fatalf("error banner count = %d, want 1", n)
	}
	if b := text(t, ctx, ".banner.crit"); !strings.Contains(b, "zzzz-not-a-session") {
		t.Fatalf("error banner %q does not echo the id", b)
	}
}

func TestUIFindingsAndFindingDetail(t *testing.T) {
	ctx, url := browser(t)
	open(t, ctx, url, "findings")
	if h := text(t, ctx, "#view h1"); h != "Findings" {
		t.Fatalf("h1 = %q", h)
	}
	if count(t, ctx, ".finding-row") == 0 {
		t.Fatal("the fixtures produced no findings with the lowered floors")
	}
	click(t, ctx, ".finding-row")
	if n := count(t, ctx, ".four .panel"); n != 4 {
		t.Fatalf("finding detail panels = %d, want the four sections", n)
	}
	for _, h := range []string{"What happened", "Why it costs money", "What to change", "What to expect"} {
		var found bool
		if err := chromedp.Run(ctx, chromedp.Evaluate(`[...document.querySelectorAll('.four h3')].some(e => e.textContent === `+quoteJS(h)+`)`, &found)); err != nil || !found {
			t.Fatalf("section %q missing (err %v)", h, err)
		}
	}
	if c := text(t, ctx, ".cli code"); !strings.Contains(c, "finding ") || !strings.Contains(c, "--evidence") {
		t.Fatalf("finding CLI equivalent = %q", c)
	}
	// The fixture finding is the read-only researcher on Opus. Its project
	// directory does not exist on this machine, so there is no agent file to
	// patch and no patch block; the evidence table is always there.
	if n := count(t, ctx, "#view table tbody tr.row"); n == 0 {
		t.Fatal("evidence table is empty")
	}
}

func TestUIAgentsChangesPricesStatus(t *testing.T) {
	ctx, url := browser(t)
	for _, tc := range []struct{ hash, h1, cli string }{
		{"agents", "Sub-agents", "tallybook agents"},
		{"changes", "Model changes", "tallybook changes"},
		{"prices", "Prices", "tallybook prices"},
		{"status", "Status", "tallybook status"},
	} {
		open(t, ctx, url, tc.hash)
		if h := text(t, ctx, "#view h1"); h != tc.h1 {
			t.Fatalf("%s: h1 = %q, want %q", tc.hash, h, tc.h1)
		}
		if c := text(t, ctx, ".cli code"); c != tc.cli {
			t.Fatalf("%s: CLI equivalent = %q, want %q", tc.hash, c, tc.cli)
		}
		if n := count(t, ctx, "#nav a.active"); n != 1 {
			t.Fatalf("%s: %d active rail entries, want 1", tc.hash, n)
		}
	}
	if n := count(t, ctx, "#view table tbody tr"); n == 0 {
		t.Fatal("status shows no transcript roots")
	}
	if n := count(t, ctx, "#rescan"); n != 1 {
		t.Fatal("status has no rescan button")
	}
}

func TestUIRescanButtonReportsTheScan(t *testing.T) {
	ctx, url := browser(t)
	open(t, ctx, url, "status")
	click(t, ctx, "#rescan")
	var toast string
	if err := chromedp.Run(ctx,
		chromedp.WaitVisible("#toast.show", chromedp.ByQuery),
		chromedp.Text("#toast", &toast, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("toast: %v", err)
	}
	if !strings.HasPrefix(toast, "Scanned ") || !strings.Contains(toast, "unchanged") {
		t.Fatalf("rescan toast = %q", toast)
	}
}

func TestUIHookButtonWritesOnlyTheFakeHome(t *testing.T) {
	ctx, url := browser(t)
	open(t, ctx, url, "config")
	if n := count(t, ctx, ".pill.warn"); n == 0 {
		t.Fatal("a fresh HOME should show the hook as not installed")
	}
	click(t, ctx, "#install-hook")
	var toast string
	if err := chromedp.Run(ctx,
		chromedp.WaitVisible("#toast.show", chromedp.ByQuery),
		chromedp.Text("#toast", &toast, chromedp.ByQuery),
		chromedp.WaitNotPresent(`#view[aria-busy="true"]`, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("toast: %v", err)
	}
	if !strings.HasPrefix(toast, "Added a SessionEnd hook") {
		t.Fatalf("hook toast = %q", toast)
	}
	home, _ := os.UserHomeDir()
	assertUnderTempDir(t, home)
	raw, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil {
		t.Fatalf("settings.json was not written under the fake HOME: %v", err)
	}
	if !strings.Contains(string(raw), "tallybook hook session-end") {
		t.Fatalf("settings.json lacks the hook: %s", raw)
	}
	// After the write the screen re-renders and the button is disabled.
	var disabled bool
	if err := chromedp.Run(ctx, chromedp.Evaluate(`document.querySelector('#install-hook').disabled`, &disabled)); err != nil || !disabled {
		t.Fatalf("install button still enabled after writing (err %v)", err)
	}
}

func TestUIThemeToggleStampsTheDocument(t *testing.T) {
	ctx, url := browser(t)
	open(t, ctx, url, "report")
	click(t, ctx, "#f-theme")
	var theme string
	if err := chromedp.Run(ctx, chromedp.Evaluate(`document.documentElement.getAttribute('data-theme') || ''`, &theme)); err != nil {
		t.Fatalf("theme: %v", err)
	}
	if theme != "dark" && theme != "light" {
		t.Fatalf("data-theme = %q after toggling, want dark or light", theme)
	}
}
