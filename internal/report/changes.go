package report

import (
	"bufio"
	"fmt"
	"github.com/magna-nz/tallybook/internal/pricing"
	"io"

	"github.com/magna-nz/tallybook/internal/config"
	"github.com/magna-nz/tallybook/internal/ledger"
)

// changesErrorRiseRevert and changesErrorTolerance mirror the thresholds
// ledger.Changes uses to reach VerdictRevert, so the verdict sentence can
// name which rule fired without ledger.Change needing to carry its own
// working. Keep them in step with internal/ledger/changes.go.
const (
	changesErrorRiseRevert = 0.10
	changesErrorTolerance  = 0.02
	// changesDefaultMinRuns is the "too early" wait we tell the reader about.
	// ledger.Change does not carry the minRuns a caller passed to
	// ledger.Changes, so this assumes the documented default of 3.
	changesDefaultMinRuns = 3
)

// Changes writes one block per model change, newest first.
func Changes(w io.Writer, cs []ledger.Change, plan config.Plan) error {
	bw := bufio.NewWriter(w)

	if len(cs) == 0 {
		msg := "No model changes in this window. Change an agent's model and the next report " +
			"will tell you whether it helped."
		for _, line := range wrapText(msg, 76) {
			fmt.Fprintln(bw, line)
		}
		return bw.Flush()
	}

	costLabel := "each"
	if plan == config.PlanSubscription {
		costLabel = "each (" + moneyHeader(plan) + ")"
	}

	for i, c := range cs {
		if i > 0 {
			fmt.Fprintln(bw)
		}

		header := fmt.Sprintf("%s: %s to %s, %s", c.Agent, pricing.DisplayName(c.From), pricing.DisplayName(c.To), c.At.Format("2 Jan"))
		verdict := string(c.Verdict)
		pad := 76 - len(header) - len(verdict)
		if pad < 1 {
			pad = 1
		}
		fmt.Fprintf(bw, "%s%s%s\n\n", header, spaces(pad), verdict)

		fmt.Fprintf(bw, "  Before  %d run%s   %s %s   %s of tool calls failed\n",
			c.Before.Runs, plural(c.Before.Runs), fmtUSD(c.Before.AvgUSD), costLabel, share(c.Before.ErrorRate))
		fmt.Fprintf(bw, "  After   %d run%s   %s %s   %s of tool calls failed\n",
			c.After.Runs, plural(c.After.Runs), fmtUSD(c.After.AvgUSD), costLabel, share(c.After.ErrorRate))
		fmt.Fprintln(bw)

		for _, line := range wrapText(verdictSentence(c), 76) {
			fmt.Fprintln(bw, "  "+line)
		}
	}

	return bw.Flush()
}

// spaces returns a string of n spaces (n must be >= 0).
func spaces(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = ' '
	}
	return string(b)
}

// verdictSentence writes the one-sentence, plain-English verdict for a
// change, using the numbers on both sides.
func verdictSentence(c ledger.Change) string {
	switch c.Verdict {
	case ledger.VerdictTooEarly:
		return tooEarlySentence(c)
	case ledger.VerdictKeep:
		return keepSentence(c)
	case ledger.VerdictRevert:
		return revertSentence(c)
	default:
		return watchSentence(c)
	}
}

func tooEarlySentence(c ledger.Change) string {
	side, runs := "since the switch", c.After.Runs
	if c.Before.Runs < c.After.Runs {
		side, runs = "before the switch", c.Before.Runs
	}
	need := changesDefaultMinRuns - runs
	if need < 1 {
		need = 1
	}
	return fmt.Sprintf(
		"Too early to tell: only %d run%s %s. It needs about %d more before the comparison means anything.",
		runs, plural(runs), side, need)
}

// plural returns "" for one and "s" for anything else.
func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func keepSentence(c ledger.Change) string {
	drop := pctChange(c.Before.AvgUSD, c.After.AvgUSD)
	return fmt.Sprintf(
		"About %.0f%% cheaper per run, and nothing started failing more. Worth keeping.", drop)
}

func revertSentence(c ledger.Change) string {
	errDelta := (c.After.ErrorRate - c.Before.ErrorRate) * 100
	if errDelta > changesErrorRiseRevert*100 {
		return fmt.Sprintf(
			"The error rate jumped %.0f percentage points after the switch. Revert it.", errDelta)
	}
	rise := pctChange(c.After.AvgUSD, c.Before.AvgUSD)
	return fmt.Sprintf(
		"About %.0f%% more expensive per run, with no drop in errors to show for it. Revert it.", rise)
}

func watchSentence(c ledger.Change) string {
	costPct := pctChange(c.Before.AvgUSD, c.After.AvgUSD) // positive = cheaper
	errPts := (c.After.ErrorRate - c.Before.ErrorRate) * 100
	switch {
	case costPct > 0 && errPts > 0:
		return fmt.Sprintf(
			"Cheaper by about %.0f%%, but the error rate rose %.0f points too. Worth watching a bit longer before calling it a win.",
			costPct, errPts)
	case costPct < 0 && errPts < 0:
		return fmt.Sprintf(
			"More expensive by about %.0f%%, but errors dropped %.0f points. Worth watching whether the extra reliability is worth the price.",
			-costPct, -errPts)
	default:
		return "Cost and errors barely moved either way. Worth watching a bit longer before drawing a conclusion."
	}
}

// pctChange returns how much smaller after is than before, as a percentage
// (positive = after is cheaper/smaller). 0 if before is 0.
func pctChange(before, after float64) float64 {
	if before <= 0 {
		return 0
	}
	return (before - after) / before * 100
}

// ChangesJSON writes the model changes as JSON.
func ChangesJSON(w io.Writer, cs []ledger.Change) error {
	doc := changesJSONDoc{Schema: 1}
	for _, c := range cs {
		doc.Changes = append(doc.Changes, changeJSON{
			Agent:   c.Agent,
			From:    c.From,
			To:      c.To,
			At:      c.At.Format("2006-01-02T15:04:05Z07:00"),
			Before:  sideJSON(c.Before),
			After:   sideJSON(c.After),
			Verdict: string(c.Verdict),
		})
	}
	return writeJSON(w, doc)
}

// sideJSONDoc is the JSON shape of one ledger.Side.
type sideJSONDoc struct {
	Runs      int     `json:"runs"`
	AvgUSD    float64 `json:"avgUSD"`
	ErrorRate float64 `json:"errorRate"`
	AvgTurns  float64 `json:"avgTurns"`
}

func sideJSON(s ledger.Side) sideJSONDoc {
	return sideJSONDoc{Runs: s.Runs, AvgUSD: s.AvgUSD, ErrorRate: s.ErrorRate, AvgTurns: s.AvgTurns}
}

// changeJSON is the JSON shape of one ledger.Change.
type changeJSON struct {
	Agent   string      `json:"agent"`
	From    string      `json:"from"`
	To      string      `json:"to"`
	At      string      `json:"at"`
	Before  sideJSONDoc `json:"before"`
	After   sideJSONDoc `json:"after"`
	Verdict string      `json:"verdict"`
}

// changesJSONDoc is the document ChangesJSON writes.
type changesJSONDoc struct {
	Schema  int          `json:"schema"`
	Changes []changeJSON `json:"changes"`
}
