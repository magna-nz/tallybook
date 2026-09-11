package report

import (
	"bufio"
	"fmt"
	"io"

	"github.com/magna-nz/tallybook/internal/config"
	"github.com/magna-nz/tallybook/internal/findings"
)

// FindingsData is everything the `findings` command needs to render. It is
// the same list the default report was given, uncapped.
type FindingsData struct {
	Findings []findings.Finding
	Plan     config.Plan
}

// directionGroup is one heading in the `findings` listing.
type directionGroup struct {
	Direction findings.Direction
	Heading   string
}

// directionGroups is the fixed order the groups print in: the cheap,
// mechanical changes first and "this needs a stronger model" last.
var directionGroups = []directionGroup{
	{findings.Downgrade, "Move work to a cheaper model"},
	{findings.Context, "Shrink what is sent every turn"},
	{findings.Cache, "Keep the prompt cache warm"},
	{findings.Effort, "Lower thinking effort"},
	{findings.Config, "A setting is not doing what you think"},
	{findings.Upgrade, "Needs a stronger model or a better brief"},
}

// Findings writes every finding, grouped by the kind of change it asks for.
// Each line keeps the number it has in the full list, so the numbers match
// the default report's and `tallybook finding <n>` takes them unchanged.
func Findings(w io.Writer, d FindingsData) error {
	bw := bufio.NewWriter(w)

	if len(d.Findings) == 0 {
		fmt.Fprintln(bw, "No findings in this window. Nothing stood out as overpaid.")
		return bw.Flush()
	}

	fmt.Fprintln(bw, "Every finding in this window (estimated saving / month)")

	for _, g := range directionGroups {
		writeDirectionGroup(bw, g.Heading, d, g.Direction)
	}
	// A finding with a direction this version does not know about must still
	// be listed rather than vanish from a listing that claims to be complete.
	writeDirectionGroup(bw, "Other", d, "")

	fmt.Fprintln(bw)
	fmt.Fprintln(bw, "Run `tallybook finding <n>` for evidence and the change to make.")

	return bw.Flush()
}

// writeDirectionGroup writes one heading and its rows, or nothing when no
// finding has that direction. An empty direction collects everything the
// fixed groups did not claim.
func writeDirectionGroup(w io.Writer, heading string, d FindingsData, dir findings.Direction) {
	var rows []NumberedFinding
	for i, f := range d.Findings {
		if inDirection(f.Direction, dir) {
			rows = append(rows, NumberedFinding{N: i + 1, Finding: f})
		}
	}
	if len(rows) == 0 {
		return
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, heading)
	fmt.Fprintln(w)
	for _, nf := range rows {
		writeFindingLine(w, nf.N, nf.Finding, d.Plan)
	}
}

// inDirection reports whether a finding belongs in the group for dir. The
// empty dir is the catch-all for directions no fixed group covers.
func inDirection(have, dir findings.Direction) bool {
	if dir != "" {
		return have == dir
	}
	for _, g := range directionGroups {
		if have == g.Direction {
			return false
		}
	}
	return true
}
