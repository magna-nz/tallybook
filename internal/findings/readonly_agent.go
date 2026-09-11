package findings

import (
	"fmt"
	"github.com/magna-nz/tallybook/internal/model"
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
		if !model.AllReadOnly(counts[row.ID]) {
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
		// Keyed on the alternative, not just the agent type: an agent that ran
		// on two different strong models has two different cheaper models to
		// move to, and merging them would price the saving against a mixture
		// while the advice named only one of them.
		key := typ + "\x00" + alt
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
	f.WhatToChange = readOnlyWhatToChange(in, groups)
	f.WhatToExpect = readOnlyWhatToExpect(groups, current, altTotal)
	if c := tokenizerCaveat(groups[0].modelID, groups[0].alt); c != "" {
		f.WhyItCosts += "\n\n" + c
	}
	f.Patch = readOnlyPatch(in, groups)
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

// readOnlyWhatToChange writes the change to make, checked against what the
// project's agent files already say. Telling someone to set a model their file
// already sets is worse than saying nothing: it reads as if the tool never
// looked. Where the file already agrees, the call site is the thing to change,
// because that is what wins.
func readOnlyWhatToChange(in Input, groups []*roGroup) string {
	var parts []string
	for _, g := range groups {
		short := shortModelName(g.alt)
		def, haveFile := in.Agents.Lookup(g.agentType)

		switch {
		case haveFile && def.SetsModel() && in.Prices.SameModel(def.Model, g.alt):
			parts = append(parts, fmt.Sprintf(
				"%s already says %s, so the file is not what is putting these runs on %s. The model passed "+
					"when the agent is launched wins over the file, so that is where to look. Drop it from "+
					"the launch and the file's %s will be used.",
				def.Path, def.Model, modelDisplay(g.modelID), def.Model))

		case haveFile && def.SetsModel():
			parts = append(parts, fmt.Sprintf(
				"Change the model line in %s from %s to %s:\n\n    model: %s\n",
				def.Path, def.Model, short, short))

		case haveFile:
			parts = append(parts, fmt.Sprintf(
				"%s does not pin a model%s. Add this line to the block at the top of the file:\n\n    model: %s\n",
				def.Path, inheritNote(def.Model), short))

		case agentFile(g.agentType) != "":
			parts = append(parts, fmt.Sprintf(
				"There is no %s yet. Create it with this at the top, and the agent will use %s from then on:"+
					"\n\n    ---\n    name: %s\n    model: %s\n    ---\n",
				agentFile(g.agentType), short, g.agentType, short))

		default:
			parts = append(parts, fmt.Sprintf(
				"%s is built into the harness and has no file of its own, so pass model: %q when you launch it.",
				g.agentType, short))
		}
	}
	return strings.TrimRight(strings.Join(parts, "\n\n"), "\n")
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

func readOnlyPatch(in Input, groups []*roGroup) string {
	var b strings.Builder
	for _, g := range groups {
		def, haveFile := in.Agents.Lookup(g.agentType)
		if !haveFile {
			continue // nothing on disk to patch
		}
		want := "model: " + shortModelName(g.alt)
		switch {
		case def.SetsModel() && in.Prices.SameModel(def.Model, g.alt):
			continue // the file already says it; the change belongs at the call site
		case def.Model != "":
			b.WriteString(replaceLinePatch(def.Path, "model: "+def.Model, want))
		default:
			b.WriteString(frontmatterPatch(def.Path, want))
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
