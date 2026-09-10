package report

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"github.com/magna-nz/tallybook/internal/config"
	"github.com/magna-nz/tallybook/internal/findings"
)

// findingLineWidth is the nominal width of the finding title line, used to
// right-align the saving phrase against the title.
const findingLineWidth = 76

// wrapWidth is the column width paragraphs are wrapped to inside a finding.
const wrapWidth = 76

// FindingMode carries the currency mode and the optional evidence table
// toggle for rendering one finding.
type FindingMode struct {
	Plan     config.Plan
	Evidence bool
}

// Finding writes one finding in full: the title line with its saving, the
// four plain-English sections, and (with mode.Evidence) the evidence table.
func Finding(w io.Writer, f findings.Finding, index int, mode FindingMode) error {
	bw := bufio.NewWriter(w)

	fmt.Fprintln(bw, titleLine(f.Title, findingSaving(f, mode.Plan)))
	fmt.Fprintln(bw)

	whyHeading := "  Why it costs money"
	if f.Direction == findings.Upgrade {
		whyHeading = "  Why it matters"
	}

	writeSection(bw, "  What happened", f.WhatHappened)
	writeSection(bw, whyHeading, f.WhyItCosts)
	writeSection(bw, "  What to change", f.WhatToChange)
	writeSection(bw, "  What to expect", f.WhatToExpect)

	if mode.Evidence {
		writeEvidence(bw, f.Evidence)
	}

	return bw.Flush()
}

// titleLine right-aligns saving against title within findingLineWidth
// columns, leaving at least one space between them.
func titleLine(title, saving string) string {
	gap := findingLineWidth - len(title) - len(saving)
	if gap < 1 {
		gap = 1
	}
	return title + strings.Repeat(" ", gap) + saving
}

// findingSaving is the phrase shown at the top right of a finding.
func findingSaving(f findings.Finding, plan config.Plan) string {
	if f.Confidence == findings.Info {
		return confidenceLabel(f)
	}
	if plan == config.PlanSubscription {
		return fmt.Sprintf("frees about %s of your usage", share(f.SavingShare))
	}
	return fmt.Sprintf("saves about %s/month", fmtUSD(f.SavingUSD))
}

// writeSection writes a heading followed by its wrapped, indented body.
// Lines that already start with four spaces (code/config snippets) are
// preserved verbatim; blank lines separate paragraphs.
func writeSection(w io.Writer, heading, body string) {
	fmt.Fprintln(w, heading)
	for _, line := range strings.Split(body, "\n") {
		switch {
		case line == "":
			fmt.Fprintln(w)
		case strings.HasPrefix(line, "    "):
			fmt.Fprintln(w, line)
		case strings.HasPrefix(line, "- "):
			for i, wrapped := range wrapText(line[2:], wrapWidth-4) {
				if i == 0 {
					fmt.Fprintln(w, "  - "+wrapped)
				} else {
					fmt.Fprintln(w, "    "+wrapped)
				}
			}
		default:
			for _, wrapped := range wrapText(line, wrapWidth) {
				fmt.Fprintln(w, "  "+wrapped)
			}
		}
	}
	fmt.Fprintln(w)
}

// writeEvidence renders a findings.Table as a simple column-aligned grid.
func writeEvidence(w io.Writer, t findings.Table) {
	if len(t.Columns) == 0 {
		return
	}
	widths := make([]int, len(t.Columns))
	for i, c := range t.Columns {
		widths[i] = len(c)
	}
	for _, row := range t.Rows {
		for i, cell := range row {
			if i < len(widths) && len(cell) > widths[i] {
				widths[i] = len(cell)
			}
		}
	}

	fmt.Fprintln(w, "  Evidence")
	writeEvidenceRow(w, t.Columns, widths)
	for _, row := range t.Rows {
		writeEvidenceRow(w, row, widths)
	}
	fmt.Fprintln(w)
}

func writeEvidenceRow(w io.Writer, cells []string, widths []int) {
	var b strings.Builder
	b.WriteString("  ")
	for i, cell := range cells {
		width := 0
		if i < len(widths) {
			width = widths[i]
		}
		b.WriteString(fmt.Sprintf("%-*s", width, cell))
		if i != len(cells)-1 {
			b.WriteString("  ")
		}
	}
	fmt.Fprintln(w, strings.TrimRight(b.String(), " "))
}
