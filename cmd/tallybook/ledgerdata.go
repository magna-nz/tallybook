package main

import (
	"time"

	"github.com/magna-nz/tallybook/internal/agentfile"
	"github.com/magna-nz/tallybook/internal/findings"
	"github.com/magna-nz/tallybook/internal/ledger"
	"github.com/magna-nz/tallybook/internal/query"
	"github.com/magna-nz/tallybook/internal/report"
	"github.com/magna-nz/tallybook/internal/store"
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
		Agents:     agentfile.Load(projectRoots(ctx)...),
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

// projectRoots is every project directory seen in the window, so the advice can
// be checked against those projects' own agent files. A worktree's agent files
// live in the worktree, so the paths are used exactly as recorded.
func projectRoots(ctx *appContext) []string {
	rows, err := ctx.st.Sessions(store.Filter{Since: ctx.filter.Since, Until: ctx.filter.Until, Project: ctx.filter.Project})
	if err != nil {
		return nil
	}
	return query.DistinctProjects(rows)
}

// priorComparison builds the window-on-window block for --compare: the same
// filter, shifted back one window length. ok is false when the window has
// nothing before it, which is what --since all means.
func priorComparison(ctx *appContext) (*report.Comparison, bool, error) {
	prior, ok := ledger.Prior(ctx.window)
	if !ok {
		return nil, false, nil
	}
	filter := ctx.filter
	filter.Since, filter.Until = prior.Since, prior.Until
	sessions, err := ledger.Sessions(ctx.st, ctx.prices, filter)
	if err != nil {
		return nil, true, err
	}
	totals, err := ledger.Total(sessions, ctx.st, ctx.prices)
	if err != nil {
		return nil, true, err
	}
	mainCount, subCount := countMainAndSub(sessions)
	return &report.Comparison{
		Window:    prior,
		Totals:    totals,
		Sessions:  mainCount,
		Subagents: subCount,
	}, true, nil
}
