package findings

import (
	"fmt"
	"sort"
	"strings"

	"github.com/magna-nz/tallybook/internal/pricing"
)

// readOnlyAgentRule finds sub-agents that only read the project but ran on
// the most expensive model available. Reading and summarising is the work a
// cheaper model does about as well, so this is the safest downgrade there is.
type readOnlyAgentRule struct{}

func (readOnlyAgentRule) ID() string { return "readonly-agent-on-strong-model" }

// roGroup is every read-only run of one agent type on one model.
type roGroup struct {
	agentType string
	modelID   string
	alt       string
	runs      int
	tools     map[string]int
	current   float64
	altCost   float64
}

func (g *roGroup) saving() float64 { return g.current - g.altCost }

// strongTier is a tier expensive enough that moving a read-only run off it is
// worth the words. Sonnet and below already are the cheap option.
func strongTier(tier string) bool {
	switch tier {
	case "opus", "fable", "gpt-pro", "gpt":
		return true
	}
	return false
}

func (r readOnlyAgentRule) Run(in Input) (*Finding, error) {
	rows, err := in.Store.Sessions(in.Filter)
	if err != nil {
		return nil, err
	}
	counts, err := in.Store.ToolCounts(in.Filter)
	if err != nil {
		return nil, err
	}

	minRuns := in.Cfg.MinRuns
	if minRuns <= 0 {
		minRuns = 5
	}

	byKey := map[string]*roGroup{}
	for _, row := range rows {
		if row.AgentID == "" {
			continue // only sub-agent transcripts
		}
		if !allReadOnly(counts[row.ID]) {
			continue
		}
		turns, err := in.Store.Turns(row.ID)
		if err != nil {
			return nil, err
		}
		ran := mostCommonModel(turns)
		if ran == "" {
			continue
		}
		canon := canonical(in, ran)
		if !strongTier(pricing.Tier(canon)) {
			continue
		}
		alt := pricing.CheaperAlternative(canon)
		if alt == "" {
			continue
		}

		typ := row.AgentType
		if typ == "" {
			typ = "unnamed"
		}
		key := typ
		g := byKey[key]
		if g == nil {
			g = &roGroup{agentType: typ, modelID: canon, alt: alt, tools: map[string]int{}}
			byKey[key] = g
		}
		g.runs++
		for name, n := range counts[row.ID] {
			g.tools[name] += n
		}
		g.current += costOf(in, turns)
		g.altCost += costAs(in, turns, alt)
	}

	var groups []*roGroup
	for _, g := range byKey {
		if g.runs >= minRuns {
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

	var saving, current, altTotal float64
	totalRuns := 0
	for _, g := range groups {
		saving += g.saving()
		current += g.current
		altTotal += g.altCost
		totalRuns += g.runs
	}
	if saving <= 0 {
		return nil, nil
	}

	f := &Finding{
		Direction:  Downgrade,
		Confidence: Medium,
		SavingUSD:  in.PerMonth(saving),
		Title:      readOnlyTitle(groups),
	}
	if totalRuns >= 10 {
		f.Confidence = High
	}
	var pairs [][2]string
	for _, g := range groups {
		pairs = append(pairs, [2]string{g.modelID, g.alt})
	}
	approximate := crossesTokenizer(pairs)
	f.Confidence = softenedBy(f.Confidence, approximate)
	f.SavingShare = in.Share(saving)

	f.WhatHappened = readOnlyWhatHappened(in, groups, totalRuns)
	f.WhyItCosts = readOnlyWhyItCosts(in, groups[0])
	f.WhatToChange = readOnlyWhatToChange(groups)
	f.WhatToExpect = readOnlyWhatToExpect(groups, current, altTotal)
	if c := tokenizerCaveat(groups[0].modelID, groups[0].alt); c != "" {
		f.WhyItCosts += "\n\n" + c
	}
	f.Patch = readOnlyPatch(groups)
	f.Evidence = readOnlyEvidence(groups)
	return f, nil
}

func readOnlyTitle(groups []*roGroup) string {
	seen := map[string]bool{}
	var names []string
	for _, g := range groups {
		d := modelDisplay(g.modelID)
		if !seen[d] {
			seen[d] = true
			names = append(names, d)
		}
	}
	return "Read-only sub-agents ran on " + joinList(names)
}

func readOnlyWhatHappened(in Input, groups []*roGroup, totalRuns int) string {
	var types []string
	seen := map[string]bool{}
	allTools := map[string]int{}
	for _, g := range groups {
		if !seen[g.agentType] {
			seen[g.agentType] = true
			types = append(types, g.agentType)
		}
		for name, n := range g.tools {
			allTools[name] += n
		}
	}

	s := fmt.Sprintf("%s times %s you launched a %s agent and it only read files and searched. "+
		"It never edited anything or ran a command that changed the project.",
		fmtInt(int64(totalRuns)), windowPhrase(in), joinList(types))

	if names := sortedKeys(allTools); len(names) > 0 {
		s += fmt.Sprintf(" Between them those runs used %s, and nothing else.", joinList(names))
	} else {
		s += " Several of those runs called no tools at all."
	}
	return s
}

func readOnlyWhyItCosts(in Input, top *roGroup) string {
	name := modelDisplay(top.modelID)
	alt := modelDisplay(top.alt)
	ratio := outputRatio(in, top.modelID, top.alt, windowUntil(in))

	rate := "a good deal more than"
	if ratio > 0 {
		rate = fmt.Sprintf("about %sx the rate of", fmtOneDP(ratio))
	}
	return fmt.Sprintf("%s is billed at %s %s for the same tokens. "+
		"Reading and summarising files is work the cheaper model does about as well, "+
		"so you pay the premium without getting the benefit.", name, rate, alt)
}

func readOnlyWhatToChange(groups []*roGroup) string {
	var parts []string
	for _, g := range groups {
		short := shortModelName(g.alt)
		if file := agentFile(g.agentType); file != "" {
			parts = append(parts, fmt.Sprintf(
				"Open %s and add this line to the block at the top of the file:\n\n    model: %s\n",
				file, short))
			continue
		}
		parts = append(parts, fmt.Sprintf(
			"When you launch %s, pass model: %q in the Agent call. This type has no file of its own, "+
				"so the call site is the only place the choice can be made.", g.agentType, short))
	}
	return strings.TrimRight(strings.Join(parts, "\n"), "\n")
}

func readOnlyWhatToExpect(groups []*roGroup, current, alt float64) string {
	share := 0.0
	if current > 0 {
		share = alt / current
	}
	display := capitalise(groups[0].agentType)
	return fmt.Sprintf("%s runs should cost about %s%% of what they do now. "+
		"If their reports start missing things, switch back. "+
		"The next report will show whether the error rate moved.", display, fmtPct(share))
}

func readOnlyPatch(groups []*roGroup) string {
	var b strings.Builder
	for _, g := range groups {
		if file := agentFile(g.agentType); file != "" {
			b.WriteString(frontmatterPatch(file, "model: "+shortModelName(g.alt)))
		}
	}
	return b.String()
}

func readOnlyEvidence(groups []*roGroup) Table {
	altHeader := "cost on cheaper model"
	same := true
	for _, g := range groups[1:] {
		if g.alt != groups[0].alt {
			same = false
		}
	}
	if same {
		altHeader = "cost on " + shortModelName(groups[0].alt)
	}

	t := Table{Columns: []string{"agent", "runs", "model", "tools used", "cost", altHeader}}
	for _, g := range groups {
		tools := joinList(sortedKeys(g.tools))
		if tools == "" {
			tools = "none"
		}
		t.Rows = append(t.Rows, []string{
			g.agentType,
			fmtInt(int64(g.runs)),
			g.modelID,
			tools,
			fmtUSD(g.current),
			fmtUSD(g.altCost),
		})
	}
	return t
}
