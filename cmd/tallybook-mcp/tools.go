package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/magna-nz/tallybook/internal/findings"
	"github.com/magna-nz/tallybook/internal/ledger"
	"github.com/magna-nz/tallybook/internal/model"
	"github.com/magna-nz/tallybook/internal/report"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Scope is the set of inputs every window-based tool accepts. Each field is
// optional; an empty value means the default the CLI would use.
type Scope struct {
	Since    string `json:"since,omitempty" jsonschema:"window: 7d, 30d, 90d, all, or a YYYY-MM-DD start date; default 30d"`
	Project  string `json:"project,omitempty" jsonschema:"restrict to one project directory, exact match, or a prefix if it ends with a path separator"`
	Source   string `json:"source,omitempty" jsonschema:"restrict to one tool: \"claude-code\" or \"codex\"; default both"`
	Currency string `json:"currency,omitempty" jsonschema:"override the detected plan: \"usd\" reports as charged, \"share\" reports as a subscription's list-price equivalent"`
}

// timeString formats a time for output, or returns "" for the zero time.
func timeString(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

// call wraps a tool body with the locking and freshness check every tool
// needs. The body returns the text a model reads and the structured value a
// client computes on. An error becomes an error result for that call only.
// When autoRefresh is false the body is responsible for scanning itself.
func call[In, Out any](a *app, autoRefresh bool, body func(In) (string, Out, error)) mcp.ToolHandlerFor[In, Out] {
	return func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		a.mu.Lock()
		defer a.mu.Unlock()
		var zero Out
		if err := ctx.Err(); err != nil {
			return nil, zero, err // cancelled while queued behind another call
		}
		if autoRefresh {
			a.ensureFresh()
		}
		text, out, err := body(in)
		if err != nil {
			return nil, zero, err
		}
		return textResult(a.freshnessNote() + text), out, nil
	}
}

// queryTool marks a tool that only reads. The lazy rescan such a tool may
// trigger writes only tallybook's own cache, which is not a change to the
// caller's environment in the sense the hint describes.
func queryTool() *mcp.ToolAnnotations {
	closed := false
	return &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closed}
}

// refreshTool marks the one tool whose purpose is to write: it updates
// tallybook's database. Nothing is destroyed, and running it twice is the
// same as running it once.
func refreshTool() *mcp.ToolAnnotations {
	no := false
	return &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: &no, IdempotentHint: true, OpenWorldHint: &no}
}

func registerTools(s *mcp.Server, a *app) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "report",
		Description: "What the coding agents on this machine cost over a window: the total, the split by tool and by model, and the list of findings about what would have been cheaper. Call finding with an id from the list for the full advice.",
		Annotations: queryTool(),
	}, call(a, true, a.report))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "finding",
		Description: "One finding in full, addressed by its stable id from report: what happened, why it costs, what to change, what to expect, plus the evidence table and the patch if there is a file to change.",
		Annotations: queryTool(),
	}, call(a, true, a.finding))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "changes",
		Description: "Every point where a sub-agent's model changed, with the real runs before and after compared and a verdict: keep, watch, revert, or too early. These are measured figures, not estimates.",
		Annotations: queryTool(),
	}, call(a, true, a.changes))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "agents",
		Description: "Spend per sub-agent type: runs, the model it mostly used, average cost per run, the share of runs whose tool calls were all read-only, errored tool results, and runs where the requested model differed from the one actually used.",
		Annotations: queryTool(),
	}, call(a, true, a.agents))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "sessions",
		Description: "Sessions in the window with what each cost, sorted by cost or by time, most expensive or most recent first. Includes sub-agent runs as their own rows.",
		Annotations: queryTool(),
	}, call(a, true, a.sessions))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "session",
		Description: "One session turn by turn: model, effort, token counts and cost per model response. Accepts a full session id or a unique prefix. Never returns prompt text, tool output, or command lines; the database does not hold them.",
		Annotations: queryTool(),
	}, call(a, true, a.session))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "prices",
		Description: "The price table every figure is computed from, in US dollars per million tokens, with the date it was last verified. Includes any overrides from the user's config.",
		Annotations: queryTool(),
	}, call(a, false, a.pricesTool))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "refresh",
		Description: "Rescan the transcript directories now instead of waiting for the automatic rescan, and report how many files were scanned, newly recorded, unchanged, or skipped. Only tallybook's own database is written.",
		Annotations: refreshTool(),
	}, call(a, false, a.refresh))
}

// ---- report ----

type ReportIn struct {
	Scope
}

type WindowOut struct {
	Since string  `json:"since,omitempty" jsonschema:"start of the window, RFC 3339; empty for all time"`
	Until string  `json:"until" jsonschema:"end of the window, RFC 3339"`
	Days  float64 `json:"days"`
	Label string  `json:"label"`
}

type SourceOut struct {
	USD      float64 `json:"usd"`
	Sessions int     `json:"sessions" jsonschema:"main sessions, not sub-agent runs"`
	Turns    int     `json:"turns"`
}

type FindingSummary struct {
	ID         string  `json:"id" jsonschema:"stable slug; pass it to the finding tool"`
	Title      string  `json:"title"`
	SavingUSD  float64 `json:"saving_usd" jsonschema:"estimated saving per 30 days at list price; 0 for info findings"`
	Share      float64 `json:"share" jsonschema:"the saving as a fraction of the window's total, 0..1"`
	Confidence string  `json:"confidence" jsonschema:"high, medium, low, or info"`
	Direction  string  `json:"direction" jsonschema:"downgrade, upgrade, context, cache, effort, or config"`
}

type ReportOut struct {
	Money
	Freshness
	Window        WindowOut            `json:"window"`
	Sessions      int                  `json:"sessions" jsonschema:"main sessions in the window"`
	Subagents     int                  `json:"subagents" jsonschema:"sub-agent runs in the window"`
	Turns         int                  `json:"turns"`
	USD           float64              `json:"usd" jsonschema:"total for the window; see currency"`
	MainUSD       float64              `json:"main_usd"`
	SubagentUSD   float64              `json:"subagent_usd"`
	BySource      map[string]SourceOut `json:"by_source"`
	ByModel       map[string]float64   `json:"by_model" jsonschema:"canonical model id to dollars"`
	UnknownModels map[string]int       `json:"unknown_models,omitempty" jsonschema:"model ids with no price, with the number of turns they appeared on; those turns cost 0 here"`
	Findings      []FindingSummary     `json:"findings"`
	SkippedFiles  int                  `json:"skipped_files" jsonschema:"transcript files the last scan could not parse"`
}

func (a *app) report(in ReportIn) (string, ReportOut, error) {
	sc, err := a.newScope(in.Scope)
	if err != nil {
		return "", ReportOut{}, err
	}
	sessions, totals, err := a.sessionsAndTotals(sc)
	if err != nil {
		return "", ReportOut{}, err
	}
	fs, err := a.findingsFor(sc, totals)
	if err != nil {
		return "", ReportOut{}, err
	}

	out := ReportOut{
		Money:     money(sc),
		Freshness: a.freshness(),
		Window: WindowOut{
			Since: timeString(sc.window.Since),
			Until: sc.window.Until.Format(time.RFC3339),
			Days:  sc.window.Days,
			Label: sc.window.Label,
		},
		// ledger.Totals.Sessions counts every row; the CLI reports main
		// sessions and sub-agent runs separately, so do the same here.
		Sessions:      totals.Sessions - totals.Subagents,
		Subagents:     totals.Subagents,
		Turns:         totals.Turns,
		USD:           totals.USD,
		MainUSD:       totals.MainUSD,
		SubagentUSD:   totals.SubagentUSD,
		BySource:      map[string]SourceOut{},
		ByModel:       totals.ByModel,
		UnknownModels: totals.UnknownModels,
		Findings:      []FindingSummary{},
		SkippedFiles:  a.ingestResult(sc.filter.Source).Failed,
	}
	if out.ByModel == nil {
		out.ByModel = map[string]float64{}
	}
	for src, st := range totals.BySource {
		out.BySource[string(src)] = SourceOut{USD: st.USD, Sessions: st.Sessions, Turns: st.Turns}
	}
	for _, f := range fs {
		out.Findings = append(out.Findings, FindingSummary{
			ID: f.ID, Title: f.Title, SavingUSD: f.SavingUSD, Share: f.SavingShare,
			Confidence: string(f.Confidence), Direction: string(f.Direction),
		})
	}

	var b strings.Builder
	fmt.Fprintln(&b, moneyNote(sc))
	fmt.Fprintf(&b, "Window: %s (%s, %.1f days). Data scanned at %s.\n", sc.sinceFlag, sc.window.Label, sc.window.Days, out.IngestedAt)
	if len(sessions) == 0 {
		fmt.Fprintln(&b, "No sessions in this window.")
		return b.String(), out, nil
	}
	fmt.Fprintf(&b, "Total %s across %d sessions and %d sub-agent runs (%d turns). Main %s, sub-agents %s.\n",
		fmtUSD(totals.USD), totals.Sessions-totals.Subagents, totals.Subagents, totals.Turns, fmtUSD(totals.MainUSD), fmtUSD(totals.SubagentUSD))
	for _, src := range sortedSources(totals.BySource) {
		st := totals.BySource[src]
		fmt.Fprintf(&b, "  %s: %s over %d sessions\n", report.SourceLabel(src), fmtUSD(st.USD), st.Sessions)
	}
	for _, id := range sortedByValue(totals.ByModel) {
		fmt.Fprintf(&b, "  %s: %s\n", id, fmtUSD(totals.ByModel[id]))
	}
	if len(totals.UnknownModels) > 0 {
		fmt.Fprintf(&b, "Unpriced models (counted as $0): %s\n", joinKeys(totals.UnknownModels))
	}
	if len(fs) == 0 {
		fmt.Fprintln(&b, "No findings: nothing crossed its evidence floor in this window.")
	} else {
		fmt.Fprintf(&b, "Findings (%d), call finding with the id for the full advice:\n", len(fs))
		for _, f := range fs {
			if f.Confidence == findings.Info {
				fmt.Fprintf(&b, "  [%s] %s (info)\n", f.ID, f.Title)
				continue
			}
			fmt.Fprintf(&b, "  [%s] %s: about %s per 30 days, %s confidence\n", f.ID, f.Title, fmtUSD(f.SavingUSD), f.Confidence)
		}
	}
	if out.SkippedFiles > 0 {
		fmt.Fprintf(&b, "%d transcript files could not be parsed and are not counted.\n", out.SkippedFiles)
	}
	return b.String(), out, nil
}

func sortedSources(m map[model.Source]ledger.SourceTotal) []model.Source {
	out := make([]model.Source, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func sortedByValue(m map[string]float64) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool {
		if m[out[i]] != m[out[j]] {
			return m[out[i]] > m[out[j]]
		}
		return out[i] < out[j]
	})
	return out
}

func joinKeys(m map[string]int) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}

// ---- finding ----

type FindingIn struct {
	Scope
	ID string `json:"id" jsonschema:"the finding's id from report, such as readonly-agent-on-strong-model"`
}

type EvidenceOut struct {
	Columns []string   `json:"columns"`
	Rows    [][]string `json:"rows"`
}

type FindingOut struct {
	Money
	Freshness
	ID           string      `json:"id"`
	Title        string      `json:"title"`
	SavingUSD    float64     `json:"saving_usd" jsonschema:"estimated saving per 30 days at list price; 0 for info findings"`
	Share        float64     `json:"share"`
	Confidence   string      `json:"confidence"`
	Direction    string      `json:"direction"`
	WhatHappened string      `json:"what_happened"`
	WhyItCosts   string      `json:"why_it_costs"`
	WhatToChange string      `json:"what_to_change"`
	WhatToExpect string      `json:"what_to_expect"`
	Patch        string      `json:"patch,omitempty" jsonschema:"unified diff of the file change, when there is a file to change; tallybook never applies it"`
	Evidence     EvidenceOut `json:"evidence"`
}

func (a *app) finding(in FindingIn) (string, FindingOut, error) {
	if in.ID == "" {
		return "", FindingOut{}, fmt.Errorf("id is required; call report for the list of finding ids")
	}
	sc, err := a.newScope(in.Scope)
	if err != nil {
		return "", FindingOut{}, err
	}
	_, totals, err := a.sessionsAndTotals(sc)
	if err != nil {
		return "", FindingOut{}, err
	}
	fs, err := a.findingsFor(sc, totals)
	if err != nil {
		return "", FindingOut{}, err
	}

	var f *findings.Finding
	ids := make([]string, 0, len(fs))
	for i := range fs {
		ids = append(ids, fs[i].ID)
		if fs[i].ID == in.ID {
			f = &fs[i]
		}
	}
	if f == nil {
		if len(ids) == 0 {
			return "", FindingOut{}, fmt.Errorf("no finding %q: there are no findings in this window", in.ID)
		}
		return "", FindingOut{}, fmt.Errorf("no finding %q in this window; available: %s", in.ID, strings.Join(ids, ", "))
	}

	out := FindingOut{
		Money:        money(sc),
		Freshness:    a.freshness(),
		ID:           f.ID,
		Title:        f.Title,
		SavingUSD:    f.SavingUSD,
		Share:        f.SavingShare,
		Confidence:   string(f.Confidence),
		Direction:    string(f.Direction),
		WhatHappened: f.WhatHappened,
		WhyItCosts:   f.WhyItCosts,
		WhatToChange: f.WhatToChange,
		WhatToExpect: f.WhatToExpect,
		Patch:        f.Patch,
		Evidence:     EvidenceOut{Columns: f.Evidence.Columns, Rows: f.Evidence.Rows},
	}
	if out.Evidence.Columns == nil {
		out.Evidence.Columns = []string{}
	}
	if out.Evidence.Rows == nil {
		out.Evidence.Rows = [][]string{}
	}

	var b strings.Builder
	fmt.Fprintln(&b, moneyNote(sc))
	fmt.Fprintf(&b, "[%s] %s\n", f.ID, f.Title)
	if f.Confidence == findings.Info {
		fmt.Fprintln(&b, "Info: nothing to save, a pointer rather than a recommendation.")
	} else {
		fmt.Fprintf(&b, "Estimated saving about %s per 30 days (%.0f%% of the window), %s confidence, direction %s.\n",
			fmtUSD(f.SavingUSD), f.SavingShare*100, f.Confidence, f.Direction)
	}
	fmt.Fprintf(&b, "\nWhat happened\n%s\n\nWhy it costs\n%s\n\nWhat to change\n%s\n\nWhat to expect\n%s\n",
		f.WhatHappened, f.WhyItCosts, f.WhatToChange, f.WhatToExpect)
	if len(f.Evidence.Rows) > 0 {
		fmt.Fprintf(&b, "\nEvidence\n%s\n", strings.Join(f.Evidence.Columns, " | "))
		for _, r := range f.Evidence.Rows {
			fmt.Fprintln(&b, strings.Join(r, " | "))
		}
	}
	if f.Patch != "" {
		fmt.Fprintf(&b, "\nPatch (not applied by tallybook)\n%s", f.Patch)
	}
	return b.String(), out, nil
}

// ---- changes ----

type ChangesIn struct {
	Scope
	MinRuns          *int `json:"min_runs,omitempty" jsonschema:"runs required on each side of a change before judging it, at least 1; default 3"`
	IncludeUndecided bool `json:"include_undecided,omitempty" jsonschema:"also return changes with too few runs on one side to judge; default false"`
}

type SideOut struct {
	Runs      int     `json:"runs"`
	AvgUSD    float64 `json:"avg_usd" jsonschema:"mean cost per run"`
	ErrorRate float64 `json:"error_rate" jsonschema:"errored tool results over all tool results, 0..1"`
	AvgTurns  float64 `json:"avg_turns"`
}

type ChangeOut struct {
	Agent   string  `json:"agent"`
	From    string  `json:"from" jsonschema:"canonical model id before the change"`
	To      string  `json:"to" jsonschema:"canonical model id after the change"`
	At      string  `json:"at" jsonschema:"RFC 3339 time of the first run on the new model"`
	Before  SideOut `json:"before"`
	After   SideOut `json:"after"`
	Verdict string  `json:"verdict" jsonschema:"keep, watch, revert, or \"too early\""`
	MinRuns int     `json:"min_runs" jsonschema:"the threshold this change was judged against"`
}

type ChangesOut struct {
	Money
	Freshness
	MinRuns   int         `json:"min_runs"`
	Changes   []ChangeOut `json:"changes"`
	Undecided int         `json:"undecided" jsonschema:"changes with too few runs to judge, whether or not they are included"`
}

func (a *app) changes(in ChangesIn) (string, ChangesOut, error) {
	sc, err := a.newScope(in.Scope)
	if err != nil {
		return "", ChangesOut{}, err
	}
	minRuns := 3
	if in.MinRuns != nil {
		minRuns = *in.MinRuns
	}
	if minRuns < 1 {
		return "", ChangesOut{}, fmt.Errorf("min_runs must be at least 1, got %d", minRuns)
	}
	cs, err := ledger.Changes(a.st, a.prices, sc.filter, minRuns)
	if err != nil {
		return "", ChangesOut{}, err
	}

	out := ChangesOut{Money: money(sc), Freshness: a.freshness(), MinRuns: minRuns, Changes: []ChangeOut{}}
	var b strings.Builder
	fmt.Fprintln(&b, moneyNote(sc))
	fmt.Fprintln(&b, "Measured, not estimated: both sides of each change are real runs.")
	for _, c := range cs {
		if c.Verdict == ledger.VerdictTooEarly {
			out.Undecided++
			if !in.IncludeUndecided {
				continue
			}
		}
		out.Changes = append(out.Changes, ChangeOut{
			Agent: c.Agent, From: c.From, To: c.To, At: timeString(c.At),
			Before:  SideOut{Runs: c.Before.Runs, AvgUSD: c.Before.AvgUSD, ErrorRate: c.Before.ErrorRate, AvgTurns: c.Before.AvgTurns},
			After:   SideOut{Runs: c.After.Runs, AvgUSD: c.After.AvgUSD, ErrorRate: c.After.ErrorRate, AvgTurns: c.After.AvgTurns},
			Verdict: string(c.Verdict), MinRuns: c.MinRuns,
		})
		fmt.Fprintf(&b, "%s: %s to %s on %s: %s. Before %d runs at %s each, %.0f%% errors. After %d runs at %s each, %.0f%% errors.\n",
			c.Agent, c.From, c.To, c.At.Format("2 Jan 2006"), c.Verdict,
			c.Before.Runs, fmtUSD(c.Before.AvgUSD), c.Before.ErrorRate*100,
			c.After.Runs, fmtUSD(c.After.AvgUSD), c.After.ErrorRate*100)
	}
	if len(out.Changes) == 0 {
		fmt.Fprintln(&b, "No model changes to judge in this window.")
	}
	if out.Undecided > 0 && !in.IncludeUndecided {
		fmt.Fprintf(&b, "%d change(s) have fewer than %d runs on one side and are not shown; pass include_undecided to see them.\n", out.Undecided, minRuns)
	}
	return b.String(), out, nil
}

// ---- agents ----

type AgentsIn struct {
	Scope
}

type AgentOut struct {
	Agent         string  `json:"agent" jsonschema:"sub-agent type name"`
	Runs          int     `json:"runs"`
	Model         string  `json:"model" jsonschema:"the model most of its runs used"`
	AvgUSD        float64 `json:"avg_usd" jsonschema:"mean cost per run"`
	ReadOnlyShare float64 `json:"read_only_share" jsonschema:"share of runs in which every tool call only read, 0..1"`
	Errors        int     `json:"errors" jsonschema:"errored tool results across all runs"`
	Mismatched    int     `json:"mismatched" jsonschema:"runs where the requested model differed from the one actually used"`
}

type AgentsOut struct {
	Money
	Freshness
	Agents []AgentOut `json:"agents"`
}

func (a *app) agents(in AgentsIn) (string, AgentsOut, error) {
	sc, err := a.newScope(in.Scope)
	if err != nil {
		return "", AgentsOut{}, err
	}
	rows, err := ledger.Agents(a.st, a.prices, sc.filter)
	if err != nil {
		return "", AgentsOut{}, err
	}
	out := AgentsOut{Money: money(sc), Freshness: a.freshness(), Agents: []AgentOut{}}
	var b strings.Builder
	fmt.Fprintln(&b, moneyNote(sc))
	for _, r := range rows {
		out.Agents = append(out.Agents, AgentOut{
			Agent: r.Agent, Runs: r.Runs, Model: r.Model, AvgUSD: r.AvgUSD,
			ReadOnlyShare: r.ReadOnlyPct, Errors: r.Errors, Mismatched: r.Mismatched,
		})
		fmt.Fprintf(&b, "%s: %d runs on %s, %s per run, %.0f%% of runs read-only, %d errors, %d requested-model mismatches\n",
			r.Agent, r.Runs, r.Model, fmtUSD(r.AvgUSD), r.ReadOnlyPct*100, r.Errors, r.Mismatched)
	}
	if len(rows) == 0 {
		fmt.Fprintln(&b, "No sub-agent runs in this window.")
	}
	return b.String(), out, nil
}

// ---- sessions ----

type SessionsIn struct {
	Scope
	Sort  string `json:"sort,omitempty" jsonschema:"\"cost\" for most expensive first or \"time\" for most recent first; default time"`
	Limit *int   `json:"limit,omitempty" jsonschema:"maximum rows to return; default 20; 0 returns every row"`
}

type SessionOut struct {
	ID              string  `json:"id"`
	Source          string  `json:"source"`
	Project         string  `json:"project"`
	StartedAt       string  `json:"started_at"`
	EndedAt         string  `json:"ended_at"`
	Turns           int     `json:"turns"`
	ToolCalls       int     `json:"tool_calls"`
	ToolErrors      int     `json:"tool_errors"`
	USD             float64 `json:"usd"`
	Known           bool    `json:"known" jsonschema:"false if a turn used a model with no price, so usd is a floor"`
	AgentType       string  `json:"agent_type,omitempty" jsonschema:"set for sub-agent runs"`
	ParentSessionID string  `json:"parent_session_id,omitempty" jsonschema:"set for sub-agent runs"`
}

type SessionsOut struct {
	Money
	Freshness
	Total    int          `json:"total" jsonschema:"rows in the window before the limit"`
	Sessions []SessionOut `json:"sessions"`
}

func (a *app) sessions(in SessionsIn) (string, SessionsOut, error) {
	sc, err := a.newScope(in.Scope)
	if err != nil {
		return "", SessionsOut{}, err
	}
	sortBy := in.Sort
	if sortBy == "" {
		sortBy = "time"
	}
	if sortBy != "cost" && sortBy != "time" {
		return "", SessionsOut{}, fmt.Errorf("sort must be \"cost\" or \"time\", got %q", in.Sort)
	}
	limit := 20
	if in.Limit != nil {
		limit = *in.Limit // 0 or negative means every row, as on the CLI
	}

	rows, err := ledger.Sessions(a.st, a.prices, sc.filter)
	if err != nil {
		return "", SessionsOut{}, err
	}
	switch sortBy {
	case "cost":
		sort.SliceStable(rows, func(i, j int) bool { return rows[i].USD > rows[j].USD })
	default:
		sort.SliceStable(rows, func(i, j int) bool { return rows[i].StartedAt.After(rows[j].StartedAt) })
	}

	out := SessionsOut{Money: money(sc), Freshness: a.freshness(), Total: len(rows), Sessions: []SessionOut{}}
	shown := rows
	if limit > 0 && limit < len(rows) {
		shown = rows[:limit]
	}
	var b strings.Builder
	fmt.Fprintln(&b, moneyNote(sc))
	fmt.Fprintf(&b, "%d of %d sessions, sorted by %s:\n", len(shown), len(rows), sortBy)
	for _, r := range shown {
		out.Sessions = append(out.Sessions, SessionOut{
			ID: r.ID, Source: string(r.Source), Project: r.Project,
			StartedAt: timeString(r.StartedAt), EndedAt: timeString(r.EndedAt),
			Turns: r.Turns, ToolCalls: r.ToolCalls, ToolErrors: r.ToolErrors,
			USD: r.USD, Known: r.Known, AgentType: r.AgentType, ParentSessionID: r.ParentSessionID,
		})
		label := r.ID
		if r.AgentType != "" {
			label += " (" + r.AgentType + ")"
		}
		fmt.Fprintf(&b, "  %s %s %s %d turns %s\n", r.StartedAt.Format("2006-01-02 15:04"), report.SourceLabel(r.Source), label, r.Turns, fmtUSD(r.USD))
	}
	return b.String(), out, nil
}

// ---- session ----

type SessionIn struct {
	ID       string `json:"id" jsonschema:"full session id or a unique prefix"`
	Currency string `json:"currency,omitempty" jsonschema:"override the detected plan: \"usd\" or \"share\""`
}

type TurnOut struct {
	Index        int     `json:"index"`
	Time         string  `json:"time"`
	Model        string  `json:"model" jsonschema:"model id as written in the transcript"`
	Effort       string  `json:"effort,omitempty"`
	Input        int64   `json:"input" jsonschema:"uncached input tokens"`
	CacheRead    int64   `json:"cache_read"`
	CacheWrite5m int64   `json:"cache_write_5m"`
	CacheWrite1h int64   `json:"cache_write_1h"`
	Output       int64   `json:"output" jsonschema:"output tokens including thinking"`
	Thinking     int64   `json:"thinking"`
	TextChars    int     `json:"text_chars" jsonschema:"characters of visible assistant text; the text itself is not stored"`
	ToolCalls    int     `json:"tool_calls"`
	USD          float64 `json:"usd"`
	Known        bool    `json:"known" jsonschema:"false when the model has no price and usd is 0"`
}

type SessionDetailOut struct {
	Money
	Freshness
	ID        string    `json:"id"`
	Source    string    `json:"source"`
	Project   string    `json:"project"`
	AgentType string    `json:"agent_type,omitempty"`
	StartedAt string    `json:"started_at"`
	EndedAt   string    `json:"ended_at"`
	USD       float64   `json:"usd"`
	Turns     []TurnOut `json:"turns"`
}

func (a *app) session(in SessionIn) (string, SessionDetailOut, error) {
	sc, err := a.newScope(Scope{Currency: in.Currency})
	if err != nil {
		return "", SessionDetailOut{}, err
	}
	id, err := a.resolveSessionID(in.ID)
	if err != nil {
		return "", SessionDetailOut{}, err
	}
	row, err := a.st.Session(id)
	if err != nil {
		return "", SessionDetailOut{}, err
	}
	if row == nil {
		return "", SessionDetailOut{}, fmt.Errorf("no session %q", id)
	}
	turns, err := a.st.Turns(id)
	if err != nil {
		return "", SessionDetailOut{}, err
	}

	out := SessionDetailOut{
		Money: money(sc), Freshness: a.freshness(),
		ID: row.ID, Source: string(row.Source), Project: row.Project, AgentType: row.AgentType,
		StartedAt: timeString(row.StartedAt), EndedAt: timeString(row.EndedAt),
		Turns: []TurnOut{},
	}
	for i, t := range turns {
		usd, known := a.prices.CostAt(t.Model, t.Usage, t.Timestamp)
		out.USD += usd
		out.Turns = append(out.Turns, TurnOut{
			Index: i + 1, Time: timeString(t.Timestamp), Model: t.Model, Effort: t.Effort,
			Input: t.Usage.Input, CacheRead: t.Usage.CacheRead,
			CacheWrite5m: t.Usage.CacheWrite5m, CacheWrite1h: t.Usage.CacheWrite1h,
			Output: t.Usage.Output, Thinking: t.Usage.Thinking,
			TextChars: t.TextChars, ToolCalls: len(t.ToolCalls), USD: usd, Known: known,
		})
	}

	var b strings.Builder
	fmt.Fprintln(&b, moneyNote(sc))
	fmt.Fprintf(&b, "Session %s (%s) in %s: %d turns, %s total.\n", row.ID, row.Source, row.Project, len(turns), fmtUSD(out.USD))
	for _, t := range out.Turns {
		fmt.Fprintf(&b, "  %d. %s %s in %d cached %d out %d %s\n", t.Index, t.Time, t.Model, t.Input, t.CacheRead, t.Output, fmtUSD(t.USD))
	}
	return b.String(), out, nil
}

// ---- prices ----

type PricesIn struct{}

type PriceOut struct {
	ID           string  `json:"id"`
	Input        float64 `json:"input"`
	CacheRead    float64 `json:"cache_read"`
	CacheWrite5m float64 `json:"cache_write_5m"`
	CacheWrite1h float64 `json:"cache_write_1h"`
	Output       float64 `json:"output"`
}

type PricesOut struct {
	Freshness
	Unit     string     `json:"unit" jsonschema:"always usd_per_million_tokens"`
	Verified string     `json:"verified" jsonschema:"the date the built-in table was last checked against the vendors' price pages"`
	Models   []PriceOut `json:"models"`
}

func (a *app) pricesTool(PricesIn) (string, PricesOut, error) {
	out := PricesOut{Freshness: a.freshness(), Unit: "usd_per_million_tokens", Verified: a.prices.Dated(), Models: []PriceOut{}}
	var b strings.Builder
	fmt.Fprintf(&b, "Prices in US dollars per million tokens, verified %s.\n", out.Verified)
	for _, id := range a.prices.Models() {
		r, ok := a.prices.Lookup(id)
		if !ok {
			continue
		}
		out.Models = append(out.Models, PriceOut{
			ID: id, Input: r.Input, CacheRead: r.CacheRead,
			CacheWrite5m: r.CacheWrite5m, CacheWrite1h: r.CacheWrite1h, Output: r.Output,
		})
		fmt.Fprintf(&b, "  %s: in %.2f, cache read %.2f, cache write %.2f/%.2f, out %.2f\n",
			id, r.Input, r.CacheRead, r.CacheWrite5m, r.CacheWrite1h, r.Output)
	}
	return b.String(), out, nil
}

// ---- refresh ----

type RefreshIn struct{}

type RefreshOut struct {
	Freshness
	Scanned   int      `json:"scanned" jsonschema:"transcript files found"`
	Ingested  int      `json:"ingested" jsonschema:"files newly recorded or re-recorded because they changed"`
	Unchanged int      `json:"unchanged"`
	Failed    int      `json:"failed" jsonschema:"files that could not be parsed"`
	Errors    []string `json:"errors" jsonschema:"one message per failed file"`
	ElapsedMS int64    `json:"elapsed_ms"`
}

func (a *app) refresh(RefreshIn) (string, RefreshOut, error) {
	if err := a.ingest(); err != nil {
		return "", RefreshOut{}, err
	}
	r := a.ingestResult("")
	out := RefreshOut{
		Freshness: a.freshness(),
		Scanned:   r.Scanned, Ingested: r.Ingested, Unchanged: r.Unchanged, Failed: r.Failed,
		Errors: r.Errors, ElapsedMS: r.Elapsed.Milliseconds(),
	}
	if out.Errors == nil {
		out.Errors = []string{}
	}
	text := fmt.Sprintf("Scanned %d transcript files in %dms: %d new or changed, %d unchanged, %d failed.\n",
		r.Scanned, out.ElapsedMS, r.Ingested, r.Unchanged, r.Failed)
	return text, out, nil
}
