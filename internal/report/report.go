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

	// MinSavingUSD and ReportLimit shape the top-findings list: the smallest
	// monthly saving worth listing and how many priced findings to print.
	// Zero means "use the default". Neither affects ReportJSON, which always
	// writes every finding.
	MinSavingUSD float64
	ReportLimit  int

	SkippedFiles int // ingest.Result.Failed; 0 when not ingested or nothing failed

	// Compare is the window before this one, when the caller asked for the
	// comparison. Nil means the block is not printed.
	Compare *Comparison
}

// Report writes the default report: a scan summary, the window totals
// table, the top findings, and a currency footer.
func Report(w io.Writer, d ReportData) error {
	bw := bufio.NewWriter(w)

	fmt.Fprintf(bw, "Scanned %d sessions, %d sub-agent runs (%s)\n\n",
		d.Sessions, d.Subagents, dateRange(d.Earliest, d.Latest))

	title := windowTitle(d.SinceFlag, d.Window.Label)

	// On a subscription the dollars are not a bill, so share leads and the
	// list-price equivalent follows as a sense of scale. Paying per token, the
	// money is the point and leads.
	row := func(label string, usd, fraction float64) {
		if d.Plan == config.PlanSubscription {
			fmt.Fprintf(bw, "%-30s%8s%18s\n", label, share(fraction), fmtUSD(usd))
			return
		}
		fmt.Fprintf(bw, "%-30s%18s%8s\n", label, fmtUSD(usd), share(fraction))
	}
	if d.Plan == config.PlanSubscription {
		fmt.Fprintf(bw, "%-30s%8s%18s\n", title, "share", "list-price equiv.")
	} else {
		fmt.Fprintf(bw, "%-30s%18s%8s\n", title, moneyHeader(d.Plan), "share")
	}

	var mainShare, subShare float64
	if d.Totals.USD > 0 {
		mainShare = d.Totals.MainUSD / d.Totals.USD
		subShare = d.Totals.SubagentUSD / d.Totals.USD
	}
	row("  Total", d.Totals.USD, 1)
	row("  Main session turns", d.Totals.MainUSD, mainShare)
	row("  Sub-agents", d.Totals.SubagentUSD, subShare)
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
			row("  "+SourceLabel(src), st.USD, sh)
		}
	}
	// One line for whether caching is working: the share of everything sent
	// that came back from the cache. It sits under the share column because
	// it is a share, not money.
	if d.Plan == config.PlanSubscription {
		fmt.Fprintf(bw, "%-30s%8s\n", "  Cache hit rate", share(d.Totals.CacheHitRate()))
	} else {
		fmt.Fprintf(bw, "%-30s%18s%8s\n", "  Cache hit rate", "", share(d.Totals.CacheHitRate()))
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

	if d.Compare != nil {
		writeComparison(bw, d)
		fmt.Fprintln(bw)
	}

	top := SelectTop(d.Findings, d.MinSavingUSD, d.ReportLimit)
	if len(d.Findings) == 0 {
		fmt.Fprintln(bw, "No findings in this window. Nothing stood out as overpaid.")
	} else {
		writeTopFindings(bw, top, d.MinSavingUSD, d.Plan)
	}
	fmt.Fprintln(bw)
	if len(d.Findings) > 0 {
		fmt.Fprintln(bw, "Run `tallybook finding <n>` for evidence and the change to make.")
	}
	if len(top.Priced) > 1 {
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

// writeTopFindings writes the report's findings section: the priced
// findings worth listing, the notes under them, and the line that points at
// `tallybook findings` when anything was held back.
func writeTopFindings(w io.Writer, top TopFindings, minSavingUSD float64, plan config.Plan) {
	if len(top.Priced) > 0 {
		fmt.Fprintln(w, "Top findings (estimated saving / month)")
		fmt.Fprintln(w)
		for _, nf := range top.Priced {
			writeFindingLine(w, nf.N, nf.Finding, plan)
		}
	}
	if len(top.Info) > 0 {
		if len(top.Priced) > 0 {
			fmt.Fprintln(w)
		}
		fmt.Fprintln(w, "Also worth knowing")
		fmt.Fprintln(w)
		for _, nf := range top.Info {
			writeNoteLine(w, nf.N, nf.Finding)
		}
	}
	if len(top.Priced) == 0 && len(top.Info) == 0 {
		floor := minSavingUSD
		if floor <= 0 {
			floor = defaultMinSavingUSD
		}
		fmt.Fprintf(w, "Nothing in this window is worth more than %s a month to change.\n", fmtUSD(floor))
	}
	if line := moreLine(top); line != "" {
		fmt.Fprintln(w)
		fmt.Fprintln(w, line)
	}
}

// writeFindingLine writes one report row plus its indented first-sentence
// continuation: " N.   $58.00   Title". The confidence is deliberately not on
// the line: it read as noise next to the money, and the finding's own text
// says whether its number is measured or estimated. It stays in --json.
func writeFindingLine(w io.Writer, n int, f findings.Finding, plan config.Plan) {
	money := findingMoneyField(f, plan)
	// Seven characters of money: a three-figure monthly saving is not rare.
	fmt.Fprintf(w, "%2d. %7s   %s\n", n, money, f.Title)
	fmt.Fprintf(w, "%14s%s\n", "", firstSentence(f.WhatHappened))
}

// writeNoteLine writes one Info finding as a single line, with no money
// column and no continuation sentence.
func writeNoteLine(w io.Writer, n int, f findings.Finding) {
	fmt.Fprintf(w, "%2d. %s\n", n, f.Title)
}

// dateRange formats an earliest/latest pair like "Jun 12 – Sep 10". A zero
// pair renders as "no sessions".
func dateRange(earliest, latest time.Time) string {
	if earliest.IsZero() || latest.IsZero() {
		return "no sessions"
	}
	return earliest.Format("Jan 2") + " – " + latest.Format("Jan 2")
}
