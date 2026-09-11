package report_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/magna-nz/tallybook/internal/config"
	"github.com/magna-nz/tallybook/internal/findings"
	"github.com/magna-nz/tallybook/internal/report"
)

func TestFindingsGroupsByDirection(t *testing.T) {
	var buf bytes.Buffer
	err := report.Findings(&buf, report.FindingsData{Findings: thirteen(), Plan: config.PlanAPI})
	if err != nil {
		t.Fatalf("Findings: %v", err)
	}
	out := buf.String()

	// Every finding is listed, with no cap and no minimum.
	for _, f := range thirteen() {
		if !strings.Contains(out, f.Title) {
			t.Errorf("findings output dropped %q:\n%s", f.Title, out)
		}
	}

	wantOrder := []string{
		"Move work to a cheaper model",
		"Shrink what is sent every turn",
		"Keep the prompt cache warm",
		"Lower thinking effort",
		"A setting is not doing what you think",
		"Needs a stronger model or a better brief",
	}
	at := -1
	for _, heading := range wantOrder {
		idx := strings.Index(out, heading)
		if idx == -1 {
			t.Fatalf("findings output missing heading %q:\n%s", heading, out)
		}
		if idx < at {
			t.Errorf("heading %q is out of order:\n%s", heading, out)
		}
		at = idx
	}

	if !strings.Contains(out, "Run `tallybook finding <n>` for evidence and the change to make.") {
		t.Errorf("findings output missing the closing line:\n%s", out)
	}
	assertMaxLineWidth(t, out, 96)

	t.Log("\n" + strings.TrimRight(out, "\n"))
}

// TestFindingsKeepsGlobalNumbers checks that a line's number is its position
// in the full list, not its position within its group, so
// `tallybook finding <n>` still works from this listing.
func TestFindingsKeepsGlobalNumbers(t *testing.T) {
	var buf bytes.Buffer
	err := report.Findings(&buf, report.FindingsData{Findings: thirteen(), Plan: config.PlanAPI})
	if err != nil {
		t.Fatalf("Findings: %v", err)
	}

	want := map[string]string{
		"Read-only sub-agents ran on Opus":                 " 1.",
		"Thinking was spent on turns that did no thinking": " 2.",
		"The prompt cache expired between turns":           " 3.",
		"Tool output fills most of the context":            " 4.",
		"A retry loop repeated one command":                "10.",
		"3 sessions look under-powered":                    "11.",
		"Codex sessions are not priced by the hour":        "13.",
	}
	for _, line := range strings.Split(buf.String(), "\n") {
		for title, prefix := range want {
			if strings.Contains(line, title) && !strings.HasPrefix(line, prefix) {
				t.Errorf("line for %q should start with %q: %q", title, prefix, line)
			}
		}
	}
}

func TestFindingsSkipsEmptyGroups(t *testing.T) {
	only := []findings.Finding{priced("Read-only sub-agents ran on Opus", 58, findings.Downgrade)}

	var buf bytes.Buffer
	if err := report.Findings(&buf, report.FindingsData{Findings: only, Plan: config.PlanAPI}); err != nil {
		t.Fatalf("Findings: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Move work to a cheaper model") {
		t.Errorf("findings output missing the one group it has:\n%s", out)
	}
	for _, heading := range []string{"Keep the prompt cache warm", "Lower thinking effort", "Other"} {
		if strings.Contains(out, heading) {
			t.Errorf("findings output printed the empty group %q:\n%s", heading, out)
		}
	}
}

func TestFindingsInfoShowsNoSaving(t *testing.T) {
	only := []findings.Finding{note("3 sessions look under-powered", findings.Upgrade)}

	var buf bytes.Buffer
	if err := report.Findings(&buf, report.FindingsData{Findings: only, Plan: config.PlanAPI}); err != nil {
		t.Fatalf("Findings: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "$") {
		t.Errorf("an Info finding should carry no money figure:\n%s", out)
	}
	if strings.Contains(out, "confidence") || strings.Contains(out, "do not downgrade") {
		t.Errorf("findings list lines should carry no confidence label:\n%s", out)
	}
}

func TestFindingsEmpty(t *testing.T) {
	var buf bytes.Buffer
	if err := report.Findings(&buf, report.FindingsData{Plan: config.PlanAPI}); err != nil {
		t.Fatalf("Findings: %v", err)
	}
	if out := buf.String(); !strings.Contains(out, "No findings in this window.") {
		t.Errorf("findings output missing the no-findings line:\n%s", out)
	}
}

func TestFindingsJSONKeepsEveryFinding(t *testing.T) {
	var buf bytes.Buffer
	if err := report.FindingsJSON(&buf, thirteen()); err != nil {
		t.Fatalf("FindingsJSON: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, `"schema": 1`) {
		t.Errorf("findings JSON missing the schema field:\n%s", out)
	}
	for _, f := range thirteen() {
		if !strings.Contains(out, f.Title) {
			t.Errorf("findings JSON dropped %q:\n%s", f.Title, out)
		}
	}
	// The shape is the report's own finding array, not a new one.
	for _, key := range []string{`"savingUSD"`, `"share"`, `"confidence"`, `"direction"`} {
		if !strings.Contains(out, key) {
			t.Errorf("findings JSON missing %s:\n%s", key, out)
		}
	}
}
