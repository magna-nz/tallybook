package report

import (
	"bufio"
	"fmt"
	"io"

	"github.com/magna-nz/tallybook/internal/ledger"
)

// Agents writes the per-sub-agent spend table.
func Agents(w io.Writer, rows []ledger.AgentRow) error {
	bw := bufio.NewWriter(w)

	fmt.Fprintf(bw, "%-20s%6s%18s%10s%11s%8s  %s\n",
		"agent", "runs", "model", "avg cost", "read-only", "errors", "requested≠actual")
	for _, r := range rows {
		fmt.Fprintf(bw, "%-20s%6d%18s%10s%10s%%%8d  %d\n",
			truncate(r.Agent, 20), r.Runs, truncate(r.Model, 18),
			fmtUSD(r.AvgUSD), fmt.Sprintf("%.0f", r.ReadOnlyPct*100), r.Errors, r.Mismatched)
	}
	if len(rows) == 0 {
		fmt.Fprintln(bw, "No sub-agent runs in this window.")
	}

	return bw.Flush()
}

// truncate shortens s to at most n characters.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return s[:n-1] + "…"
}
