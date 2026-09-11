package report_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/magna-nz/tallybook/internal/config"
	"github.com/magna-nz/tallybook/internal/ledger"
	"github.com/magna-nz/tallybook/internal/model"
	"github.com/magna-nz/tallybook/internal/report"
)

// withUsage gives the fixture report a token mix: 800 of every 1,000
// input-side tokens read from the cache, so the hit rate is 80%.
func withUsage(d report.ReportData) report.ReportData {
	d.Totals.Usage = model.Usage{Input: 100, CacheRead: 800, CacheWrite5m: 50, CacheWrite1h: 50, Output: 400}
	return d
}

func render(t *testing.T, d report.ReportData) string {
	t.Helper()
	var buf bytes.Buffer
	if err := report.Report(&buf, d); err != nil {
		t.Fatalf("Report: %v", err)
	}
	return buf.String()
}

func TestReportShowsCacheHitRate(t *testing.T) {
	out := render(t, withUsage(buildReportData()))
	if !strings.Contains(out, "Cache hit rate") {
		t.Fatalf("report has no cache hit rate line:\n%s", out)
	}
	line := lineContaining(out, "Cache hit rate")
	if !strings.Contains(line, "80%") {
		t.Errorf("cache hit rate line should say 80%%: %q", line)
	}
	// On API billing the share is the last column, under "share".
	header := lineContaining(out, "list price")
	if idx := strings.LastIndex(line, "80%") + 3; idx != len(strings.TrimRight(header, " ")) {
		t.Errorf("hit rate should end under the share column:\nheader %q\nline   %q", header, line)
	}
}

func TestReportCacheHitRateOnASubscription(t *testing.T) {
	d := withUsage(buildReportData())
	d.Plan = config.PlanSubscription
	out := render(t, d)
	line := lineContaining(out, "Cache hit rate")
	if !strings.Contains(line, "80%") {
		t.Errorf("subscription layout should still show 80%%: %q", line)
	}
	// The share column comes first on a subscription, so the value sits right
	// after the label with no empty money column trailing it.
	if strings.TrimRight(line, " ") != strings.TrimRight(strings.SplitN(line, "%", 2)[0]+"%", " ") {
		t.Errorf("subscription hit rate line has trailing content: %q", line)
	}
}

func TestReportCacheHitRateWithNothingSentIsZero(t *testing.T) {
	out := render(t, buildReportData()) // fixture has no Usage at all
	if line := lineContaining(out, "Cache hit rate"); !strings.Contains(line, "0%") {
		t.Errorf("no usage should render as 0%%: %q", line)
	}
}

func priorFor(d report.ReportData) *report.Comparison {
	prior, _ := ledger.Prior(d.Window)
	return &report.Comparison{
		Window:    prior,
		Sessions:  100,
		Subagents: 62,
		Totals: ledger.Totals{
			USD: 317.54, MainUSD: 200.00, SubagentUSD: 117.54,
			Usage: model.Usage{Input: 300, CacheRead: 700},
		},
	}
}

func TestReportComparisonBlock(t *testing.T) {
	d := withUsage(buildReportData())
	d.Compare = priorFor(d)
	out := render(t, d)

	for _, want := range []string{
		"Compared with the 30 days before (Jul 12 – Aug 11)",
		"Total", "up 30%", "$317.54 before, $412.80 now",
		"Main session turns", "up 36%", "$200.00 before, $271.10 now",
		"Sub-agents", "up 21%", "$117.54 before, $141.70 now",
		"Sessions", "up 93%", "100 before, 193 now",
		"Sub-agent runs", "no change", "62 before, 62 now",
		"Cache hit rate", "up 10 points", "70% before, 80% now",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("comparison block missing %q:\n%s", want, out)
		}
	}
	// The block sits between the totals table and the findings.
	if strings.Index(out, "Compared with") > strings.Index(out, "Top findings") {
		t.Errorf("comparison should come before the findings:\n%s", out)
	}
	for _, line := range strings.Split(out, "\n") {
		if len(line) > 100 {
			t.Errorf("line exceeds 100 columns: %q", line)
		}
	}
}

func TestReportComparisonSaysDownWhenSpendFell(t *testing.T) {
	d := withUsage(buildReportData())
	c := priorFor(d)
	c.Totals.USD, c.Totals.MainUSD = 825.60, 542.20 // exactly double this window
	d.Compare = c
	out := render(t, d)
	block := out[strings.Index(out, "Compared with"):]
	if line := lineContaining(block, "  Total"); !strings.Contains(line, "down 50%") {
		t.Errorf("total line should say down 50%%: %q", line)
	}
}

func TestReportComparisonWithAnEmptyPriorWindow(t *testing.T) {
	d := withUsage(buildReportData())
	prior, _ := ledger.Prior(d.Window)
	d.Compare = &report.Comparison{Window: prior}
	out := render(t, d)
	if !strings.Contains(out, "Nothing was recorded in the 30 days before (Jul 12 – Aug 11), so there is nothing to compare with.") {
		t.Errorf("empty prior window should say so plainly:\n%s", out)
	}
	if strings.Contains(out, "before, ") {
		t.Errorf("empty prior window must not print figure lines:\n%s", out)
	}
}

func TestReportWithoutCompareHasNoBlock(t *testing.T) {
	out := render(t, withUsage(buildReportData()))
	if strings.Contains(out, "Compared with") || strings.Contains(out, "nothing to compare") {
		t.Errorf("no comparison was asked for:\n%s", out)
	}
}

func TestReportJSONCarriesHitRateAndComparison(t *testing.T) {
	d := withUsage(buildReportData())
	d.Compare = priorFor(d)
	var buf bytes.Buffer
	if err := report.ReportJSON(&buf, d); err != nil {
		t.Fatalf("ReportJSON: %v", err)
	}
	var doc struct {
		CacheHitRate float64 `json:"cacheHitRate"`
		Compare      *struct {
			Window struct {
				Since string  `json:"since"`
				Until string  `json:"until"`
				Days  float64 `json:"days"`
				Label string  `json:"label"`
			} `json:"window"`
			Sessions     int     `json:"sessions"`
			Subagents    int     `json:"subagents"`
			USD          float64 `json:"usd"`
			MainUSD      float64 `json:"mainUSD"`
			SubagentUSD  float64 `json:"subagentUSD"`
			CacheHitRate float64 `json:"cacheHitRate"`
		} `json:"compare"`
	}
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatalf("parse: %v\n%s", err, buf.String())
	}
	if doc.CacheHitRate != 0.8 {
		t.Errorf("cacheHitRate = %v, want 0.8", doc.CacheHitRate)
	}
	if doc.Compare == nil {
		t.Fatalf("compare object missing:\n%s", buf.String())
	}
	if doc.Compare.USD != 317.54 || doc.Compare.Sessions != 100 || doc.Compare.Subagents != 62 {
		t.Errorf("compare figures wrong: %+v", doc.Compare)
	}
	if doc.Compare.CacheHitRate != 0.7 {
		t.Errorf("compare.cacheHitRate = %v, want 0.7", doc.Compare.CacheHitRate)
	}
	if doc.Compare.Window.Since != "2026-07-12" || doc.Compare.Window.Until != "2026-08-11" || doc.Compare.Window.Days != 30 {
		t.Errorf("compare window wrong: %+v", doc.Compare.Window)
	}
	if doc.Compare.Window.Label != "Jul 12 – Aug 11" {
		t.Errorf("compare label = %q", doc.Compare.Window.Label)
	}
}

func TestReportJSONOmitsComparisonWhenNotAsked(t *testing.T) {
	var buf bytes.Buffer
	if err := report.ReportJSON(&buf, withUsage(buildReportData())); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), `"compare"`) {
		t.Errorf("compare should be omitted when nil:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), `"cacheHitRate": 0.8`) {
		t.Errorf("cacheHitRate should always be present:\n%s", buf.String())
	}
}

func lineContaining(out, needle string) string {
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, needle) {
			return line
		}
	}
	return ""
}

// Keep the compiler honest about the fixture's window: the labels above
// assume it ends on 10 September.
var _ = time.September
