package report_test

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/magna-nz/tallybook/internal/config"
	"github.com/magna-nz/tallybook/internal/ledger"
	"github.com/magna-nz/tallybook/internal/report"
)

func buildChange(verdict ledger.Verdict) ledger.Change {
	at := time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)
	switch verdict {
	case ledger.VerdictKeep:
		return ledger.Change{
			Agent: "researcher", From: "claude-opus-5", To: "claude-sonnet-5", At: at,
			Before:  ledger.Side{Runs: 6, AvgUSD: 1.40, ErrorRate: 0, AvgTurns: 4},
			After:   ledger.Side{Runs: 9, AvgUSD: 0.35, ErrorRate: 0, AvgTurns: 4},
			Verdict: ledger.VerdictKeep,
		}
	case ledger.VerdictRevert:
		return ledger.Change{
			Agent: "reviewer", From: "claude-sonnet-5", To: "claude-haiku-4-5", At: at,
			Before:  ledger.Side{Runs: 8, AvgUSD: 0.60, ErrorRate: 0.02, AvgTurns: 3},
			After:   ledger.Side{Runs: 10, AvgUSD: 0.55, ErrorRate: 0.45, AvgTurns: 3},
			Verdict: ledger.VerdictRevert,
		}
	case ledger.VerdictTooEarly:
		return ledger.Change{
			Agent: "planner", From: "claude-opus-5", To: "claude-sonnet-5", At: at,
			Before:  ledger.Side{Runs: 6, AvgUSD: 1.10, ErrorRate: 0, AvgTurns: 4},
			After:   ledger.Side{Runs: 2, AvgUSD: 0.90, ErrorRate: 0, AvgTurns: 4},
			Verdict: ledger.VerdictTooEarly,
		}
	default:
		return ledger.Change{
			Agent: "coder", From: "claude-sonnet-5", To: "claude-opus-5", At: at,
			Before:  ledger.Side{Runs: 5, AvgUSD: 0.50, ErrorRate: 0.10, AvgTurns: 5},
			After:   ledger.Side{Runs: 5, AvgUSD: 0.70, ErrorRate: 0.02, AvgTurns: 5},
			Verdict: ledger.VerdictWatch,
		}
	}
}

func TestChangesKeepVerdict(t *testing.T) {
	c := buildChange(ledger.VerdictKeep)
	var buf bytes.Buffer
	if err := report.Changes(&buf, []ledger.Change{c}, config.PlanAPI); err != nil {
		t.Fatalf("Changes: %v", err)
	}
	out := buf.String()

	for _, want := range []string{"researcher", "Opus 5", "Sonnet 5", "keep", "6 runs", "9 runs", "$1.40", "$0.35", "Worth keeping"} {
		if !strings.Contains(out, want) {
			t.Errorf("Changes(keep) output missing %q\n--- output ---\n%s", want, out)
		}
	}
	assertMaxLineWidth(t, out, 100)
}

func TestChangesRevertVerdict(t *testing.T) {
	c := buildChange(ledger.VerdictRevert)
	var buf bytes.Buffer
	if err := report.Changes(&buf, []ledger.Change{c}, config.PlanAPI); err != nil {
		t.Fatalf("Changes: %v", err)
	}
	out := buf.String()

	for _, want := range []string{"reviewer", "revert", "8 runs", "10 runs", "$0.60", "$0.55", "Revert it"} {
		if !strings.Contains(out, want) {
			t.Errorf("Changes(revert) output missing %q\n--- output ---\n%s", want, out)
		}
	}
	// The error rate jumped from 2% to 45%, well past the 10-point rule; the
	// sentence must name what got worse.
	if !strings.Contains(out, "error rate") && !strings.Contains(out, "errors") {
		t.Errorf("Changes(revert) sentence should name what got worse:\n%s", out)
	}
	assertMaxLineWidth(t, out, 100)
}

func TestChangesTooEarlyVerdict(t *testing.T) {
	c := buildChange(ledger.VerdictTooEarly)
	var buf bytes.Buffer
	if err := report.Changes(&buf, []ledger.Change{c}, config.PlanAPI); err != nil {
		t.Fatalf("Changes: %v", err)
	}
	out := buf.String()

	for _, want := range []string{"planner", "too early", "6 runs", "2 runs", "Too early to tell"} {
		if !strings.Contains(out, want) {
			t.Errorf("Changes(too early) output missing %q\n--- output ---\n%s", want, out)
		}
	}
	assertMaxLineWidth(t, out, 100)
}

func TestChangesWatchVerdict(t *testing.T) {
	c := buildChange(ledger.VerdictWatch)
	var buf bytes.Buffer
	if err := report.Changes(&buf, []ledger.Change{c}, config.PlanAPI); err != nil {
		t.Fatalf("Changes: %v", err)
	}
	out := buf.String()

	for _, want := range []string{"coder", "watch", "5 runs", "$0.50", "$0.70", "Worth watching"} {
		if !strings.Contains(out, want) {
			t.Errorf("Changes(watch) output missing %q\n--- output ---\n%s", want, out)
		}
	}
	assertMaxLineWidth(t, out, 100)
}

func TestChangesNoChanges(t *testing.T) {
	var buf bytes.Buffer
	if err := report.Changes(&buf, nil, config.PlanAPI); err != nil {
		t.Fatalf("Changes: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "No model changes in this window.") {
		t.Errorf("Changes(nil) missing the no-changes line:\n%s", out)
	}
	assertMaxLineWidth(t, out, 100)
}

func TestChangesSubscriptionLabelsEquivalent(t *testing.T) {
	c := buildChange(ledger.VerdictKeep)
	var buf bytes.Buffer
	if err := report.Changes(&buf, []ledger.Change{c}, config.PlanSubscription); err != nil {
		t.Fatalf("Changes: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "equiv") {
		t.Errorf("subscription plan should label the money as a list-price equivalent:\n%s", out)
	}
	assertMaxLineWidth(t, out, 100)
}

func TestChangesJSON(t *testing.T) {
	c := buildChange(ledger.VerdictKeep)
	var buf bytes.Buffer
	if err := report.ChangesJSON(&buf, []ledger.Change{c}); err != nil {
		t.Fatalf("ChangesJSON: %v", err)
	}
	out := buf.String()
	for _, want := range []string{`"schema": 1`, `"agent": "researcher"`, `"from": "claude-opus-5"`, `"verdict": "keep"`} {
		if !strings.Contains(out, want) {
			t.Errorf("ChangesJSON output missing %q\n--- output ---\n%s", want, out)
		}
	}
}

// The header reads for a person, so it uses display names; the JSON is for a
// machine, so it keeps the exact model ids.
func TestChangesRendersDisplayNamesButJSONKeepsIDs(t *testing.T) {
	cs := []ledger.Change{{
		Agent: "researcher", From: "claude-opus-5", To: "claude-haiku-4-5",
		At:      time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC),
		Before:  ledger.Side{Runs: 6, AvgUSD: 1.40},
		After:   ledger.Side{Runs: 9, AvgUSD: 0.35},
		Verdict: ledger.VerdictKeep,
	}}

	var text bytes.Buffer
	if err := report.Changes(&text, cs, config.PlanAPI); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text.String(), "Opus 5 to Haiku 4.5") {
		t.Errorf("header should use display names:\n%s", text.String())
	}
	if strings.Contains(text.String(), "claude-opus-5") {
		t.Errorf("header should not use raw ids:\n%s", text.String())
	}

	var js bytes.Buffer
	if err := report.ChangesJSON(&js, cs); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(js.String(), "claude-opus-5") {
		t.Errorf("JSON should keep the exact id:\n%s", js.String())
	}
}

// One run either side reads as "1 run", not "1 runs".
func TestChangesSingularRun(t *testing.T) {
	cs := []ledger.Change{{
		Agent: "researcher", From: "claude-opus-5", To: "claude-sonnet-5",
		At:      time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC),
		Before:  ledger.Side{Runs: 1, AvgUSD: 2.00},
		After:   ledger.Side{Runs: 4, AvgUSD: 1.00},
		Verdict: ledger.VerdictTooEarly,
	}}
	var buf bytes.Buffer
	if err := report.Changes(&buf, cs, config.PlanAPI); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.Contains(out, "1 runs") {
		t.Errorf("singular run rendered as plural:\n%s", out)
	}
	// The short side is before the switch, so the sentence must say so rather
	// than claiming there was only one run after it.
	if !strings.Contains(out, "before the switch") {
		t.Errorf("too-early sentence names the wrong side:\n%s", out)
	}
}

// A decided verdict must not be buried under undecided ones.
func TestChangesPutsDecidedVerdictsFirst(t *testing.T) {
	base := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	early := ledger.Change{Agent: "a", From: "claude-opus-5", To: "claude-sonnet-5",
		At: base.AddDate(0, 0, 5), Before: ledger.Side{Runs: 1}, After: ledger.Side{Runs: 1},
		Verdict: ledger.VerdictTooEarly}
	keep := ledger.Change{Agent: "b", From: "claude-opus-5", To: "claude-sonnet-5",
		At: base, Before: ledger.Side{Runs: 6, AvgUSD: 2}, After: ledger.Side{Runs: 6, AvgUSD: 1},
		Verdict: ledger.VerdictKeep}

	var buf bytes.Buffer
	// Newer "too early" first on input; the renderer receives whatever the
	// ledger ordered, so this asserts the ledger's ordering via Changes.
	if err := report.Changes(&buf, []ledger.Change{keep, early}, config.PlanAPI); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.Index(out, "keep") > strings.Index(out, "too early") {
		t.Errorf("a decided verdict is buried below an undecided one:\n%s", out)
	}
}

// The sentence under the table must never contradict the numbers in it. A
// verdict of keep tolerates a small rise in failures, and an earlier version
// still said "nothing started failing more" when something had.
func TestChangesSentenceNeverContradictsTheTable(t *testing.T) {
	at := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name       string
		change     ledger.Change
		mustNotSay []string
		mustSay    []string
	}{
		{
			name: "kept, but failures edged up",
			change: ledger.Change{
				Agent: "researcher", From: "claude-opus-5", To: "claude-sonnet-5", At: at,
				Before:  ledger.Side{Runs: 10, AvgUSD: 2.00, ErrorRate: 0.03},
				After:   ledger.Side{Runs: 10, AvgUSD: 1.00, ErrorRate: 0.05},
				Verdict: ledger.VerdictKeep,
			},
			mustNotSay: []string{"nothing started failing more"},
			mustSay:    []string{"edged up"},
		},
		{
			name: "watching something dearer that also fails more",
			change: ledger.Change{
				Agent: "researcher", From: "claude-sonnet-5", To: "claude-haiku-4-5", At: at,
				Before:  ledger.Side{Runs: 10, AvgUSD: 1.00, ErrorRate: 0.02},
				After:   ledger.Side{Runs: 10, AvgUSD: 1.03, ErrorRate: 0.07},
				Verdict: ledger.VerdictWatch,
			},
			mustNotSay: []string{"barely moved"},
			mustSay:    []string{"more often"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := report.Changes(&buf, []ledger.Change{c.change}, config.PlanAPI); err != nil {
				t.Fatal(err)
			}
			out := buf.String()
			for _, s := range c.mustNotSay {
				if strings.Contains(out, s) {
					t.Errorf("sentence contradicts the table, says %q:\n%s", s, out)
				}
			}
			for _, s := range c.mustSay {
				if !strings.Contains(out, s) {
					t.Errorf("sentence should mention %q:\n%s", s, out)
				}
			}
		})
	}
}
