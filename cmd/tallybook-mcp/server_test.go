// Tests for the MCP server: an in-process client/server pair talking over
// mcp.NewInMemoryTransports, driven against the checked-in transcript
// fixtures with a temp database and a temp TALLYBOOK_DIR. No test in this
// file ever touches the user's real ~/.claude, ~/.codex or ~/.config
// directories.
package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/magna-nz/tallybook/internal/ledger"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// repoRoot returns the absolute path to the tallybook module root, resolved
// from this test file's own location on disk so it never depends on the
// working directory `go test` happens to use.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	// file is <repoRoot>/cmd/tallybook-mcp/server_test.go
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

// assertUnderTempDir fails the test if dir is not rooted under os.TempDir(),
// so a bug here can never point tallybook at a real home directory.
func assertUnderTempDir(t *testing.T, dir string) {
	t.Helper()
	tmp := os.TempDir()
	realTmp, err := filepath.EvalSymlinks(tmp)
	if err != nil {
		realTmp = tmp
	}
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		realDir = dir
	}
	if !strings.HasPrefix(realDir, realTmp) {
		t.Fatalf("temp dir %q is not under os.TempDir() %q", dir, tmp)
	}
}

// e2eEnv isolates one test's TALLYBOOK_DIR and database, and points the
// transcript roots at the checked-in fixtures. Every value comes from
// t.TempDir() or the fixtures under internal/transcript/..., never from the
// real environment.
func e2eEnv(t *testing.T) {
	t.Helper()

	dir := t.TempDir()
	assertUnderTempDir(t, dir)
	t.Setenv("TALLYBOOK_DIR", dir)

	// agentfile.Load reads ~/.claude/agents, so fake the home directory the
	// way every platform reads it (see cmd/tallybook/setup_test.go).
	home := t.TempDir()
	assertUnderTempDir(t, home)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	root := repoRoot(t)
	t.Setenv("TALLYBOOK_CLAUDE_ROOTS", filepath.Join(root, "internal", "transcript", "claude", "testdata", "projects"))
	t.Setenv("TALLYBOOK_CODEX_ROOTS", filepath.Join(root, "internal", "transcript", "codex", "testdata", "sessions"))
	t.Setenv("TALLYBOOK_PLAN", "api")
	t.Setenv("TALLYBOOK_DB", filepath.Join(t.TempDir(), "tallybook.db"))
}

// newTestApp sets up an isolated environment and opens a fresh app against
// it, closed on cleanup. e2eEnv forces TALLYBOOK_PLAN=api; a test that needs
// another plan calls e2eEnv, overrides the variable, and calls newApp
// itself.
func newTestApp(t *testing.T) *app {
	t.Helper()
	e2eEnv(t)
	a, err := newApp()
	if err != nil {
		t.Fatalf("newApp: %v", err)
	}
	t.Cleanup(a.close)
	return a
}

// callTool calls a tool by name and fails the test if the transport-level
// call itself errors (a tool-level error is reported via IsError, not a Go
// error, and is not a failure here).
func callTool(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%s): %v", name, err)
	}
	return res
}

// decode marshals a tool result's structured content and unmarshals it into
// T, the Out struct the tool declares in tools.go.
func decode[T any](t *testing.T, res *mcp.CallToolResult) T {
	t.Helper()
	var out T
	b, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal into %T: %v", out, err)
	}
	return out
}

// resultText returns the text of the first text content block, or "" if
// there is none.
func resultText(res *mcp.CallToolResult) string {
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			return tc.Text
		}
	}
	return ""
}

// newTestSession wires an in-memory client/server pair against a and
// returns the connected client session plus a cleanup-free close func. Both
// sessions are closed via t.Cleanup.
func newTestSession(t *testing.T, a *app) *mcp.ClientSession {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ct, st := mcp.NewInMemoryTransports()
	server := newServer(a, "test")
	ss, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatalf("server.Connect: %v", err)
	}
	t.Cleanup(func() { ss.Close() })

	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	t.Cleanup(func() { cs.Close() })

	return cs
}

func TestToolsList(t *testing.T) {
	a := newTestApp(t)
	cs := newTestSession(t, a)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	lt, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	want := map[string]bool{
		"report": false, "finding": false, "changes": false, "agents": false,
		"sessions": false, "session": false, "prices": false, "refresh": false,
	}
	if len(lt.Tools) != len(want) {
		var got []string
		for _, tl := range lt.Tools {
			got = append(got, tl.Name)
		}
		t.Fatalf("got %d tools %v, want %d: %v", len(lt.Tools), got, len(want), want)
	}
	for _, tl := range lt.Tools {
		if _, ok := want[tl.Name]; !ok {
			t.Errorf("unexpected tool %q", tl.Name)
			continue
		}
		want[tl.Name] = true
		if tl.Description == "" {
			t.Errorf("tool %q has no description", tl.Name)
		}
		if tl.InputSchema == nil {
			t.Errorf("tool %q has no input schema", tl.Name)
		}
		if tl.OutputSchema == nil {
			t.Errorf("tool %q has no output schema", tl.Name)
		}
		if tl.Annotations == nil {
			t.Errorf("tool %q has no annotations", tl.Name)
			continue
		}
		wantReadOnly := tl.Name != "refresh"
		if tl.Annotations.ReadOnlyHint != wantReadOnly {
			t.Errorf("tool %q readOnlyHint = %v, want %v", tl.Name, tl.Annotations.ReadOnlyHint, wantReadOnly)
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("expected tool %q was not returned", name)
		}
	}
}

func TestReportPositiveTotal(t *testing.T) {
	a := newTestApp(t)
	cs := newTestSession(t, a)

	res := callTool(t, cs, "report", map[string]any{"since": "all"})
	if res.IsError {
		t.Fatalf("report returned IsError, text: %s", resultText(res))
	}
	out := decode[ReportOut](t, res)
	if out.USD <= 0 {
		t.Errorf("USD = %v, want > 0", out.USD)
	}
	if out.Currency != "usd" {
		t.Errorf("Currency = %q, want usd", out.Currency)
	}
	if out.Plan != "api" {
		t.Errorf("Plan = %q, want api", out.Plan)
	}
	if out.IngestedAt == "" {
		t.Error("IngestedAt is empty")
	}
	text := resultText(res)
	if text == "" {
		t.Error("text content is empty")
	}
	if !strings.Contains(text, "$") {
		t.Errorf("text content %q does not contain $", text)
	}
}

func TestFindingUnknownID(t *testing.T) {
	a := newTestApp(t)
	cs := newTestSession(t, a)

	res := callTool(t, cs, "finding", map[string]any{"since": "all", "id": "does-not-exist"})
	if !res.IsError {
		t.Fatalf("finding with unknown id: IsError = false, want true; text: %s", resultText(res))
	}
	if !strings.Contains(resultText(res), "does-not-exist") {
		t.Errorf("text %q does not mention the id", resultText(res))
	}

	res = callTool(t, cs, "finding", map[string]any{"since": "all", "id": ""})
	if !res.IsError {
		t.Fatalf("finding with empty id: IsError = false, want true; text: %s", resultText(res))
	}
}

func TestSubscriptionLabelsMoney(t *testing.T) {
	t.Run("subscription plan", func(t *testing.T) {
		e2eEnv(t)
		t.Setenv("TALLYBOOK_PLAN", "subscription")
		a, err := newApp()
		if err != nil {
			t.Fatalf("newApp: %v", err)
		}
		t.Cleanup(a.close)
		cs := newTestSession(t, a)

		res := callTool(t, cs, "report", map[string]any{"since": "all"})
		if res.IsError {
			t.Fatalf("report returned IsError, text: %s", resultText(res))
		}
		out := decode[ReportOut](t, res)
		if out.Currency != "list_price_equivalent" {
			t.Errorf("Currency = %q, want list_price_equivalent", out.Currency)
		}
		if out.Plan != "subscription" {
			t.Errorf("Plan = %q, want subscription", out.Plan)
		}
		text := resultText(res)
		if !strings.Contains(text, "not what was billed") {
			t.Errorf("text %q does not contain %q", text, "not what was billed")
		}
		if strings.Contains(text, "API plan") {
			t.Errorf("text %q carries the API-plan sentence on a subscription", text)
		}
	})

	t.Run("currency override", func(t *testing.T) {
		a := newTestApp(t)
		cs := newTestSession(t, a)

		res := callTool(t, cs, "report", map[string]any{"since": "all", "currency": "share"})
		if res.IsError {
			t.Fatalf("report returned IsError, text: %s", resultText(res))
		}
		out := decode[ReportOut](t, res)
		if out.Currency != "list_price_equivalent" {
			t.Errorf("Currency = %q, want list_price_equivalent", out.Currency)
		}
	})
}

func TestSourceFilterSplits(t *testing.T) {
	a := newTestApp(t)
	cs := newTestSession(t, a)

	codexRes := callTool(t, cs, "report", map[string]any{"since": "all", "source": "codex"})
	if codexRes.IsError {
		t.Fatalf("report source=codex returned IsError, text: %s", resultText(codexRes))
	}
	codexOut := decode[ReportOut](t, codexRes)
	if len(codexOut.BySource) != 1 {
		t.Fatalf("source=codex BySource = %v, want exactly one key", codexOut.BySource)
	}
	if _, ok := codexOut.BySource["codex"]; !ok {
		t.Errorf("source=codex BySource = %v, want key %q", codexOut.BySource, "codex")
	}

	claudeRes := callTool(t, cs, "report", map[string]any{"since": "all", "source": "claude-code"})
	if claudeRes.IsError {
		t.Fatalf("report source=claude-code returned IsError, text: %s", resultText(claudeRes))
	}
	claudeOut := decode[ReportOut](t, claudeRes)
	if len(claudeOut.BySource) != 1 {
		t.Fatalf("source=claude-code BySource = %v, want exactly one key", claudeOut.BySource)
	}
	if _, ok := claudeOut.BySource["claude-code"]; !ok {
		t.Errorf("source=claude-code BySource = %v, want key %q", claudeOut.BySource, "claude-code")
	}

	unfiltered := decode[ReportOut](t, callTool(t, cs, "report", map[string]any{"since": "all"}))
	sum := codexOut.USD + claudeOut.USD
	diff := sum - unfiltered.USD
	if diff < 0 {
		diff = -diff
	}
	if diff > 1e-9 {
		t.Errorf("codex USD %v + claude-code USD %v = %v, want %v (unfiltered)", codexOut.USD, claudeOut.USD, sum, unfiltered.USD)
	}

	bogus := callTool(t, cs, "report", map[string]any{"since": "all", "source": "nonsense"})
	if !bogus.IsError {
		t.Fatalf("report source=nonsense: IsError = false, want true; text: %s", resultText(bogus))
	}
}

func TestMatchesLedgerTotal(t *testing.T) {
	a := newTestApp(t)
	cs := newTestSession(t, a)

	res := callTool(t, cs, "report", map[string]any{"since": "all"})
	if res.IsError {
		t.Fatalf("report returned IsError, text: %s", resultText(res))
	}
	out := decode[ReportOut](t, res)

	sc, err := a.newScope(Scope{Since: "all"})
	if err != nil {
		t.Fatalf("newScope: %v", err)
	}
	sessions, err := ledger.Sessions(a.st, a.prices, sc.filter)
	if err != nil {
		t.Fatalf("ledger.Sessions: %v", err)
	}
	tot, err := ledger.Total(sessions, a.st, a.prices)
	if err != nil {
		t.Fatalf("ledger.Total: %v", err)
	}
	if tot.USD != out.USD {
		t.Errorf("ledger.Total USD = %v, report USD = %v, want equal", tot.USD, out.USD)
	}
}

func TestSessionsAndSession(t *testing.T) {
	a := newTestApp(t)
	cs := newTestSession(t, a)

	res := callTool(t, cs, "sessions", map[string]any{"since": "all", "sort": "cost", "limit": 2})
	if res.IsError {
		t.Fatalf("sessions returned IsError, text: %s", resultText(res))
	}
	out := decode[SessionsOut](t, res)
	if len(out.Sessions) != 2 {
		t.Fatalf("len(Sessions) = %d, want 2", len(out.Sessions))
	}
	if out.Total != 4 {
		t.Fatalf("Total = %d, want 4", out.Total)
	}
	for i := 1; i < len(out.Sessions); i++ {
		if out.Sessions[i].USD > out.Sessions[i-1].USD {
			t.Errorf("Sessions not sorted descending by USD at index %d: %v then %v", i, out.Sessions[i-1].USD, out.Sessions[i].USD)
		}
	}

	all := decode[SessionsOut](t, callTool(t, cs, "sessions", map[string]any{"since": "all", "limit": 0}))
	if len(all.Sessions) != 4 {
		t.Fatalf("len(Sessions) with limit 0 = %d, want every row (4)", len(all.Sessions))
	}
	dflt := decode[SessionsOut](t, callTool(t, cs, "sessions", map[string]any{"since": "all"}))
	if len(dflt.Sessions) != 4 || dflt.Total != 4 {
		t.Fatalf("sessions with no limit: %d rows, Total %d, want 4 and 4", len(dflt.Sessions), dflt.Total)
	}

	bogusSort := callTool(t, cs, "sessions", map[string]any{"since": "all", "sort": "bogus"})
	if !bogusSort.IsError {
		t.Fatalf("sessions sort=bogus: IsError = false, want true; text: %s", resultText(bogusSort))
	}

	full := callTool(t, cs, "session", map[string]any{"id": "thr_0001"})
	if full.IsError {
		t.Fatalf("session id=thr_0001 returned IsError, text: %s", resultText(full))
	}
	fullOut := decode[SessionDetailOut](t, full)
	if fullOut.ID != "thr_0001" {
		t.Errorf("ID = %q, want thr_0001", fullOut.ID)
	}
	if len(fullOut.Turns) == 0 {
		t.Error("Turns is empty, want at least one")
	}
	if fullOut.USD <= 0 {
		t.Errorf("USD = %v, want > 0", fullOut.USD)
	}

	ambiguous := callTool(t, cs, "session", map[string]any{"id": "thr_00"})
	if !ambiguous.IsError {
		t.Fatalf("session id=thr_00: IsError = false, want true; text: %s", resultText(ambiguous))
	}
	text := resultText(ambiguous)
	if !strings.Contains(text, "thr_0001") || !strings.Contains(text, "thr_0002") {
		t.Errorf("ambiguous text %q does not name both thr_0001 and thr_0002", text)
	}

	missing := callTool(t, cs, "session", map[string]any{"id": "zzz"})
	if !missing.IsError {
		t.Fatalf("session id=zzz: IsError = false, want true; text: %s", resultText(missing))
	}
}

func TestNoTranscriptTextLeaks(t *testing.T) {
	a := newTestApp(t)
	cs := newTestSession(t, a)

	res := callTool(t, cs, "session", map[string]any{"id": "thr_0001"})
	if res.IsError {
		t.Fatalf("session returned IsError, text: %s", resultText(res))
	}
	b, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	forbidden := map[string]bool{
		"text": true, "content": true, "prompt": true, "command": true,
		"input_text": true, "output_text": true, "description": true, "arguments": true,
	}
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			for k, vv := range x {
				if forbidden[k] {
					t.Errorf("forbidden key %q present in structured content", k)
				}
				walk(vv)
			}
		case []any:
			for _, vv := range x {
				walk(vv)
			}
		}
	}
	walk(m)
}

func TestAgentsChangesPricesRefresh(t *testing.T) {
	a := newTestApp(t)
	cs := newTestSession(t, a)

	agentsRes := callTool(t, cs, "agents", map[string]any{"since": "all"})
	if agentsRes.IsError {
		t.Fatalf("agents returned IsError, text: %s", resultText(agentsRes))
	}
	agentsOut := decode[AgentsOut](t, agentsRes)
	found := false
	for _, ag := range agentsOut.Agents {
		if ag.Agent == "researcher" {
			found = true
			// The fixture's one researcher run only reads, so the share is
			// exactly 1: a 0..1 fraction, not a percentage.
			if ag.ReadOnlyShare != 1 {
				t.Errorf("researcher ReadOnlyShare = %v, want 1", ag.ReadOnlyShare)
			}
		}
	}
	if !found {
		t.Errorf("agents output %+v does not contain agent %q", agentsOut.Agents, "researcher")
	}
	if text := resultText(agentsRes); !strings.Contains(text, "100% of runs read-only") {
		t.Errorf("agents text %q does not say 100%% of runs read-only", text)
	}

	changesRes := callTool(t, cs, "changes", map[string]any{"since": "all"})
	if changesRes.IsError {
		t.Fatalf("changes returned IsError, text: %s", resultText(changesRes))
	}
	changesOut := decode[ChangesOut](t, changesRes)
	if changesOut.MinRuns != 3 {
		t.Errorf("MinRuns = %d, want 3 (default)", changesOut.MinRuns)
	}

	// The CLI rejects --min-runs 0; an explicit 0 must not silently become 3.
	for _, bad := range []int{0, -1} {
		badChanges := callTool(t, cs, "changes", map[string]any{"since": "all", "min_runs": bad})
		if !badChanges.IsError {
			t.Fatalf("changes min_runs=%d: IsError = false, want true; text: %s", bad, resultText(badChanges))
		}
	}

	pricesRes := callTool(t, cs, "prices", map[string]any{})
	if pricesRes.IsError {
		t.Fatalf("prices returned IsError, text: %s", resultText(pricesRes))
	}
	pricesOut := decode[PricesOut](t, pricesRes)
	if pricesOut.Verified == "" {
		t.Error("Verified is empty")
	}
	if len(pricesOut.Models) == 0 {
		t.Fatal("Models is empty")
	}
	haveOpus, haveGPT := false, false
	for _, m := range pricesOut.Models {
		if m.ID == "claude-opus-5" {
			haveOpus = true
		}
		if m.ID == "gpt-5.5" {
			haveGPT = true
		}
	}
	if !haveOpus {
		t.Error("Models does not contain claude-opus-5")
	}
	if !haveGPT {
		t.Error("Models does not contain gpt-5.5")
	}

	refreshRes := callTool(t, cs, "refresh", map[string]any{})
	if refreshRes.IsError {
		t.Fatalf("refresh returned IsError, text: %s", resultText(refreshRes))
	}
	refreshOut := decode[RefreshOut](t, refreshRes)
	if refreshOut.Scanned <= 0 {
		t.Errorf("Scanned = %d, want > 0", refreshOut.Scanned)
	}
	if refreshOut.AgeSeconds < 0 {
		t.Errorf("AgeSeconds = %d, want >= 0", refreshOut.AgeSeconds)
	}
}

func TestLazyRefresh(t *testing.T) {
	a := newTestApp(t)
	cs := newTestSession(t, a)

	fakeNow := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	a.now = func() time.Time { return fakeNow }

	// The first call performs the first scan (newApp does not).
	res := callTool(t, cs, "report", map[string]any{"since": "all"})
	if res.IsError {
		t.Fatalf("report returned IsError, text: %s", resultText(res))
	}
	if !a.lastIngest.Equal(fakeNow) || !a.lastAttempt.Equal(fakeNow) {
		t.Fatalf("after first call lastIngest = %v, lastAttempt = %v, want both %v", a.lastIngest, a.lastAttempt, fakeNow)
	}
	out := decode[ReportOut](t, res)
	if out.IngestedAt != fakeNow.Format(time.RFC3339) || out.AgeSeconds != 0 {
		t.Errorf("Freshness = %+v, want ingested_at %s and age 0", out.Freshness, fakeNow.Format(time.RFC3339))
	}

	before := a.lastIngest
	fakeNow = fakeNow.Add(30 * time.Second)
	res = callTool(t, cs, "report", map[string]any{"since": "all"})
	if res.IsError {
		t.Fatalf("report returned IsError, text: %s", resultText(res))
	}
	if !a.lastIngest.Equal(before) {
		t.Errorf("lastIngest = %v, want unchanged %v (well under refreshAfter)", a.lastIngest, before)
	}

	fakeNow = fakeNow.Add(31 * time.Second)
	res = callTool(t, cs, "report", map[string]any{"since": "all"})
	if res.IsError {
		t.Fatalf("report returned IsError, text: %s", resultText(res))
	}
	if !a.lastIngest.Equal(fakeNow) {
		t.Errorf("lastIngest = %v, want advanced to %v", a.lastIngest, fakeNow)
	}

	// A failed attempt is throttled like a successful one and surfaced in
	// both the structured value and the prose.
	a.lastError = "codex root unreadable (simulated)"
	res = callTool(t, cs, "report", map[string]any{"since": "all"})
	if res.IsError {
		t.Fatalf("report returned IsError, text: %s", resultText(res))
	}
	out = decode[ReportOut](t, res)
	if out.ScanError == "" {
		t.Error("ScanError is empty after a failed attempt")
	}
	if !strings.HasPrefix(resultText(res), "Warning:") {
		t.Errorf("text does not open with the scan warning: %q", resultText(res))
	}
}
