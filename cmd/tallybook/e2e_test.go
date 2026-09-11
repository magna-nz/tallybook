// End-to-end tests: they execute the real CLI in-process against the
// checked-in transcript fixtures, with a temp database and a temp
// TALLYBOOK_DIR. No test in this file ever touches the user's real
// ~/.claude, ~/.codex or ~/.config directories.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/magna-nz/tallybook/internal/config"
)

// usdAmountRe matches a formatted dollar figure like "$1,234.56".
var usdAmountRe = regexp.MustCompile(`\$[0-9,]+\.[0-9]{2}`)

// repoRoot returns the absolute path to the tallybook module root, resolved
// from this test file's own location on disk so it never depends on the
// working directory `go test` happens to use.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	// file is <repoRoot>/cmd/tallybook/e2e_test.go
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
// transcript roots at the checked-in fixtures. It returns the database
// path. Every value comes from t.TempDir() or the fixtures under
// internal/transcript/..., never from the real environment.
func e2eEnv(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	assertUnderTempDir(t, dir)
	t.Setenv("TALLYBOOK_DIR", dir)

	root := repoRoot(t)
	t.Setenv("TALLYBOOK_CLAUDE_ROOTS", filepath.Join(root, "internal", "transcript", "claude", "testdata", "projects"))
	t.Setenv("TALLYBOOK_CODEX_ROOTS", filepath.Join(root, "internal", "transcript", "codex", "testdata", "sessions"))
	t.Setenv("TALLYBOOK_PLAN", "api")

	dbPath := filepath.Join(t.TempDir(), "tallybook.db")
	t.Setenv("TALLYBOOK_DB", dbPath)

	return dbPath
}

// run builds a fresh root command, executes it with args, and returns its
// captured stdout/stderr output. Cobra's SilenceErrors means an error
// returned here was never printed to out; check err.Error() for the
// message.
func run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := newRoot()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

func TestE2EReportAll(t *testing.T) {
	e2eEnv(t)

	out, err := run(t, "--since", "all", "report")
	if err != nil {
		t.Fatalf("report --since all: %v\noutput:\n%s", err, out)
	}

	for _, want := range []string{"Scanned", "All time", "Total", "list price"} {
		if !strings.Contains(out, want) {
			t.Errorf("report output missing %q:\n%s", want, out)
		}
	}
	if !usdAmountRe.MatchString(out) {
		t.Errorf("report output has no dollar amount matching %s:\n%s", usdAmountRe, out)
	}
	for _, line := range strings.Split(out, "\n") {
		if len(line) > 100 {
			t.Errorf("report line exceeds 100 columns (%d): %q", len(line), line)
		}
	}
}

func TestE2EReportJSON(t *testing.T) {
	e2eEnv(t)

	out, err := run(t, "--since", "all", "--json", "report")
	if err != nil {
		t.Fatalf("report --json: %v\noutput:\n%s", err, out)
	}

	var doc struct {
		Schema int     `json:"schema"`
		USD    float64 `json:"usd"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("report --json did not parse: %v\noutput:\n%s", err, out)
	}
	if doc.Schema != 1 {
		t.Errorf("schema = %d, want 1", doc.Schema)
	}
	if doc.USD <= 0 {
		t.Errorf("usd = %v, want a positive amount", doc.USD)
	}
}

// TestE2ESourceFlags checks that --claude and --codex each restrict the
// session count to their own source, and that passing both together counts
// the union. It uses `sessions --json`, whose "sessions" array lists both
// main and sub-agent sessions, since report's JSON only counts main
// sessions.
func TestE2ESourceFlags(t *testing.T) {
	e2eEnv(t)

	type sessionsDoc struct {
		Sessions []struct {
			ID string `json:"id"`
		} `json:"sessions"`
	}

	claudeOut, err := run(t, "--claude", "--since", "all", "--json", "sessions")
	if err != nil {
		t.Fatalf("sessions --claude: %v\noutput:\n%s", err, claudeOut)
	}
	var claudeDoc sessionsDoc
	if err := json.Unmarshal([]byte(claudeOut), &claudeDoc); err != nil {
		t.Fatalf("sessions --claude --json did not parse: %v\noutput:\n%s", err, claudeOut)
	}
	if len(claudeDoc.Sessions) != 2 {
		t.Errorf("--claude sessions = %d, want 2: %+v", len(claudeDoc.Sessions), claudeDoc.Sessions)
	}

	codexOut, err := run(t, "--codex", "--since", "all", "--json", "sessions")
	if err != nil {
		t.Fatalf("sessions --codex: %v\noutput:\n%s", err, codexOut)
	}
	var codexDoc sessionsDoc
	if err := json.Unmarshal([]byte(codexOut), &codexDoc); err != nil {
		t.Fatalf("sessions --codex --json did not parse: %v\noutput:\n%s", err, codexOut)
	}
	if len(codexDoc.Sessions) != 2 {
		t.Errorf("--codex sessions = %d, want 2: %+v", len(codexDoc.Sessions), codexDoc.Sessions)
	}

	bothOut, err := run(t, "--claude", "--codex", "--since", "all", "--json", "sessions")
	if err != nil {
		t.Fatalf("sessions --claude --codex: %v\noutput:\n%s", err, bothOut)
	}
	var bothDoc sessionsDoc
	if err := json.Unmarshal([]byte(bothOut), &bothDoc); err != nil {
		t.Fatalf("sessions --claude --codex --json did not parse: %v\noutput:\n%s", err, bothOut)
	}
	wantUnion := len(claudeDoc.Sessions) + len(codexDoc.Sessions)
	if len(bothDoc.Sessions) != wantUnion {
		t.Errorf("--claude --codex sessions = %d, want the union %d", len(bothDoc.Sessions), wantUnion)
	}
	if len(bothDoc.Sessions) != 4 {
		t.Errorf("--claude --codex sessions = %d, want 4: %+v", len(bothDoc.Sessions), bothDoc.Sessions)
	}
}

// TestE2EFindingIndexMatchesReport checks that `finding 1`'s title matches
// the first entry of `report`'s findings list, when there is one. The
// fixtures are small enough that no rule may cross its evidence floor; in
// that case `finding 1` must fail cleanly instead.
func TestE2EFindingIndexMatchesReport(t *testing.T) {
	e2eEnv(t)

	reportOut, err := run(t, "--since", "all", "--json", "report")
	if err != nil {
		t.Fatalf("report --json: %v\noutput:\n%s", err, reportOut)
	}
	var reportDoc struct {
		Findings []struct {
			Title string `json:"title"`
		} `json:"findings"`
	}
	if err := json.Unmarshal([]byte(reportOut), &reportDoc); err != nil {
		t.Fatalf("report --json did not parse: %v\noutput:\n%s", err, reportOut)
	}

	findingOut, findErr := run(t, "--since", "all", "--json", "finding", "1")

	if len(reportDoc.Findings) == 0 {
		if findErr == nil {
			t.Fatalf("finding 1 succeeded with no findings in the window, want an error\noutput:\n%s", findingOut)
		}
		if !strings.Contains(findErr.Error(), "no finding") {
			t.Errorf("finding 1 error message missing \"no finding\": %v", findErr)
		}
		return
	}

	if findErr != nil {
		t.Fatalf("finding 1: %v\noutput:\n%s", findErr, findingOut)
	}
	var findingDoc struct {
		Title string `json:"title"`
	}
	if err := json.Unmarshal([]byte(findingOut), &findingDoc); err != nil {
		t.Fatalf("finding --json did not parse: %v\noutput:\n%s", err, findingOut)
	}
	if findingDoc.Title != reportDoc.Findings[0].Title {
		t.Errorf("finding 1 title %q != report's first finding title %q", findingDoc.Title, reportDoc.Findings[0].Title)
	}
}

func TestE2EAgentsAndSessions(t *testing.T) {
	e2eEnv(t)

	agentsOut, err := run(t, "--since", "all", "agents")
	if err != nil {
		t.Fatalf("agents: %v\noutput:\n%s", err, agentsOut)
	}
	header, _, _ := strings.Cut(agentsOut, "\n")
	if !strings.Contains(header, "agent") || !strings.Contains(header, "runs") {
		t.Errorf("agents header missing \"agent\"/\"runs\": %q", header)
	}
	if !strings.Contains(agentsOut, "researcher") {
		t.Errorf("agents output missing a \"researcher\" row:\n%s", agentsOut)
	}

	sessionsOut, err := run(t, "--since", "all", "sessions")
	if err != nil {
		t.Fatalf("sessions: %v\noutput:\n%s", err, sessionsOut)
	}
	rows := strings.Split(strings.TrimRight(sessionsOut, "\n"), "\n")
	if len(rows) < 5 { // header + at least 4 session rows
		t.Errorf("sessions output has %d lines, want a header plus at least 4 rows:\n%s", len(rows), sessionsOut)
	}
	if !strings.Contains(sessionsOut, "thr_0001") {
		t.Errorf("sessions output missing \"thr_0001\":\n%s", sessionsOut)
	}

	if _, err := run(t, "--since", "all", "sessions", "--sort", "cost"); err != nil {
		t.Fatalf("sessions --sort cost: %v", err)
	}

	_, err = run(t, "--since", "all", "session", "thr_00")
	if err == nil {
		t.Fatal("session thr_00 succeeded, want an ambiguous-prefix error")
	}
	if !strings.Contains(err.Error(), "thr_0001") || !strings.Contains(err.Error(), "thr_0002") {
		t.Errorf("ambiguous session error does not name both sessions: %v", err)
	}

	sessionOut, err := run(t, "--since", "all", "session", "thr_0001")
	if err != nil {
		t.Fatalf("session thr_0001: %v\noutput:\n%s", err, sessionOut)
	}
	if !strings.Contains(sessionOut, "gpt-5.5") {
		t.Errorf("session thr_0001 output missing \"gpt-5.5\":\n%s", sessionOut)
	}
}

func TestE2EStatusAndPrices(t *testing.T) {
	dbPath := e2eEnv(t)

	statusOut, err := run(t, "--no-ingest", "status")
	if err != nil {
		t.Fatalf("status --no-ingest: %v\noutput:\n%s", err, statusOut)
	}
	if !strings.Contains(statusOut, dbPath) {
		t.Errorf("status output missing db path %q:\n%s", dbPath, statusOut)
	}
	if !strings.Contains(statusOut, "Plan") {
		t.Errorf("status output missing \"Plan\":\n%s", statusOut)
	}

	pricesOut, err := run(t, "prices")
	if err != nil {
		t.Fatalf("prices: %v\noutput:\n%s", err, pricesOut)
	}
	for _, want := range []string{"claude-opus-5", "gpt-5.5"} {
		if !strings.Contains(pricesOut, want) {
			t.Errorf("prices output missing %q:\n%s", want, pricesOut)
		}
	}

	pricesJSONOut, err := run(t, "prices", "--json")
	if err != nil {
		t.Fatalf("prices --json: %v\noutput:\n%s", err, pricesJSONOut)
	}
	var doc struct {
		Schema int `json:"schema"`
		Models []struct {
			ID string `json:"id"`
		} `json:"models"`
	}
	if err := json.Unmarshal([]byte(pricesJSONOut), &doc); err != nil {
		t.Fatalf("prices --json did not parse: %v\noutput:\n%s", err, pricesJSONOut)
	}
	if doc.Schema != 1 || len(doc.Models) == 0 {
		t.Errorf("prices --json = %+v, want schema 1 and at least one model", doc)
	}
}

func TestE2EConfigInit(t *testing.T) {
	e2eEnv(t)

	pathOut, err := run(t, "config", "path")
	if err != nil {
		t.Fatalf("config path: %v\noutput:\n%s", err, pathOut)
	}
	wantPath := config.Path()
	if strings.TrimSpace(pathOut) != wantPath {
		t.Errorf("config path = %q, want %q", strings.TrimSpace(pathOut), wantPath)
	}

	if _, err := run(t, "config", "init"); err != nil {
		t.Fatalf("config init: %v", err)
	}
	data, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatalf("read %s: %v", wantPath, err)
	}
	if !strings.Contains(string(data), `plan = "auto"`) {
		t.Errorf("config.toml missing plan = \"auto\":\n%s", data)
	}

	const marker = "\n# marker\n"
	if err := os.WriteFile(wantPath, append(data, []byte(marker)...), 0o644); err != nil {
		t.Fatalf("write marker into %s: %v", wantPath, err)
	}

	_, err = run(t, "config", "init")
	if err == nil {
		t.Fatal("config init without --force over an existing file succeeded, want an error")
	}
	var ue usageError
	if !errorsAsUsageError(err, &ue) {
		t.Errorf("config init without --force did not produce a usageError: %v (%T)", err, err)
	}
	after, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatalf("read %s after failed init: %v", wantPath, err)
	}
	if !strings.Contains(string(after), "# marker") {
		t.Errorf("config init without --force overwrote the file")
	}

	if _, err := run(t, "config", "init", "--force"); err != nil {
		t.Fatalf("config init --force: %v", err)
	}
	forced, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatalf("read %s after forced init: %v", wantPath, err)
	}
	if strings.Contains(string(forced), "# marker") {
		t.Error("config init --force did not overwrite the file")
	}
}

func TestE2EUsageErrors(t *testing.T) {
	e2eEnv(t)

	_, err := run(t, "--since", "nonsense", "report")
	if err == nil {
		t.Fatal("--since nonsense report succeeded, want an error")
	}
	var sinceErr usageError
	if !errorsAsUsageError(err, &sinceErr) {
		t.Errorf("--since nonsense did not produce a usageError: %v (%T)", err, err)
	}
	if !strings.Contains(err.Error(), "--since") {
		t.Errorf("error message missing \"--since\": %v", err)
	}

	_, err = run(t, "finding", "notanumber")
	if err == nil {
		t.Fatal("finding notanumber succeeded, want an error")
	}
	var findingErr usageError
	if !errorsAsUsageError(err, &findingErr) {
		t.Errorf("finding notanumber did not produce a usageError: %v (%T)", err, err)
	}
}

func TestE2ENoIngestOnEmptyDB(t *testing.T) {
	e2eEnv(t)

	out, err := run(t, "--no-ingest", "--since", "all", "report")
	if err != nil {
		t.Fatalf("--no-ingest report on an empty db: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, "no sessions") {
		t.Errorf("report output missing \"no sessions\":\n%s", out)
	}
}

// TestE2EFindingsCommand runs `findings` over the fixtures and checks that
// it lists every finding the report's JSON knows about, that its numbers are
// the ones `finding <n>` takes, and that --json returns the same array.
func TestE2EFindingsCommand(t *testing.T) {
	e2eEnv(t)

	reportOut, err := run(t, "--since", "all", "--json", "report")
	if err != nil {
		t.Fatalf("report --json: %v\noutput:\n%s", err, reportOut)
	}
	type findingsDoc struct {
		Findings []struct {
			Title     string `json:"title"`
			Direction string `json:"direction"`
		} `json:"findings"`
	}
	var reportDoc findingsDoc
	if err := json.Unmarshal([]byte(reportOut), &reportDoc); err != nil {
		t.Fatalf("report --json did not parse: %v\noutput:\n%s", err, reportOut)
	}

	jsonOut, err := run(t, "--since", "all", "--json", "findings")
	if err != nil {
		t.Fatalf("findings --json: %v\noutput:\n%s", err, jsonOut)
	}
	var listDoc findingsDoc
	if err := json.Unmarshal([]byte(jsonOut), &listDoc); err != nil {
		t.Fatalf("findings --json did not parse: %v\noutput:\n%s", err, jsonOut)
	}
	if len(listDoc.Findings) != len(reportDoc.Findings) {
		t.Errorf("findings --json has %d findings, report --json has %d",
			len(listDoc.Findings), len(reportDoc.Findings))
	}
	for i := range listDoc.Findings {
		if i < len(reportDoc.Findings) && listDoc.Findings[i].Title != reportDoc.Findings[i].Title {
			t.Errorf("findings[%d] = %q, report's = %q", i, listDoc.Findings[i].Title, reportDoc.Findings[i].Title)
		}
	}

	out, err := run(t, "--since", "all", "findings")
	if err != nil {
		t.Fatalf("findings: %v\noutput:\n%s", err, out)
	}
	for _, line := range strings.Split(out, "\n") {
		if len(line) > 96 {
			t.Errorf("findings line exceeds 96 columns (%d): %q", len(line), line)
		}
	}

	if len(listDoc.Findings) == 0 {
		if !strings.Contains(out, "No findings in this window.") {
			t.Errorf("findings output on an empty window missing the no-findings line:\n%s", out)
		}
		return
	}

	if !strings.Contains(out, "Every finding in this window (estimated saving / month)") {
		t.Errorf("findings output missing its heading:\n%s", out)
	}
	if !strings.Contains(out, "Run `tallybook finding <n>` for evidence and the change to make.") {
		t.Errorf("findings output missing the closing line:\n%s", out)
	}
	// Every finding is listed, under a heading, with the number that
	// `finding <n>` takes.
	for i, f := range listDoc.Findings {
		if !strings.Contains(out, f.Title) {
			t.Errorf("findings output does not list %q:\n%s", f.Title, out)
		}
		wantPrefix := fmt.Sprintf("%2d. ", i+1)
		found := false
		for _, line := range strings.Split(out, "\n") {
			if strings.HasPrefix(line, wantPrefix) && strings.Contains(line, f.Title) {
				found = true
			}
		}
		if !found {
			t.Errorf("findings output has no line %q%s:\n%s", wantPrefix, f.Title, out)
		}
	}

	// The number a line carries really does resolve through `finding <n>`.
	detail, err := run(t, "--since", "all", "--json", "finding", "1")
	if err != nil {
		t.Fatalf("finding 1: %v\noutput:\n%s", err, detail)
	}
	var one struct {
		Title string `json:"title"`
	}
	if err := json.Unmarshal([]byte(detail), &one); err != nil {
		t.Fatalf("finding 1 --json did not parse: %v\noutput:\n%s", err, detail)
	}
	if one.Title != listDoc.Findings[0].Title {
		t.Errorf("finding 1 title %q != findings' first title %q", one.Title, listDoc.Findings[0].Title)
	}

	t.Log("\n" + strings.TrimRight(out, "\n"))
}

// The fixtures run from 1 to 2 September 2026. A window starting on the 2nd
// therefore has the 1st in the window before it, however far today drifts
// from the fixtures: the prior window is the same length and ends where this
// one starts, so it only ever grows backwards.
const compareSince = "2026-09-02"

func TestE2ECompareShowsThePriorWindow(t *testing.T) {
	e2eEnv(t)

	out, err := run(t, "--since", compareSince, "report", "--compare")
	if err != nil {
		t.Fatalf("report --compare: %v\noutput:\n%s", err, out)
	}
	for _, want := range []string{
		"Compared with the", "before (", // the heading names the prior window
		"Total", "before, ", " now", // and every line gives both figures
		"Cache hit rate",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("compare output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "nothing to compare with") {
		t.Errorf("the prior window holds the 1 September fixtures, so it is not empty:\n%s", out)
	}
	// The block sits between the totals and the findings, and stays narrow.
	if strings.Index(out, "Compared with") < strings.Index(out, "Total") {
		t.Errorf("comparison should follow the totals table:\n%s", out)
	}
	for _, line := range strings.Split(out, "\n") {
		if len(line) > 100 {
			t.Errorf("compare line exceeds 100 columns (%d): %q", len(line), line)
		}
	}
}

func TestE2ECompareWorksOnTheBareCommand(t *testing.T) {
	e2eEnv(t)

	out, err := run(t, "--since", compareSince, "--compare")
	if err != nil {
		t.Fatalf("tallybook --compare: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, "Compared with the") {
		t.Errorf("bare command with --compare printed no comparison:\n%s", out)
	}
}

func TestE2ECompareJSON(t *testing.T) {
	e2eEnv(t)

	out, err := run(t, "--since", compareSince, "--json", "report", "--compare")
	if err != nil {
		t.Fatalf("report --compare --json: %v\noutput:\n%s", err, out)
	}
	var doc struct {
		USD          float64 `json:"usd"`
		CacheHitRate float64 `json:"cacheHitRate"`
		Compare      *struct {
			USD          float64 `json:"usd"`
			Sessions     int     `json:"sessions"`
			CacheHitRate float64 `json:"cacheHitRate"`
			Window       struct {
				Since string  `json:"since"`
				Until string  `json:"until"`
				Days  float64 `json:"days"`
			} `json:"window"`
		} `json:"compare"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("parse: %v\n%s", err, out)
	}
	if doc.CacheHitRate < 0 || doc.CacheHitRate > 1 {
		t.Errorf("cacheHitRate = %v, want 0..1", doc.CacheHitRate)
	}
	if doc.Compare == nil {
		t.Fatalf("compare object missing:\n%s", out)
	}
	if doc.Compare.Sessions == 0 || doc.Compare.USD <= 0 {
		t.Errorf("prior window should hold the 1 September fixtures with a cost: %+v", doc.Compare)
	}
	if doc.Compare.Window.Until != compareSince {
		t.Errorf("prior window should end where this one starts: until = %q, want %q", doc.Compare.Window.Until, compareSince)
	}
	if doc.Compare.Window.Days <= 0 {
		t.Errorf("prior window has no length: %+v", doc.Compare.Window)
	}
	if doc.Compare.CacheHitRate < 0 || doc.Compare.CacheHitRate > 1 {
		t.Errorf("compare.cacheHitRate = %v, want 0..1", doc.Compare.CacheHitRate)
	}
}

func TestE2ECompareRefusesAllTime(t *testing.T) {
	e2eEnv(t)

	_, err := run(t, "--since", "all", "report", "--compare")
	if err == nil {
		t.Fatal("--since all --compare succeeded; there is no window before all time")
	}
	var ue usageError
	if !errorsAsUsageError(err, &ue) {
		t.Errorf("--since all --compare did not produce a usageError: %v (%T)", err, err)
	}
	if !strings.Contains(err.Error(), "--compare") {
		t.Errorf("error should name the flag: %v", err)
	}
}

func TestE2EReportWithoutCompareHasNoComparison(t *testing.T) {
	e2eEnv(t)

	out, err := run(t, "--since", compareSince, "report")
	if err != nil {
		t.Fatalf("report: %v\n%s", err, out)
	}
	if strings.Contains(out, "Compared with") || strings.Contains(out, "nothing to compare") {
		t.Errorf("no comparison was asked for:\n%s", out)
	}
	if !strings.Contains(out, "Cache hit rate") {
		t.Errorf("the cache hit rate line is always shown:\n%s", out)
	}
	jsonOut, err := run(t, "--since", compareSince, "--json", "report")
	if err != nil {
		t.Fatalf("report --json: %v\n%s", err, jsonOut)
	}
	if strings.Contains(jsonOut, `"compare"`) {
		t.Errorf("compare should be omitted from JSON when not asked for:\n%s", jsonOut)
	}
	if !strings.Contains(jsonOut, `"cacheHitRate"`) {
		t.Errorf("cacheHitRate should always be in JSON:\n%s", jsonOut)
	}
}

func TestE2ECompareOnAnEmptyPriorWindow(t *testing.T) {
	e2eEnv(t)

	// A window starting on the 1st has nothing before it in the fixtures.
	out, err := run(t, "--since", "2026-09-01", "report", "--compare")
	if err != nil {
		t.Fatalf("report --compare: %v\n%s", err, out)
	}
	if !strings.Contains(out, "nothing to compare with") {
		t.Errorf("an empty prior window should say so:\n%s", out)
	}
}
