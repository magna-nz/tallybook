package report_test

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/magna-nz/tallybook/internal/config"
	"github.com/magna-nz/tallybook/internal/findings"
	"github.com/magna-nz/tallybook/internal/ledger"
	"github.com/magna-nz/tallybook/internal/report"
)

func assertMaxLineWidth(t *testing.T, out string, max int) {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if len(line) > max {
			t.Errorf("line exceeds %d columns (%d): %q", max, len(line), line)
		}
	}
}

func buildReportData() report.ReportData {
	earliest := time.Date(2026, 6, 12, 9, 0, 0, 0, time.UTC)
	latest := time.Date(2026, 9, 10, 17, 0, 0, 0, time.UTC)
	window, _ := ledger.ParseSince("30d", latest)

	return report.ReportData{
		SinceFlag: "30d",
		Window:    window,
		Earliest:  earliest,
		Latest:    latest,
		Sessions:  193,
		Subagents: 62,
		Totals: ledger.Totals{
			USD:         412.80,
			MainUSD:     271.10,
			SubagentUSD: 141.70,
			Sessions:    193,
			Subagents:   62,
		},
		Findings: []findings.Finding{
			{
				Title:        "Read-only sub-agents ran on Opus",
				SavingUSD:    58,
				SavingShare:  0.14,
				Confidence:   findings.High,
				Direction:    findings.Downgrade,
				WhatHappened: "41 runs of researcher and Explore used only Read/Grep/Glob. It never edited anything.",
			},
			{
				Title:        "3 sessions look under-powered",
				SavingUSD:    0,
				Confidence:   findings.Info,
				Direction:    findings.Upgrade,
				WhatHappened: "3 sessions retried the same failing command more than the threshold allows.",
			},
		},
		Plan:        config.PlanAPI,
		PricesDated: "2026-09-10",
	}
}

func TestReportContainsKeyLines(t *testing.T) {
	var buf bytes.Buffer
	if err := report.Report(&buf, buildReportData()); err != nil {
		t.Fatalf("Report: %v", err)
	}
	out := buf.String()

	wantSubstrings := []string{
		"Scanned 193 sessions, 62 sub-agent runs (Jun 12 – Sep 10)",
		"Last 30 days",
		"list price",
		"share",
		"$412.80",
		"100%",
		"$271.10",
		"$141.70",
		"Top findings (estimated saving / month)",
		"Read-only sub-agents ran on Opus",
		"Run `tallybook finding <n>` for evidence and the change to make.",
		"Prices are Anthropic and OpenAI list prices, verified 2026-09-10.",
	}
	// The confidence label was dropped from the list lines: it read as noise
	// next to the money, and the finding's own text says how strong it is.
	if strings.Contains(out, "confidence") {
		t.Errorf("Report list lines should not carry a confidence label:\n%s", out)
	}
	for _, want := range wantSubstrings {
		if !strings.Contains(out, want) {
			t.Errorf("Report output missing %q\n--- output ---\n%s", want, out)
		}
	}

	assertMaxLineWidth(t, out, 100)
}

func TestReportNoFindings(t *testing.T) {
	d := buildReportData()
	d.Findings = nil

	var buf bytes.Buffer
	if err := report.Report(&buf, d); err != nil {
		t.Fatalf("Report: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "No findings in this window. Nothing stood out as overpaid.") {
		t.Errorf("Report with no findings missing the no-findings line:\n%s", out)
	}
	assertMaxLineWidth(t, out, 100)
}

func TestReportSkippedFiles(t *testing.T) {
	d := buildReportData()
	d.SkippedFiles = 3

	var buf bytes.Buffer
	if err := report.Report(&buf, d); err != nil {
		t.Fatalf("Report: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Skipped 3 files that could not be read (tallybook status shows them).") {
		t.Errorf("Report missing skipped-files line:\n%s", out)
	}
}

func TestReportSubscriptionMode(t *testing.T) {
	d := buildReportData()
	d.Plan = config.PlanSubscription

	var buf bytes.Buffer
	if err := report.Report(&buf, d); err != nil {
		t.Fatalf("Report: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "list-price equiv.") {
		t.Errorf("subscription report missing list-price equiv. header:\n%s", out)
	}
	if !strings.Contains(out, "You are on a subscription") {
		t.Errorf("subscription report missing subscription closing line:\n%s", out)
	}
	assertMaxLineWidth(t, out, 100)
}

func buildFinding() findings.Finding {
	return findings.Finding{
		ID:          "readonly-subagent-opus",
		Title:       "Read-only sub-agents ran on Opus",
		SavingUSD:   58,
		SavingShare: 0.14,
		Confidence:  findings.High,
		Direction:   findings.Downgrade,

		WhatHappened: "41 times you launched a researcher or Explore agent and it only read files and " +
			"searched. It never edited anything or ran a command that changed the project.",
		WhyItCosts: "Opus is billed at about two and a half times the rate of Sonnet for the same tokens. " +
			"Reading and summarising files is work the cheaper models do about as well.",
		WhatToChange: "Open .claude/agents/researcher.md and add this line to the block at the top of the file:\n\n" +
			"    model: sonnet",
		WhatToExpect: "Researcher runs should cost about 40% of what they do now.",

		Patch: "--- a/.claude/agents/researcher.md\n+++ b/.claude/agents/researcher.md\n@@\n+model: sonnet\n",
		Evidence: findings.Table{
			Columns: []string{"run", "model", "cost"},
			Rows: [][]string{
				{"1", "claude-opus-5", "$1.20"},
				{"2", "claude-opus-5", "$0.98"},
			},
		},
	}
}

func TestFindingContainsKeyLines(t *testing.T) {
	f := buildFinding()
	mode := report.FindingMode{Plan: config.PlanAPI}

	var buf bytes.Buffer
	if err := report.Finding(&buf, f, 1, mode); err != nil {
		t.Fatalf("Finding: %v", err)
	}
	out := buf.String()

	wantSubstrings := []string{
		"Read-only sub-agents ran on Opus",
		"saves about $58.00/month",
		"What happened",
		"Why it costs money",
		"What to change",
		"What to expect",
		"model: sonnet",
	}
	for _, want := range wantSubstrings {
		if !strings.Contains(out, want) {
			t.Errorf("Finding output missing %q\n--- output ---\n%s", want, out)
		}
	}
	assertMaxLineWidth(t, out, 100)
}

func TestFindingWithEvidence(t *testing.T) {
	f := buildFinding()
	mode := report.FindingMode{Plan: config.PlanAPI, Evidence: true}

	var buf bytes.Buffer
	if err := report.Finding(&buf, f, 1, mode); err != nil {
		t.Fatalf("Finding: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "claude-opus-5") {
		t.Errorf("Finding with evidence missing evidence rows:\n%s", out)
	}
	assertMaxLineWidth(t, out, 100)
}

func TestFindingUpgradeUsesWhyItMatters(t *testing.T) {
	f := buildFinding()
	f.Direction = findings.Upgrade
	f.Confidence = findings.Info

	var buf bytes.Buffer
	if err := report.Finding(&buf, f, 1, report.FindingMode{Plan: config.PlanAPI}); err != nil {
		t.Fatalf("Finding: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Why it matters") {
		t.Errorf("Upgrade finding should use 'Why it matters' heading:\n%s", out)
	}
	if !strings.Contains(out, "do not downgrade") {
		t.Errorf("Info+Upgrade finding should say 'do not downgrade':\n%s", out)
	}
}
