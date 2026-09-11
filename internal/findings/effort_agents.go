package findings

import (
	"fmt"
	"sort"
	"strings"

	"github.com/magna-nz/tallybook/internal/model"
)

// Thresholds below will move into config.Findings alongside the other rules'
// knobs once that wiring lands; for now they are fixed here.
const (
	// effortMinRuns is the fewest qualifying runs of one agent type worth
	// reporting.
	effortMinRuns = 3
	// effortSavedShare is the assumed fraction of thinking spend a run gives
	// up by moving from a high effort level to medium. It is a guess, not a
	// measurement, which is why the saving it produces is estimated and the
	// finding says so.
	effortSavedShare = 0.5
)

// effortAgentsRule finds sub-agent types whose runs never touched anything
// (model.AllReadOnly) but still ran at the harness's highest thinking
// effort levels. Reading and summarising does not need the deepest
// reasoning a run can buy, so the effort setting on these agents is paying
// for thinking their job never uses.
type effortAgentsRule struct{}

func (effortAgentsRule) ID() string { return "effort-on-read-only-agents" }

// effortGroup is every qualifying read-only run of one agent type.
type effortGroup struct {
	agentType      string
	runs           int
	efforts        map[string]int // effort level -> number of runs at that level
	thinkingTokens int64
	thinkingCost   float64
}

func (g *effortGroup) saving() float64 { return g.thinkingCost * effortSavedShare }

// topEffort is the effort level most of a group's runs used, breaking ties
// alphabetically so the answer is stable between runs.
func (g *effortGroup) topEffort() string {
	best, bestN := "", 0
	for e, n := range g.efforts {
		if n > bestN || (n == bestN && e < best) {
			best, bestN = e, n
		}
	}
	return best
}

func (r effortAgentsRule) Run(in Input) (*Finding, error) {
	rows, err := in.Store.Sessions(in.Filter)
	if err != nil {
		return nil, err
	}
	counts, err := in.Store.ToolCounts(in.Filter)
	if err != nil {
		return nil, err
	}

	byType := map[string]*effortGroup{}
	for _, row := range rows {
		if row.AgentID == "" {
			continue // only sub-agent transcripts
		}
		if !model.AllReadOnly(counts[row.ID]) {
			continue
		}
		turns, err := in.Store.Turns(row.ID)
		if err != nil {
			return nil, err
		}

		effort, ok := runEffort(turns)
		if !ok {
			continue
		}

		typ := row.AgentType
		if typ == "" {
			typ = "unnamed"
		}
		g := byType[typ]
		if g == nil {
			g = &effortGroup{agentType: typ, efforts: map[string]int{}}
			byType[typ] = g
		}
		g.runs++
		g.efforts[effort]++
		for _, t := range turns {
			g.thinkingTokens += t.Usage.Thinking
			if rate, ok := in.Prices.LookupAt(t.Model, t.Timestamp); ok {
				g.thinkingCost += float64(t.Usage.Thinking) * rate.Output / 1e6
			}
		}
	}

	var groups []*effortGroup
	for _, g := range byType {
		if g.runs >= effortMinRuns {
			groups = append(groups, g)
		}
	}
	if len(groups) == 0 {
		return nil, nil
	}
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].saving() != groups[j].saving() {
			return groups[i].saving() > groups[j].saving()
		}
		return groups[i].agentType < groups[j].agentType
	})

	var saving float64
	totalRuns := 0
	for _, g := range groups {
		saving += g.saving()
		totalRuns += g.runs
	}
	if saving < 1 {
		return nil, nil
	}

	f := &Finding{
		Direction:  Effort,
		Confidence: Low,
		SavingUSD:  in.PerMonth(saving),
		Title:      "Read-only agents are thinking at the highest effort",
	}
	f.SavingShare = in.Share(saving)

	var types, efforts []string
	seenType, seenEffort := map[string]bool{}, map[string]bool{}
	for _, g := range groups {
		if !seenType[g.agentType] {
			seenType[g.agentType] = true
			types = append(types, g.agentType)
		}
		if e := g.topEffort(); !seenEffort[e] {
			seenEffort[e] = true
			efforts = append(efforts, e)
		}
	}
	f.WhatHappened = fmt.Sprintf(
		"%s times %s, a %s agent did nothing but read and search, never editing anything or running a command "+
			"that changed the project, yet ran at %s effort the whole time.",
		fmtInt(int64(totalRuns)), windowPhrase(in), joinList(types), joinList(efforts))

	f.WhyItCosts = "Reading and summarising is not work that needs the deepest reasoning a model can do. " +
		"The saving assumes dropping to medium effort roughly halves the thinking tokens these runs spend; " +
		"the real drop depends on the agent, but the direction does not."

	f.WhatToChange = effortWhatToChange(in, groups)
	f.WhatToExpect = "These runs should spend less on thinking and come back sooner. " +
		"If their reports start missing things, raise the effort back to high."
	f.Patch = effortPatch(in, groups)
	f.Evidence = effortEvidence(groups)
	return f, nil
}

// runEffort reports the single effort level a run's turns agree on, when
// every turn that recorded an effort recorded the same one and it was
// "high", "xhigh" or "max". A run with no effort recorded on any turn, or
// with a mix of levels, is not something this rule can say anything about.
func runEffort(turns []model.Turn) (string, bool) {
	seen := map[string]bool{}
	for _, t := range turns {
		if t.Effort != "" {
			seen[t.Effort] = true
		}
	}
	if len(seen) != 1 {
		return "", false
	}
	var effort string
	for e := range seen {
		effort = e
	}
	switch effort {
	case "high", "xhigh", "max":
		return effort, true
	}
	return "", false
}

// effortWhatToChange writes the change to make, checked against what the
// project's agent files already say. It never proposes a session-wide effort
// setting: that would slow down the main session along with the agent.
//
// Mechanism verified 2026-09-11 against code.claude.com/docs/en/sub-agents.
func effortWhatToChange(in Input, groups []*effortGroup) string {
	var parts []string
	for _, g := range groups {
		if def, ok := in.Agents.Lookup(g.agentType); ok {
			parts = append(parts, fmt.Sprintf(
				"In %s add this line to the block at the top of the file:\n\n    effort: medium\n",
				displayPath(def.Path)))
			continue
		}
		parts = append(parts, fmt.Sprintf(
			"`~/.claude/agents/%s.md` does not exist. Creating one with an `effort: medium` line is the change.",
			g.agentType))
	}
	parts = append(parts, "The effort key accepts low, medium, high, xhigh and max. "+
		"Codex has no per-agent effort setting.")
	return strings.TrimRight(strings.Join(parts, "\n\n"), "\n")
}

func effortPatch(in Input, groups []*effortGroup) string {
	var b strings.Builder
	for _, g := range groups {
		if def, ok := in.Agents.Lookup(g.agentType); ok {
			b.WriteString(frontmatterPatch(displayPath(def.Path), "effort: medium"))
		}
	}
	return b.String()
}

func effortEvidence(groups []*effortGroup) Table {
	t := Table{Columns: []string{"agent", "runs", "effort", "thinking tokens", "thinking cost", "saving"}}
	for _, g := range groups {
		t.Rows = append(t.Rows, []string{
			g.agentType,
			fmtInt(int64(g.runs)),
			g.topEffort(),
			fmtInt(g.thinkingTokens),
			fmtUSD(g.thinkingCost),
			fmtUSD(g.saving()),
		})
	}
	return t
}
