package report

import (
	"bufio"
	"fmt"
	"io"

	"github.com/magna-nz/tallybook/internal/config"
	"github.com/magna-nz/tallybook/internal/ingest"
)

// StatusData is everything the status command shows.
type StatusData struct {
	DBPath  string
	DBBytes int64

	ClaudeRoots []string
	ClaudeCount int
	CodexRoots  []string
	CodexCount  int

	HaveIngest bool
	LastIngest ingest.Result

	Plan       config.Plan
	PlanReason string

	PricesDated string
}

// Status writes the ingest and configuration summary.
func Status(w io.Writer, d StatusData) error {
	bw := bufio.NewWriter(w)

	fmt.Fprintf(bw, "Database    %s (%s)\n", d.DBPath, fmtBytes(d.DBBytes))
	fmt.Fprintln(bw)

	fmt.Fprintln(bw, "Transcripts")
	for _, root := range d.ClaudeRoots {
		fmt.Fprintf(bw, "  %s\n", root)
	}
	fmt.Fprintf(bw, "    Claude Code sessions: %d\n", d.ClaudeCount)
	for _, root := range d.CodexRoots {
		fmt.Fprintf(bw, "  %s\n", root)
	}
	fmt.Fprintf(bw, "    Codex sessions: %d\n", d.CodexCount)
	fmt.Fprintln(bw)

	if d.HaveIngest {
		fmt.Fprintf(bw, "Last ingest   scanned %d, ingested %d, unchanged %d, failed %d (%s)\n",
			d.LastIngest.Scanned, d.LastIngest.Ingested, d.LastIngest.Unchanged,
			d.LastIngest.Failed, d.LastIngest.Elapsed.Round(1e6))
		for _, e := range d.LastIngest.Errors {
			fmt.Fprintf(bw, "    %s\n", e)
		}
	} else {
		fmt.Fprintln(bw, "Last ingest   skipped (--no-ingest)")
	}
	fmt.Fprintln(bw)

	fmt.Fprintf(bw, "Plan          %s (%s)\n", d.Plan, d.PlanReason)
	fmt.Fprintf(bw, "Prices        verified %s\n", d.PricesDated)

	return bw.Flush()
}

// fmtBytes renders a byte count as a human-friendly size.
func fmtBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	units := "KMGTPE"
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), units[exp])
}
