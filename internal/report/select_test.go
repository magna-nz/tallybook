package report_test

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/magna-nz/tallybook/internal/findings"
	"github.com/magna-nz/tallybook/internal/report"
)

// priced builds a saving finding worth usd a month.
func priced(title string, usd float64, dir findings.Direction) findings.Finding {
	return findings.Finding{
		Title:        title,
		SavingUSD:    usd,
		SavingShare:  usd / 400,
		Confidence:   findings.Medium,
		Direction:    dir,
		WhatHappened: "Something in this window cost more than it had to. A second sentence that is not shown.",
	}
}

// note builds an Info finding, which never carries a saving.
func note(title string, dir findings.Direction) findings.Finding {
	return findings.Finding{
		Title:        title,
		Confidence:   findings.Info,
		Direction:    dir,
		WhatHappened: "Worth knowing about, with nothing to save. A second sentence that is not shown.",
	}
}

// thirteen is a full-strength list in the order findings.Run returns:
// priced findings by descending saving, then the Info findings.
func thirteen() []findings.Finding {
	return []findings.Finding{
		priced("Read-only sub-agents ran on Opus", 58, findings.Downgrade),
		priced("Thinking was spent on turns that did no thinking", 24, findings.Effort),
		priced("The prompt cache expired between turns", 12, findings.Cache),
		priced("Tool output fills most of the context", 9, findings.Context),
		priced("You asked for a cheaper model but got Opus", 6, findings.Config),
		priced("The same file was read eleven times", 4, findings.Context),
		priced("A long session never compacted", 2.5, findings.Context),
		priced("Two agents share one oversized brief", 1.25, findings.Context),
		priced("A cache write was paid for twice", 0.60, findings.Cache),
		priced("A retry loop repeated one command", 0.20, findings.Config),
		note("3 sessions look under-powered", findings.Upgrade),
		note("One agent has no model pinned", findings.Config),
		note("Codex sessions are not priced by the hour", findings.Config),
	}
}

func TestSelectTopKeepsOrderAndNumbers(t *testing.T) {
	top := report.SelectTop(thirteen(), 1.0, 5)

	if len(top.Priced) != 5 {
		t.Fatalf("priced = %d, want 5", len(top.Priced))
	}
	wantN := []int{1, 2, 3, 4, 5}
	for i, nf := range top.Priced {
		if nf.N != wantN[i] {
			t.Errorf("priced[%d].N = %d, want %d", i, nf.N, wantN[i])
		}
	}
	if got := top.Priced[0].Finding.Title; got != "Read-only sub-agents ran on Opus" {
		t.Errorf("first priced title = %q", got)
	}

	// Five priced listed, five held back (two of them under the $1 floor).
	if top.HiddenPriced != 5 {
		t.Errorf("hiddenPriced = %d, want 5", top.HiddenPriced)
	}
	if len(top.Info) != 2 {
		t.Errorf("info = %d, want 2", len(top.Info))
	}
	if top.Info[0].N != 11 || top.Info[1].N != 12 {
		t.Errorf("info numbers = %d, %d, want 11, 12", top.Info[0].N, top.Info[1].N)
	}
	if top.HiddenInfo != 1 {
		t.Errorf("hiddenInfo = %d, want 1", top.HiddenInfo)
	}
	if top.Hidden() != 6 {
		t.Errorf("Hidden() = %d, want 6", top.Hidden())
	}
}

func TestSelectTopMinSavingHidesSmallFindings(t *testing.T) {
	all := []findings.Finding{
		priced("Worth doing", 20, findings.Downgrade),
		priced("Worth 50 cents", 0.50, findings.Cache),
		priced("Worth exactly the floor", 1.0, findings.Cache),
	}
	top := report.SelectTop(all, 1.0, 5)

	if len(top.Priced) != 2 {
		t.Fatalf("priced = %d, want 2 (the 50c finding is under the floor)", len(top.Priced))
	}
	// The number is the position in the full list, so the printed numbers skip.
	if top.Priced[0].N != 1 || top.Priced[1].N != 3 {
		t.Errorf("priced numbers = %d, %d, want 1, 3", top.Priced[0].N, top.Priced[1].N)
	}
	if top.HiddenPriced != 1 {
		t.Errorf("hiddenPriced = %d, want 1", top.HiddenPriced)
	}
}

func TestSelectTopZeroThresholdsUseDefaults(t *testing.T) {
	top := report.SelectTop(thirteen(), 0, 0)
	if len(top.Priced) != 5 || len(top.Info) != 2 {
		t.Errorf("priced/info = %d/%d with zero thresholds, want 5/2", len(top.Priced), len(top.Info))
	}
}

func TestSelectTopNothingHidden(t *testing.T) {
	all := []findings.Finding{
		priced("Worth doing", 20, findings.Downgrade),
		note("Worth knowing", findings.Upgrade),
	}
	top := report.SelectTop(all, 1.0, 5)
	if top.Hidden() != 0 {
		t.Errorf("Hidden() = %d, want 0", top.Hidden())
	}
}

func TestSelectTopEmpty(t *testing.T) {
	top := report.SelectTop(nil, 1.0, 5)
	if len(top.Priced) != 0 || len(top.Info) != 0 || top.Hidden() != 0 {
		t.Errorf("SelectTop(nil) = %+v, want everything empty", top)
	}
}

// TestReportTopSectionWithThirteenFindings renders the real report over a
// thirteen-finding list and checks the shape of the section: five priced
// rows, two notes, and one line pointing at `tallybook findings`.
func TestReportTopSectionWithThirteenFindings(t *testing.T) {
	d := buildReportData()
	d.Findings = thirteen()

	var buf bytes.Buffer
	if err := report.Report(&buf, d); err != nil {
		t.Fatalf("Report: %v", err)
	}
	out := buf.String()

	for _, want := range []string{
		"Top findings (estimated saving / month)",
		"Also worth knowing",
		"11. 3 sessions look under-powered",
		"5 more findings and 1 more note. Run `tallybook findings` to see them all.",
		"Run `tallybook finding <n>` for evidence and the change to make.",
		"they overlap, so they do not add up.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report missing %q\n--- output ---\n%s", want, out)
		}
	}
	if strings.Contains(out, "A retry loop repeated one command") {
		t.Errorf("report listed a finding under the floor:\n%s", out)
	}
	if strings.Contains(out, "Codex sessions are not priced by the hour") {
		t.Errorf("report listed a third note:\n%s", out)
	}
	assertMaxLineWidth(t, out, 96)

	t.Log("\n" + findingsSection(out))
}

func TestReportMoreLineSingular(t *testing.T) {
	d := buildReportData()
	d.Findings = []findings.Finding{
		priced("Worth doing", 20, findings.Downgrade),
		priced("Worth 50 cents", 0.50, findings.Cache),
	}

	var buf bytes.Buffer
	if err := report.Report(&buf, d); err != nil {
		t.Fatalf("Report: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "1 more finding. Run `tallybook findings` to see them all.") {
		t.Errorf("report missing the singular more-findings line:\n%s", out)
	}
	// Only one priced finding printed, so the overlap caveat does not apply.
	if strings.Contains(out, "they overlap") {
		t.Errorf("report printed the overlap note for a single finding:\n%s", out)
	}
}

func TestReportOmitsMoreLineWhenNothingHidden(t *testing.T) {
	var buf bytes.Buffer
	if err := report.Report(&buf, buildReportData()); err != nil {
		t.Fatalf("Report: %v", err)
	}
	if out := buf.String(); strings.Contains(out, "tallybook findings") {
		t.Errorf("report pointed at `tallybook findings` with nothing hidden:\n%s", out)
	}
}

func TestReportAllFindingsUnderTheFloor(t *testing.T) {
	d := buildReportData()
	d.Findings = []findings.Finding{
		priced("Worth 50 cents", 0.50, findings.Cache),
		priced("Worth 20 cents", 0.20, findings.Cache),
	}

	var buf bytes.Buffer
	if err := report.Report(&buf, d); err != nil {
		t.Fatalf("Report: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Nothing in this window is worth more than $1.00 a month to change.") {
		t.Errorf("report missing the nothing-worth-changing line:\n%s", out)
	}
	if !strings.Contains(out, "2 more findings. Run `tallybook findings` to see them all.") {
		t.Errorf("report missing the more-findings line:\n%s", out)
	}
}

func TestReportLimitIsHonoured(t *testing.T) {
	d := buildReportData()
	d.Findings = thirteen()
	d.ReportLimit = 2

	var buf bytes.Buffer
	if err := report.Report(&buf, d); err != nil {
		t.Fatalf("Report: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "The prompt cache expired between turns") {
		t.Errorf("report ignored ReportLimit = 2:\n%s", out)
	}
	if !strings.Contains(out, "8 more findings and 1 more note.") {
		t.Errorf("report miscounted what it held back:\n%s", out)
	}
}

// TestReportJSONKeepsEveryFinding pins the promise that --json is not
// filtered by the two report thresholds.
func TestReportJSONKeepsEveryFinding(t *testing.T) {
	d := buildReportData()
	d.Findings = thirteen()

	var buf bytes.Buffer
	if err := report.ReportJSON(&buf, d); err != nil {
		t.Fatalf("ReportJSON: %v", err)
	}
	out := buf.String()
	for _, f := range d.Findings {
		if !strings.Contains(out, f.Title) {
			t.Errorf("report JSON dropped %q:\n%s", f.Title, out)
		}
	}
}

// findingsSection is the part of the report from the findings heading to the
// currency footer, for logging a real sample.
func findingsSection(out string) string {
	start := strings.Index(out, "Top findings")
	if start == -1 {
		start = strings.Index(out, "Nothing in this window")
	}
	if start == -1 {
		return out
	}
	rest := out[start:]
	if end := strings.Index(rest, "Prices are"); end != -1 {
		rest = rest[:end]
	}
	return strings.TrimRight(rest, "\n") + fmt.Sprintf("\n(section is %d lines)", strings.Count(rest, "\n"))
}
