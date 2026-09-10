// Package report renders the ledger and findings as text or JSON. Every
// dollar figure goes through fmtUSD and every fraction through share, so the
// formatting is consistent everywhere it appears.
package report

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/magna-nz/tallybook/internal/config"
	"github.com/magna-nz/tallybook/internal/findings"
)

// fmtUSD formats a dollar amount like "$1,234.56" or "$0.40".
func fmtUSD(amount float64) string {
	neg := amount < 0
	if neg {
		amount = -amount
	}
	cents := int64(math.Round(amount * 100))
	whole := cents / 100
	rem := cents % 100

	ws := strconv.FormatInt(whole, 10)
	var b strings.Builder
	n := len(ws)
	for i, c := range ws {
		if i > 0 && (n-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}

	s := fmt.Sprintf("$%s.%02d", b.String(), rem)
	if neg {
		s = "-" + s
	}
	return s
}

// share formats a fraction (0.34) as a percentage string ("34%").
func share(fraction float64) string {
	return fmt.Sprintf("%.0f%%", fraction*100)
}

// moneyHeader returns the label for the money column: "list price" for a
// per-token plan, "list-price equiv." for a subscription.
func moneyHeader(plan config.Plan) string {
	if plan == config.PlanSubscription {
		return "list-price equiv."
	}
	return "list price"
}

// closingLine is the footer sentence that reminds the reader what the
// dollar figures mean.
func closingLine(plan config.Plan, dated string) string {
	if plan == config.PlanSubscription {
		return "You are on a subscription: dollars are what this usage would cost on the API, " +
			"not what you paid. Share is the number to watch."
	}
	return fmt.Sprintf("Prices are Anthropic and OpenAI list prices, verified %s.", dated)
}

// windowTitle renders the heading over the totals table: "Last 30 days" for
// the relative windows, "All time" for "all", and the window's date-range
// label otherwise.
func windowTitle(sinceFlag string, label string) string {
	switch sinceFlag {
	case "7d":
		return "Last 7 days"
	case "30d":
		return "Last 30 days"
	case "90d":
		return "Last 90 days"
	case "all":
		return "All time"
	default:
		return label
	}
}

// confidenceLabel is the text shown in a finding's confidence slot.
func confidenceLabel(f findings.Finding) string {
	if f.Confidence == findings.Info {
		if f.Direction == findings.Upgrade {
			return "do not downgrade"
		}
		return "informational"
	}
	return string(f.Confidence) + " confidence"
}

// findingMoneyField is what goes in the report's per-finding money/share
// column: "--" for an Info finding, otherwise the saving in the currency
// the plan calls for.
func findingMoneyField(f findings.Finding, plan config.Plan) string {
	if f.Confidence == findings.Info {
		return "--"
	}
	if plan == config.PlanSubscription {
		return share(f.SavingShare)
	}
	return fmtUSD(f.SavingUSD)
}

// firstSentence returns the text up to and including the first ". " (or the
// whole string if there is none), truncated so it always fits on an
// 100-column line once indented.
func firstSentence(s string) string {
	s = strings.TrimSpace(s)
	if idx := strings.Index(s, ". "); idx != -1 {
		s = s[:idx+1]
	}
	const maxLen = 84
	if len(s) > maxLen {
		s = strings.TrimSpace(s[:maxLen-1]) + "…"
	}
	return s
}

// wrapText greedily wraps s into lines no wider than width columns.
func wrapText(s string, width int) []string {
	words := strings.Fields(s)
	if len(words) == 0 {
		return nil
	}
	lines := make([]string, 0, len(words)/8+1)
	cur := words[0]
	for _, word := range words[1:] {
		if len(cur)+1+len(word) > width {
			lines = append(lines, cur)
			cur = word
			continue
		}
		cur += " " + word
	}
	lines = append(lines, cur)
	return lines
}
