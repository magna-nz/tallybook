package main

import (
	"time"

	"github.com/magna-nz/tallybook/internal/findings"
	"github.com/magna-nz/tallybook/internal/ledger"
)

// sessionsAndTotals runs the ledger queries every report-like command
// needs, in the one order that keeps them consistent with each other.
func sessionsAndTotals(ctx *appContext) ([]ledger.SessionCost, ledger.Totals, error) {
	sessions, err := ledger.Sessions(ctx.st, ctx.prices, ctx.filter)
	if err != nil {
		return nil, ledger.Totals{}, err
	}
	totals, err := ledger.Total(sessions, ctx.st, ctx.prices)
	if err != nil {
		return nil, ledger.Totals{}, err
	}
	return sessions, totals, nil
}

// findingsFor runs every enabled finding rule over ctx's window. report and
// finding must call this with the same totals so that `finding <n>` indexes
// the exact list `report` printed.
func findingsFor(ctx *appContext, totals ledger.Totals) ([]findings.Finding, error) {
	in := findings.Input{
		Store:      ctx.st,
		Prices:     ctx.prices,
		Cfg:        ctx.cfg.Findings,
		Plan:       ctx.plan,
		Filter:     ctx.filter,
		Now:        time.Now(),
		TotalUSD:   totals.USD,
		WindowDays: ctx.window.Days,
	}
	return findings.Run(in)
}

// sessionSpan returns the earliest StartedAt and latest EndedAt among
// sessions, or the zero pair if there are none.
func sessionSpan(sessions []ledger.SessionCost) (earliest, latest time.Time) {
	for _, s := range sessions {
		if !s.StartedAt.IsZero() && (earliest.IsZero() || s.StartedAt.Before(earliest)) {
			earliest = s.StartedAt
		}
		if s.EndedAt.After(latest) {
			latest = s.EndedAt
		}
	}
	return earliest, latest
}

// countMainAndSub splits sessions into main (non-sub-agent) and sub-agent
// counts.
func countMainAndSub(sessions []ledger.SessionCost) (main, sub int) {
	for _, s := range sessions {
		if s.AgentID == "" {
			main++
		} else {
			sub++
		}
	}
	return main, sub
}
