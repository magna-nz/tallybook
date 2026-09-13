package query

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/magna-nz/tallybook/internal/findings"
	"github.com/magna-nz/tallybook/internal/ledger"
	"github.com/magna-nz/tallybook/internal/model"
	"github.com/magna-nz/tallybook/internal/pricing"
	"github.com/magna-nz/tallybook/internal/report"
	"github.com/magna-nz/tallybook/internal/store"
)

// Scope is the set of inputs every window-based query accepts. Each field is
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

// ---- report ----

// ReportIn is the input to Report.
type ReportIn struct {
	Scope
	Compare bool `json:"compare,omitempty" jsonschema:"also return the window of the same length before this one, so the caller can say whether spend rose or fell; not allowed with since=all"`
}

// WindowOut describes the span an answer covers.
type WindowOut struct {
	Since string  `json:"since,omitempty" jsonschema:"start of the window, RFC 3339; empty for all time"`
	Until string  `json:"until" jsonschema:"end of the window, RFC 3339"`
	Days  float64 `json:"days"`
	Label string  `json:"label"`
}

// SourceOut is the slice of a report that came from one tool.
type SourceOut struct {
	USD      float64 `json:"usd"`
	Sessions int     `json:"sessions" jsonschema:"main sessions, not sub-agent runs"`
	Turns    int     `json:"turns"`
}

// FindingSummary is one line of the report's findings list, enough to decide
// whether to ask for the finding in full.
type FindingSummary struct {
	ID         string  `json:"id" jsonschema:"stable slug; pass it to the finding tool"`
	Title      string  `json:"title"`
	SavingUSD  float64 `json:"saving_usd" jsonschema:"estimated saving per 30 days at list price; 0 for info findings"`
	Share      float64 `json:"share" jsonschema:"the saving as a fraction of the window's total, 0..1"`
	Confidence string  `json:"confidence" jsonschema:"high, medium, low, or info"`
	Direction  string  `json:"direction" jsonschema:"downgrade, upgrade, context, cache, effort, or config"`
}

// PriorOut is the window before the report's own, returned when compare is
// set. Its figures are in the same units as the top-level ones.
type PriorOut struct {
	Window       WindowOut `json:"window"`
	Sessions     int       `json:"sessions" jsonschema:"main sessions in the prior window"`
	Subagents    int       `json:"subagents"`
	USD          float64   `json:"usd"`
	MainUSD      float64   `json:"main_usd"`
	SubagentUSD  float64   `json:"subagent_usd"`
	CacheHitRate float64   `json:"cache_hit_rate" jsonschema:"share of input-side tokens read back from the prompt cache, 0..1"`
}

// ReportOut is what a window cost, split by tool and model, with the
// findings about what would have been cheaper.
type ReportOut struct {
	Money
	Freshness
	Window        WindowOut            `json:"window"`
	CacheHitRate  float64              `json:"cache_hit_rate" jsonschema:"share of everything sent to the model that was read back from the prompt cache rather than processed afresh, 0..1"`
	Prior         *PriorOut            `json:"prior,omitempty" jsonschema:"the window before this one; present only when compare was set"`
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
	ByDay         []DayOut             `json:"by_day" jsonschema:"spend per calendar day in the window, by each session's start date in local time; days with no sessions are absent"`
}

// DayOut is one day of the report's spend series.
type DayOut struct {
	Date     string  `json:"date" jsonschema:"YYYY-MM-DD, local time"`
	USD      float64 `json:"usd"`
	Sessions int     `json:"sessions" jsonschema:"rows that started that day, sub-agent runs included"`
}

// byDay buckets sessions by the local calendar day they started, sorted by
// date. It is computed from the rows the report already priced, so the page
// gets its chart without a second pass over the ledger.
func byDay(sessions []ledger.SessionCost) []DayOut {
	buckets := map[string]*DayOut{}
	for _, s := range sessions {
		if s.StartedAt.IsZero() {
			continue
		}
		key := s.StartedAt.Local().Format("2006-01-02")
		d, ok := buckets[key]
		if !ok {
			d = &DayOut{Date: key}
			buckets[key] = d
		}
		d.USD += s.USD
		d.Sessions++
	}
	out := make([]DayOut, 0, len(buckets))
	for _, d := range buckets {
		out = append(out, *d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date < out[j].Date })
	return out
}

// Report is what the coding agents on this machine cost over a window. It
// returns the prose a model reads and the structured value a client computes
// on. The caller must hold the lock.
func (a *App) Report(in ReportIn) (string, ReportOut, error) {
	sc, err := a.NewScope(in.Scope)
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
	var prior *PriorOut
	if in.Compare {
		prior, err = a.priorWindow(sc)
		if err != nil {
			return "", ReportOut{}, err
		}
	}

	out := ReportOut{
		Money:     money(sc),
		Freshness: a.Freshness(),
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
		SkippedFiles:  a.IngestResult(sc.filter.Source).Failed,
		CacheHitRate:  totals.CacheHitRate(),
		Prior:         prior,
		ByDay:         byDay(sessions),
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
	fmt.Fprintln(&b, MoneyNote(sc))
	fmt.Fprintf(&b, "Window: %s (%s, %.1f days). Data scanned at %s.\n", sc.sinceFlag, sc.window.Label, sc.window.Days, out.IngestedAt)
	if len(sessions) == 0 {
		fmt.Fprintln(&b, "No sessions in this window.")
		return b.String(), out, nil
	}
	fmt.Fprintf(&b, "Total %s across %d sessions and %d sub-agent runs (%d turns). Main %s, sub-agents %s.\n",
		FmtUSD(totals.USD), totals.Sessions-totals.Subagents, totals.Subagents, totals.Turns, FmtUSD(totals.MainUSD), FmtUSD(totals.SubagentUSD))
	fmt.Fprintf(&b, "Cache hit rate %.0f%%: that share of everything sent was read back from the prompt cache.\n", out.CacheHitRate*100)
	if prior != nil {
		fmt.Fprint(&b, priorSentence(out, prior))
	}
	for _, src := range sortedSources(totals.BySource) {
		st := totals.BySource[src]
		fmt.Fprintf(&b, "  %s: %s over %d sessions\n", report.SourceLabel(src), FmtUSD(st.USD), st.Sessions)
	}
	for _, id := range sortedByValue(totals.ByModel) {
		fmt.Fprintf(&b, "  %s: %s\n", id, FmtUSD(totals.ByModel[id]))
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
			fmt.Fprintf(&b, "  [%s] %s: about %s per 30 days, %s confidence\n", f.ID, f.Title, FmtUSD(f.SavingUSD), f.Confidence)
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

// FindingIn addresses one finding within a window.
type FindingIn struct {
	Scope
	ID string `json:"id" jsonschema:"the finding's id from report, such as readonly-agent-on-strong-model"`
}

// EvidenceOut is the table a finding rests on, as columns and rows of text.
type EvidenceOut struct {
	Columns []string   `json:"columns"`
	Rows    [][]string `json:"rows"`
}

// FindingOut is one finding in full: what happened, why it costs, what to
// change, what to expect, the evidence, and the patch if there is a file to
// change.
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

// Finding returns one finding by its stable id, or an error naming the ids
// that are available in this window. The caller must hold the lock.
func (a *App) Finding(in FindingIn) (string, FindingOut, error) {
	if in.ID == "" {
		return "", FindingOut{}, badInput("id is required; call report for the list of finding ids")
	}
	sc, err := a.NewScope(in.Scope)
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
			return "", FindingOut{}, notFound("no finding %q: there are no findings in this window", in.ID)
		}
		return "", FindingOut{}, notFound("no finding %q in this window; available: %s", in.ID, strings.Join(ids, ", "))
	}

	out := FindingOut{
		Money:        money(sc),
		Freshness:    a.Freshness(),
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
	fmt.Fprintln(&b, MoneyNote(sc))
	fmt.Fprintf(&b, "[%s] %s\n", f.ID, f.Title)
	if f.Confidence == findings.Info {
		fmt.Fprintln(&b, "Info: nothing to save, a pointer rather than a recommendation.")
	} else {
		fmt.Fprintf(&b, "Estimated saving about %s per 30 days (%.0f%% of the window), %s confidence, direction %s.\n",
			FmtUSD(f.SavingUSD), f.SavingShare*100, f.Confidence, f.Direction)
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

// ChangesIn narrows which model changes are judged and shown.
type ChangesIn struct {
	Scope
	MinRuns          *int `json:"min_runs,omitempty" jsonschema:"runs required on each side of a change before judging it, at least 1; default 3"`
	IncludeUndecided bool `json:"include_undecided,omitempty" jsonschema:"also return changes with too few runs on one side to judge; default false"`
}

// SideOut is one side of a model change: the real runs before it, or after.
type SideOut struct {
	Runs      int     `json:"runs"`
	AvgUSD    float64 `json:"avg_usd" jsonschema:"mean cost per run"`
	ErrorRate float64 `json:"error_rate" jsonschema:"errored tool results over all tool results, 0..1"`
	AvgTurns  float64 `json:"avg_turns"`
}

// ChangeOut is one point where a sub-agent's model changed, with both sides
// measured and a verdict.
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

// ChangesOut is every model change in the window.
type ChangesOut struct {
	Money
	Freshness
	MinRuns   int         `json:"min_runs"`
	Changes   []ChangeOut `json:"changes"`
	Undecided int         `json:"undecided" jsonschema:"changes with too few runs to judge, whether or not they are included"`
}

// Changes compares the real runs on either side of every model change in the
// window. These are measured figures, not estimates. The caller must hold
// the lock.
func (a *App) Changes(in ChangesIn) (string, ChangesOut, error) {
	sc, err := a.NewScope(in.Scope)
	if err != nil {
		return "", ChangesOut{}, err
	}
	minRuns := 3
	if in.MinRuns != nil {
		minRuns = *in.MinRuns
	}
	if minRuns < 1 {
		return "", ChangesOut{}, badInput("min_runs must be at least 1, got %d", minRuns)
	}
	cs, err := ledger.Changes(a.st, a.prices, sc.filter, minRuns)
	if err != nil {
		return "", ChangesOut{}, err
	}

	out := ChangesOut{Money: money(sc), Freshness: a.Freshness(), MinRuns: minRuns, Changes: []ChangeOut{}}
	var b strings.Builder
	fmt.Fprintln(&b, MoneyNote(sc))
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
			c.Before.Runs, FmtUSD(c.Before.AvgUSD), c.Before.ErrorRate*100,
			c.After.Runs, FmtUSD(c.After.AvgUSD), c.After.ErrorRate*100)
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

// AgentsIn is the window the per-agent spend is summed over.
type AgentsIn struct {
	Scope
}

// AgentOut is one sub-agent type's spend and behaviour over the window.
type AgentOut struct {
	Agent         string  `json:"agent" jsonschema:"sub-agent type name"`
	Runs          int     `json:"runs"`
	Model         string  `json:"model" jsonschema:"the model most of its runs used"`
	Effort        string  `json:"effort,omitempty" jsonschema:"the effort level most of its turns ran at; empty when the transcripts record none"`
	AvgUSD        float64 `json:"avg_usd" jsonschema:"mean cost per run"`
	ReadOnlyShare float64 `json:"read_only_share" jsonschema:"share of runs in which every tool call only read, 0..1"`
	Errors        int     `json:"errors" jsonschema:"errored tool results across all runs"`
	Mismatched    int     `json:"mismatched" jsonschema:"runs where the requested model differed from the one actually used"`
}

// AgentsOut is the spend per sub-agent type.
type AgentsOut struct {
	Money
	Freshness
	Agents []AgentOut `json:"agents"`
}

// Agents reports what each sub-agent type cost and how its runs behaved. The
// caller must hold the lock.
func (a *App) Agents(in AgentsIn) (string, AgentsOut, error) {
	sc, err := a.NewScope(in.Scope)
	if err != nil {
		return "", AgentsOut{}, err
	}
	rows, err := ledger.Agents(a.st, a.prices, sc.filter)
	if err != nil {
		return "", AgentsOut{}, err
	}
	out := AgentsOut{Money: money(sc), Freshness: a.Freshness(), Agents: []AgentOut{}}
	var b strings.Builder
	fmt.Fprintln(&b, MoneyNote(sc))
	for _, r := range rows {
		out.Agents = append(out.Agents, AgentOut{
			Agent: r.Agent, Runs: r.Runs, Model: r.Model, Effort: r.Effort, AvgUSD: r.AvgUSD,
			ReadOnlyShare: r.ReadOnlyPct, Errors: r.Errors, Mismatched: r.Mismatched,
		})
		effort := ""
		if r.Effort != "" {
			effort = " at " + r.Effort + " effort"
		}
		fmt.Fprintf(&b, "%s: %d runs on %s%s, %s per run, %.0f%% of runs read-only, %d errors, %d requested-model mismatches\n",
			r.Agent, r.Runs, r.Model, effort, FmtUSD(r.AvgUSD), r.ReadOnlyPct*100, r.Errors, r.Mismatched)
	}
	if len(rows) == 0 {
		fmt.Fprintln(&b, "No sub-agent runs in this window.")
	}
	return b.String(), out, nil
}

// ---- sessions ----

// SessionsIn is the window, order and size of the session list.
type SessionsIn struct {
	Scope
	Sort  string `json:"sort,omitempty" jsonschema:"\"cost\" for most expensive first or \"time\" for most recent first; default time"`
	Limit *int   `json:"limit,omitempty" jsonschema:"maximum rows to return; default 20; 0 returns every row"`
}

// SessionOut is one session or sub-agent run and what it cost.
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

// sessionOut is the one mapping from a priced session to a row, so the list
// and a session's sub-agent rows can never describe the same run
// differently.
func sessionOut(r ledger.SessionCost) SessionOut {
	return SessionOut{
		ID: r.ID, Source: string(r.Source), Project: r.Project,
		StartedAt: timeString(r.StartedAt), EndedAt: timeString(r.EndedAt),
		Turns: r.Turns, ToolCalls: r.ToolCalls, ToolErrors: r.ToolErrors,
		USD: r.USD, Known: r.Known, AgentType: r.AgentType, ParentSessionID: r.ParentSessionID,
	}
}

// SessionsOut is the session list, and how many rows the window held before
// the limit was applied.
type SessionsOut struct {
	Money
	Freshness
	Total    int          `json:"total" jsonschema:"rows in the window before the limit"`
	Sessions []SessionOut `json:"sessions"`
}

// Sessions lists the window's sessions and sub-agent runs with what each
// cost. The caller must hold the lock.
func (a *App) Sessions(in SessionsIn) (string, SessionsOut, error) {
	sc, err := a.NewScope(in.Scope)
	if err != nil {
		return "", SessionsOut{}, err
	}
	sortBy := in.Sort
	if sortBy == "" {
		sortBy = "time"
	}
	if sortBy != "cost" && sortBy != "time" {
		return "", SessionsOut{}, badInput("sort must be \"cost\" or \"time\", got %q", in.Sort)
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

	out := SessionsOut{Money: money(sc), Freshness: a.Freshness(), Total: len(rows), Sessions: []SessionOut{}}
	shown := rows
	if limit > 0 && limit < len(rows) {
		shown = rows[:limit]
	}
	var b strings.Builder
	fmt.Fprintln(&b, MoneyNote(sc))
	fmt.Fprintf(&b, "%d of %d sessions, sorted by %s:\n", len(shown), len(rows), sortBy)
	for _, r := range shown {
		out.Sessions = append(out.Sessions, sessionOut(r))
		label := r.ID
		if r.AgentType != "" {
			label += " (" + r.AgentType + ")"
		}
		fmt.Fprintf(&b, "  %s %s %s %d turns %s\n", r.StartedAt.Format("2006-01-02 15:04"), report.SourceLabel(r.Source), label, r.Turns, FmtUSD(r.USD))
	}
	return b.String(), out, nil
}

// ---- session ----

// SessionIn addresses one session by id or by a unique prefix of one.
type SessionIn struct {
	ID       string `json:"id" jsonschema:"full session id or a unique prefix"`
	Currency string `json:"currency,omitempty" jsonschema:"override the detected plan: \"usd\" or \"share\""`
}

// LaunchOut is one sub-agent launch a turn made. The brief it was given is
// never returned: the store does not hold it.
type LaunchOut struct {
	AgentType      string `json:"agent_type" jsonschema:"sub-agent type name"`
	RequestedModel string `json:"requested_model,omitempty" jsonschema:"the model the call site asked for; empty when it named none"`
	ResolvedModel  string `json:"resolved_model,omitempty" jsonschema:"the model the harness reported it actually used"`
	AgentID        string `json:"agent_id,omitempty" jsonschema:"links to the sub-agent's own session"`
}

// TurnOut is one model response: what it ran on, what it cost, and how big
// the context had grown by then.
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
	// Context is the prompt size the model saw on this turn, which is what
	// a caller needs to draw context growth over a session.
	Context          int64       `json:"context" jsonschema:"prompt tokens the model saw on this turn: input, cache reads and cache writes"`
	CompactionBefore bool        `json:"compaction_before,omitempty" jsonschema:"true when the conversation was compacted between the previous turn and this one"`
	Note             string      `json:"note,omitempty" jsonschema:"\"compacted\" or \"cache rebuilt\" when this turn is one of those, else absent"`
	Launches         []LaunchOut `json:"launches,omitempty" jsonschema:"sub-agents this turn spawned"`
}

// SessionDetailOut is one session turn by turn, plus the sub-agent runs it
// spawned so a caller can follow the spend into them.
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

	ParentSessionID string       `json:"parent_session_id,omitempty" jsonschema:"set when this is itself a sub-agent run"`
	TurnCount       int          `json:"turn_count" jsonschema:"length of turns, so a caller can size the session without walking it"`
	ToolCalls       int          `json:"tool_calls"`
	ToolErrors      int          `json:"tool_errors" jsonschema:"tool results the harness marked as errors"`
	Subagents       []SessionOut `json:"subagents" jsonschema:"the sub-agent runs this session launched; empty for a session that launched none"`
}

// Session returns one session turn by turn. It never returns prompt text,
// tool output or command lines; the database does not hold them. The caller
// must hold the lock.
func (a *App) Session(in SessionIn) (string, SessionDetailOut, error) {
	sc, err := a.NewScope(Scope{Currency: in.Currency})
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
		return "", SessionDetailOut{}, notFound("no session %q", id)
	}
	turns, err := a.st.Turns(id)
	if err != nil {
		return "", SessionDetailOut{}, err
	}
	subagents, err := a.subagentsOf(id)
	if err != nil {
		return "", SessionDetailOut{}, err
	}

	out := SessionDetailOut{
		Money: money(sc), Freshness: a.Freshness(),
		ID: row.ID, Source: string(row.Source), Project: row.Project, AgentType: row.AgentType,
		StartedAt: timeString(row.StartedAt), EndedAt: timeString(row.EndedAt),
		Turns: []TurnOut{},

		ParentSessionID: row.ParentSessionID,
		TurnCount:       len(turns),
		ToolCalls:       row.ToolCalls,
		ToolErrors:      row.ToolErrors,
		Subagents:       subagents,
	}
	var prevContext int64
	for i, t := range turns {
		usd, known := a.prices.CostTurn(t)
		out.USD += usd
		turn := TurnOut{
			Index: i + 1, Time: timeString(t.Timestamp), Model: t.Model, Effort: t.Effort,
			Input: t.Usage.Input, CacheRead: t.Usage.CacheRead,
			CacheWrite5m: t.Usage.CacheWrite5m, CacheWrite1h: t.Usage.CacheWrite1h,
			Output: t.Usage.Output, Thinking: t.Usage.Thinking,
			TextChars: t.TextChars, ToolCalls: len(t.ToolCalls), USD: usd, Known: known,
			Context:          t.Usage.ContextTokens(),
			CompactionBefore: t.CompactionBefore,
			Note:             report.TurnNote(t, prevContext),
		}
		for _, tc := range t.ToolCalls {
			if tc.Agent == nil {
				continue
			}
			turn.Launches = append(turn.Launches, LaunchOut{
				AgentType:      tc.Agent.SubagentType,
				RequestedModel: tc.Agent.RequestedModel,
				ResolvedModel:  tc.Agent.ResolvedModel,
				AgentID:        tc.Agent.AgentID,
			})
		}
		prevContext = t.Usage.ContextTokens()
		out.Turns = append(out.Turns, turn)
	}

	var b strings.Builder
	fmt.Fprintln(&b, MoneyNote(sc))
	fmt.Fprintf(&b, "Session %s (%s) in %s: %d turns, %s total.\n", row.ID, row.Source, row.Project, len(turns), FmtUSD(out.USD))
	for _, t := range out.Turns {
		fmt.Fprintf(&b, "  %d. %s %s in %d cached %d out %d %s\n", t.Index, t.Time, t.Model, t.Input, t.CacheRead, t.Output, FmtUSD(t.USD))
	}
	return b.String(), out, nil
}

// subagentsOf is every run this session launched, priced like any other
// session. The filter is unbounded because a sub-agent's own start time may
// fall outside whatever window the caller asked about, and a session's
// children belong to it regardless. The result is never nil, so a client can
// iterate it without a null check.
func (a *App) subagentsOf(id string) ([]SessionOut, error) {
	rows, err := ledger.Sessions(a.st, a.prices, store.Filter{Parent: id})
	if err != nil {
		return nil, err
	}
	out := []SessionOut{}
	for _, r := range rows {
		out = append(out, sessionOut(r))
	}
	return out, nil
}

// ---- prices ----

// PricesIn takes no inputs: the price table is not window-dependent.
type PricesIn struct{}

// PriceOut is one model's rates in US dollars per million tokens.
type PriceOut struct {
	ID           string  `json:"id"`
	Input        float64 `json:"input"`
	CacheRead    float64 `json:"cache_read"`
	CacheWrite5m float64 `json:"cache_write_5m"`
	CacheWrite1h float64 `json:"cache_write_1h"`
	Output       float64 `json:"output"`
	Override     bool    `json:"override,omitempty" jsonschema:"true when the rate comes from a [prices] table in the user's config"`
	// Premium tiers. Without these a client cannot reconcile a per-turn usd
	// against this table: a fast-mode Opus turn costs exactly double what
	// the flat columns above can explain.
	Fast            *PriceTierOut `json:"fast,omitempty" jsonschema:"the rate this model bills under fast mode, absent when it has none"`
	Long            *PriceTierOut `json:"long,omitempty" jsonschema:"the rate this model bills once the prompt passes long_context_from tokens, absent when it has none"`
	LongContextFrom int64         `json:"long_context_from,omitempty" jsonschema:"prompt size in tokens above which long applies"`
}

// PriceTierOut is one premium tier of a model's rate.
type PriceTierOut struct {
	Input        float64 `json:"input"`
	CacheRead    float64 `json:"cache_read"`
	CacheWrite5m float64 `json:"cache_write_5m"`
	CacheWrite1h float64 `json:"cache_write_1h"`
	Output       float64 `json:"output"`
}

// priceTierOut renders a tier, or nil when the model has none.
func priceTierOut(r *pricing.Rate) *PriceTierOut {
	if r == nil {
		return nil
	}
	return &PriceTierOut{
		Input: r.Input, CacheRead: r.CacheRead,
		CacheWrite5m: r.CacheWrite5m, CacheWrite1h: r.CacheWrite1h, Output: r.Output,
	}
}

// PricesOut is the table every figure in every other answer is computed
// from.
type PricesOut struct {
	Freshness
	Unit     string     `json:"unit" jsonschema:"always usd_per_million_tokens"`
	Verified string     `json:"verified" jsonschema:"the date the built-in table was last checked against the vendors' price pages"`
	Models   []PriceOut `json:"models"`
}

// Prices returns the price table, including the user's overrides. It does
// not scan transcripts: no figure here comes from one. The caller must hold
// the lock.
func (a *App) Prices(PricesIn) (string, PricesOut, error) {
	out := PricesOut{Freshness: a.Freshness(), Unit: "usd_per_million_tokens", Verified: a.prices.Dated(), Models: []PriceOut{}}
	// Table.Set lower-cases the id it is given, so the config's keys are
	// compared in that form rather than as the user spelled them.
	override := make(map[string]bool, len(a.cfg.Prices))
	for id := range a.cfg.Prices {
		override[strings.ToLower(id)] = true
	}
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
			Override:        override[strings.ToLower(id)],
			Fast:            priceTierOut(r.Fast),
			Long:            priceTierOut(r.Long),
			LongContextFrom: r.LongContextFrom,
		})
		fmt.Fprintf(&b, "  %s: in %.2f, cache read %.2f, cache write %.2f/%.2f, out %.2f\n",
			id, r.Input, r.CacheRead, r.CacheWrite5m, r.CacheWrite1h, r.Output)
		if r.Fast != nil {
			fmt.Fprintf(&b, "    in fast mode: in %.2f, cache read %.2f, cache write %.2f/%.2f, out %.2f\n",
				r.Fast.Input, r.Fast.CacheRead, r.Fast.CacheWrite5m, r.Fast.CacheWrite1h, r.Fast.Output)
		}
		if r.Long != nil {
			fmt.Fprintf(&b, "    over %dk tokens: in %.2f, cache read %.2f, cache write %.2f/%.2f, out %.2f\n",
				r.LongContextFrom/1000, r.Long.Input, r.Long.CacheRead,
				r.Long.CacheWrite5m, r.Long.CacheWrite1h, r.Long.Output)
		}
	}
	return b.String(), out, nil
}

// ---- refresh ----

// RefreshIn takes no inputs: a refresh always scans every root.
type RefreshIn struct{}

// RefreshOut is what one scan of the transcript directories found.
type RefreshOut struct {
	Freshness
	Scanned   int      `json:"scanned" jsonschema:"transcript files found"`
	Ingested  int      `json:"ingested" jsonschema:"files newly recorded or re-recorded because they changed"`
	Unchanged int      `json:"unchanged"`
	Failed    int      `json:"failed" jsonschema:"files that could not be parsed"`
	Errors    []string `json:"errors" jsonschema:"one message per failed file"`
	ElapsedMS int64    `json:"elapsed_ms"`
}

// Refresh scans the transcript directories now rather than waiting for the
// throttle to expire. Only tallybook's own database is written. The caller
// must hold the lock.
func (a *App) Refresh(RefreshIn) (string, RefreshOut, error) {
	if err := a.Ingest(); err != nil {
		return "", RefreshOut{}, err
	}
	r := a.IngestResult("")
	out := RefreshOut{
		Freshness: a.Freshness(),
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

// priorWindow builds the compare block: the same filter shifted back one
// window length. An all-time window has nothing before it, which is an error
// for the caller to fix rather than a silent absence.
func (a *App) priorWindow(sc scope) (*PriorOut, error) {
	prior, ok := ledger.Prior(sc.window)
	if !ok {
		return nil, badInput("compare needs a bounded window; since=all has nothing before it")
	}
	filter := sc.filter
	filter.Since, filter.Until = prior.Since, prior.Until
	sessions, err := ledger.Sessions(a.st, a.prices, filter)
	if err != nil {
		return nil, err
	}
	totals, err := ledger.Total(sessions, a.st, a.prices)
	if err != nil {
		return nil, err
	}
	return &PriorOut{
		Window: WindowOut{
			Since: timeString(prior.Since), Until: prior.Until.Format(time.RFC3339),
			Days: prior.Days, Label: prior.Label,
		},
		Sessions:     totals.Sessions - totals.Subagents,
		Subagents:    totals.Subagents,
		USD:          totals.USD,
		MainUSD:      totals.MainUSD,
		SubagentUSD:  totals.SubagentUSD,
		CacheHitRate: totals.CacheHitRate(),
	}, nil
}

// priorSentence is the one line of prose a model reads about the prior
// window. The structured value carries the figures; this carries the
// direction, which is what the question was.
func priorSentence(out ReportOut, prior *PriorOut) string {
	if prior.Sessions == 0 && prior.Subagents == 0 {
		return fmt.Sprintf("Nothing was recorded in the %.0f days before (%s), so there is nothing to compare with.\n",
			prior.Window.Days, prior.Window.Label)
	}
	return fmt.Sprintf("Compared with the %.0f days before (%s): total %s, from %s to %s; sessions %d to %d; cache hit rate %.0f%% to %.0f%%.\n",
		prior.Window.Days, prior.Window.Label, report.ChangePhrase(prior.USD, out.USD),
		FmtUSD(prior.USD), FmtUSD(out.USD), prior.Sessions, out.Sessions,
		prior.CacheHitRate*100, out.CacheHitRate*100)
}
