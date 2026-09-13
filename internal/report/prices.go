package report

import (
	"bufio"
	"fmt"
	"io"

	"github.com/magna-nz/tallybook/internal/pricing"
)

// Prices writes the built-in price table, one row per model id, sorted.
func Prices(w io.Writer, table *pricing.Table) error {
	bw := bufio.NewWriter(w)

	fmt.Fprintf(bw, "%-24s%10s%14s%12s%12s%10s\n",
		"id", "input", "cache read", "5m write", "1h write", "output")
	for _, id := range table.Models() {
		r, ok := table.Lookup(id)
		if !ok {
			continue
		}
		fmt.Fprintf(bw, "%-24s%10s%14s%12s%12s%10s\n",
			id, rate(r.Input), rate(r.CacheRead), rate(r.CacheWrite5m), rate(r.CacheWrite1h), rate(r.Output))
		// A model with a premium tier bills two different ways, and showing
		// only the standard row invites someone reconciling a bill to think
		// tallybook disagrees with the vendor.
		if r.Fast != nil {
			tierRow(bw, "  in fast mode", *r.Fast)
		}
		if r.Long != nil {
			tierRow(bw, fmt.Sprintf("  over %dk tokens", r.LongContextFrom/1000), *r.Long)
		}
	}

	return bw.Flush()
}

// tierRow prints one premium tier under the model it belongs to.
func tierRow(bw *bufio.Writer, label string, r pricing.Rate) {
	fmt.Fprintf(bw, "%-24s%10s%14s%12s%12s%10s\n",
		label, rate(r.Input), rate(r.CacheRead), rate(r.CacheWrite5m), rate(r.CacheWrite1h), rate(r.Output))
}

// rate formats a per-million-token rate as a dollar figure.
func rate(usdPerMillion float64) string {
	return fmtUSD(usdPerMillion)
}
