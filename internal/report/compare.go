package report

import (
	"fmt"
	"io"
	"math"

	"github.com/magna-nz/tallybook/internal/ledger"
)

// Comparison is the window before the report's own, for `--compare`. The
// report says how this window moved against it: a trend rather than a
// snapshot, which is the same question `tallybook changes` asks of one
// sub-agent at a time.
type Comparison struct {
	Window    ledger.Window
	Totals    ledger.Totals
	Sessions  int // main sessions in the prior window
	Subagents int // sub-agent runs in the prior window
}

// writeComparison writes the window-on-window block. Every line names the
// direction first, because that is what the reader came for, and then the
// two figures it was worked out from.
func writeComparison(w io.Writer, d ReportData) {
	c := d.Compare
	if c == nil {
		return
	}
	days := fmtDays(c.Window.Days)
	if c.Sessions == 0 && c.Subagents == 0 {
		fmt.Fprintf(w, "Nothing was recorded in the %s before (%s), so there is nothing to compare with.\n",
			days, c.Window.Label)
		return
	}
	fmt.Fprintf(w, "Compared with the %s before (%s)\n", days, c.Window.Label)
	money := func(label string, before, after float64) {
		fmt.Fprintf(w, "  %-20s%-16s%s before, %s now\n",
			label, changePhrase(before, after), fmtUSD(before), fmtUSD(after))
	}
	count := func(label string, before, after int) {
		fmt.Fprintf(w, "  %-20s%-16s%d before, %d now\n",
			label, changePhrase(float64(before), float64(after)), before, after)
	}
	money("Total", c.Totals.USD, d.Totals.USD)
	money("Main session turns", c.Totals.MainUSD, d.Totals.MainUSD)
	money("Sub-agents", c.Totals.SubagentUSD, d.Totals.SubagentUSD)
	count("Sessions", c.Sessions, d.Sessions)
	count("Sub-agent runs", c.Subagents, d.Subagents)
	before, after := c.Totals.CacheHitRate(), d.Totals.CacheHitRate()
	fmt.Fprintf(w, "  %-20s%-16s%s before, %s now\n",
		"Cache hit rate", pointsPhrase(before, after), share(before), share(after))
}

// ChangePhrase is changePhrase for callers outside the package, so the MCP
// server says "up 30%" with the same rounding and the same "about the same"
// floor as the CLI.
func ChangePhrase(before, after float64) string { return changePhrase(before, after) }

// changePhrase says how a figure moved, as a percentage of where it started.
// It is deliberately coarse: a move under one percent reads as "about the
// same", because a report that says "up 0.3%" is inviting a decision that
// the number does not support.
func changePhrase(before, after float64) string {
	switch {
	case before == after:
		return "no change"
	case before == 0:
		return "new"
	case after == 0:
		return "down 100%"
	}
	pct := (after - before) / before * 100
	switch {
	case math.Abs(pct) < 1:
		return "about the same"
	case pct > 0:
		return fmt.Sprintf("up %s%%", fmtWhole(pct))
	default:
		return fmt.Sprintf("down %s%%", fmtWhole(-pct))
	}
}

// pointsPhrase says how a share moved, in percentage points, since a share
// compared against itself as a percentage of a percentage is not a number
// anyone reasons with.
func pointsPhrase(before, after float64) string {
	delta := (after - before) * 100
	if math.Abs(delta) < 1 {
		return "about the same"
	}
	word := "up"
	if delta < 0 {
		word, delta = "down", -delta
	}
	n := fmtWhole(delta)
	if n == "1" {
		return word + " 1 point"
	}
	return fmt.Sprintf("%s %s points", word, n)
}

// fmtWhole rounds to the nearest whole number for prose, grouping thousands
// so a ten-fold jump still reads as a number rather than a wall of digits.
func fmtWhole(v float64) string {
	digits := fmt.Sprintf("%d", int64(math.Round(v)))
	if len(digits) <= 3 {
		return digits
	}
	var out []byte
	for i, c := range []byte(digits) {
		if i > 0 && (len(digits)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, c)
	}
	return string(out)
}

// fmtDays renders a window length for prose: "30 days", "7 days", "1 day".
func fmtDays(days float64) string {
	n := int(math.Round(days))
	if n == 1 {
		return "1 day"
	}
	return fmt.Sprintf("%d days", n)
}
