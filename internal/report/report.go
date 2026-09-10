package report

import (
	"bufio"
	"fmt"
	"github.com/magna-nz/tallybook/internal/model"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/magna-nz/tallybook/internal/config"
	"github.com/magna-nz/tallybook/internal/findings"
	"github.com/magna-nz/tallybook/internal/ledger"
)

// ReportData is everything the default report needs to render. Callers
// build it from ledger and findings results; report never queries the
// store itself.
type ReportData struct {
	SinceFlag string // the raw --since value, e.g. "30d", "all", "2026-08-01"
	Window    ledger.Window

	Earliest, Latest time.Time // span of the sessions actually scanned
	Sessions         int       // main (non-sub-agent) sessions matched
	Subagents        int       // sub-agent runs matched

	Totals   ledger.Totals
	Findings []findings.Finding

	Plan        config.Plan
	PricesDated string

	SkippedFiles int // ingest.Result.Failed; 0 when not ingested or nothing failed
}

// Report writes the default report: a scan summary, the window totals
// table, the top findings, and a currency footer.
func Report(w io.Writer, d ReportData) error {
	bw := bufio.NewWriter(w)

	fmt.Fprintf(bw, "Scanned %d sessions, %d sub-agent runs (%s)\n\n",
		d.Sessions, d.Subagents, dateRange(d.Earliest, d.Latest))

	title := windowTitle(d.SinceFlag, d.Window.Label)
	fmt.Fprintf(bw, "%-30s%18s%8s\n", title, moneyHeader(d.Plan), "share")

	var mainShare, subShare float64
	if d.Totals.USD > 0 {
		mainShare = d.Totals.MainUSD / d.Totals.USD
		subShare = d.Totals.SubagentUSD / d.Totals.USD
	}
	fmt.Fprintf(bw, "%-30s%18s%8s\n", "  Total", fmtUSD(d.Totals.USD), share(1))
	fmt.Fprintf(bw, "%-30s%18s%8s\n", "  Main session turns", fmtUSD(d.Totals.MainUSD), share(mainShare))
	fmt.Fprintf(bw, "%-30s%18s%8s\n", "  Sub-agents", fmtUSD(d.Totals.SubagentUSD), share(subShare))
	if len(d.Totals.BySource) > 1 {
		for _, src := range []model.Source{model.SourceClaudeCode, model.SourceCodex} {
			st, ok := d.Totals.BySource[src]
			if !ok {
				continue
			}
			var sh float64
			if d.Totals.USD > 0 {
				sh = st.USD / d.Totals.USD
			}
			fmt.Fprintf(bw, "%-30s%18s%8s\n", "  "+SourceLabel(src), fmtUSD(st.USD), share(sh))
		}
	}
	if n := len(d.Totals.UnknownModels); n > 0 {
		var ids []string
		var turns int
		for id, c := range d.Totals.UnknownModels {
			ids = append(ids, id)
			turns += c
		}
		sort.Strings(ids)
		fmt.Fprintf(bw, "  Not priced: %d turns on %s (no price on file; add one under [prices] in config.toml)\n", turns, strings.Join(ids, ", "))
	}
	fmt.Fprintln(bw)

	if len(d.Findings) == 0 {
		fmt.Fprintln(bw, "No findings in this window. Nothing stood out as overpaid.")
	} else {
		fmt.Fprintln(bw, "Top findings (estimated saving / month)")
		fmt.Fprintln(bw)
		for i, f := range d.Findings {
			writeFindingLine(bw, i+1, f, d.Plan)
		}
	}
	fmt.Fprintln(bw)
	if len(d.Findings) > 0 {
		fmt.Fprintln(bw, "Run `tallybook finding <n>` for evidence and the change to make.")
	}
	if len(d.Findings) > 1 {
		fmt.Fprintln(bw, "Savings are estimated one finding at a time. Where two touch the same runs")
		fmt.Fprintln(bw, "they overlap, so they do not add up.")
	}

	if d.SkippedFiles > 0 {
		fmt.Fprintf(bw, "Skipped %d files that could not be read (tallybook status shows them).\n", d.SkippedFiles)
	}

	fmt.Fprintln(bw)
	for _, line := range wrapText(closingLine(d.Plan, d.PricesDated), 96) {
		fmt.Fprintln(bw, line)
	}

	return bw.Flush()
}

// writeFindingLine writes one report row plus its indented first-sentence
// continuation: " N. $58   Title                                    high confidence".
func writeFindingLine(w io.Writer, n int, f findings.Finding, plan config.Plan) {
	money := findingMoneyField(f, plan)
	conf := confidenceLabel(f)
	fmt.Fprintf(w, "%2d. %6s   %-48s %s\n", n, money, truncate(f.Title, 48), conf)
	fmt.Fprintf(w, "%13s%s\n", "", firstSentence(f.WhatHappened))
}

// dateRange formats an earliest/latest pair like "Jun 12 – Sep 10". A zero
// pair renders as "no sessions".
func dateRange(earliest, latest time.Time) string {
	if earliest.IsZero() || latest.IsZero() {
		return "no sessions"
	}
	return earliest.Format("Jan 2") + " – " + latest.Format("Jan 2")
}
