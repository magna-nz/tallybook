package report

import (
	"bufio"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/magna-nz/tallybook/internal/ledger"
	"github.com/magna-nz/tallybook/internal/model"
)

// Sessions writes a table of the first limit rows. Callers choose the
// order (time or cost) before calling Sessions; this just truncates it.
func Sessions(w io.Writer, rows []ledger.SessionCost, limit int) error {
	bw := bufio.NewWriter(w)

	if limit > 0 && limit < len(rows) {
		rows = rows[:limit]
	}

	fmt.Fprintf(bw, "%-18s%-13s%-26s%-14s%7s%12s%14s\n",
		"session", "source", "project", "started", "turns", "cost", "sub-agent")
	for _, r := range rows {
		agent := r.AgentType
		fmt.Fprintf(bw, "%-18s%-13s%-26s%-14s%7d%12s%14s\n",
			shortID(r.ID), SourceLabel(r.Source), truncate(projectLabel(r.Project), 25),
			r.StartedAt.Format("Jan 2 15:04"), r.Turns, fmtUSD(r.USD), agent)
	}
	if len(rows) == 0 {
		fmt.Fprintln(bw, "No sessions in this window.")
	}

	return bw.Flush()
}

// Session writes one session's turn-by-turn detail.
func Session(w io.Writer, id string, source model.Source, turns []model.Turn, cost float64) error {
	bw := bufio.NewWriter(w)

	fmt.Fprintf(bw, "Session %s (%s) — %d turns, %s\n\n", id, SourceLabel(source), len(turns), fmtUSD(cost))
	fmt.Fprintf(bw, "%-6s%-9s%-18s%10s%10s%10s  %s\n",
		"turn", "time", "model", "in", "cached", "out", "note")

	var prevContext int64
	for i, t := range turns {
		note := turnNote(t, prevContext)
		fmt.Fprintf(bw, "%-6d%-9s%-18s%10d%10d%10d  %s\n",
			i+1, t.Timestamp.Format("15:04:05"), truncate(t.Model, 18),
			t.Usage.Input, t.Usage.CacheRead, t.Usage.Output, note)

		for _, tc := range t.ToolCalls {
			if tc.Agent != nil {
				fmt.Fprintf(bw, "         sub-agent: %s asked %s, ran %s\n",
					tc.Agent.SubagentType, tc.Agent.RequestedModel, tc.Agent.ResolvedModel)
			}
		}

		prevContext = t.Usage.ContextTokens()
	}

	return bw.Flush()
}

// turnNote reports "cache rebuilt" when this turn's cache writes exceed
// half of the previous turn's context size.
func turnNote(t model.Turn, prevContext int64) string {
	writes := t.Usage.CacheWrite5m + t.Usage.CacheWrite1h
	if prevContext > 0 && writes > prevContext/2 {
		return "cache rebuilt"
	}
	return ""
}

// shortID returns the first 8 characters of a session id. A sub-agent id
// ("<parent>/agent-<agentId>") shows as "<parent8>/<agent6>" so siblings can
// be told apart.
func shortID(id string) string {
	if parent, agent, ok := strings.Cut(id, "/agent-"); ok {
		if len(agent) > 6 {
			agent = agent[:6]
		}
		return shortID(parent) + "/" + agent
	}
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

// projectLabel is the base name of a project path, with a "(worktree)"
// suffix when the path is under a Claude Code worktree.
func projectLabel(project string) string {
	if project == "" {
		return ""
	}
	if repo, _, ok := strings.Cut(project, "/.claude/worktrees/"); ok {
		return filepath.Base(repo) + " (worktree)"
	}
	return filepath.Base(project)
}
