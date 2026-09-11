package findings

import (
	"fmt"
	"sort"
	"strings"

	"github.com/magna-nz/tallybook/internal/model"
)

// requestedModelRule finds launches where the caller asked for one model and
// the sub-agent ran on another. That is a settings problem rather than a
// habit: the choice was made correctly and something overrode it.
type requestedModelRule struct{}

func (requestedModelRule) ID() string { return "requested-model-not-honoured" }

// mismatch is one launch whose child did not run on the requested model.
type mismatch struct {
	parentID  string
	agentType string
	requested string
	resolved  string
	actual    string
	cost      float64
	asAsked   float64
}

func (requestedModelRule) Run(in Input) (*Finding, error) {
	launches, err := in.Store.Launches(in.Filter)
	if err != nil {
		return nil, err
	}

	turnCache := map[string][]model.Turn{}
	var found []mismatch
	for _, l := range launches {
		if l.RequestedModel == "" || l.ChildSessionID == "" {
			continue
		}
		turns, ok := turnCache[l.ChildSessionID]
		if !ok {
			turns, err = in.Store.Turns(l.ChildSessionID)
			if err != nil {
				return nil, err
			}
			turnCache[l.ChildSessionID] = turns
		}
		actual := mostCommonModel(turns)
		if actual == "" {
			continue // nothing ran; nothing to say
		}
		// ResolvedModel is what the harness claimed. The turns are what
		// actually happened, so they decide; the claim is only evidence.
		if sameModel(in, actual, l.RequestedModel) {
			continue
		}
		agentType := l.SubagentType
		if agentType == "" {
			agentType = "unnamed"
		}
		found = append(found, mismatch{
			parentID:  l.ParentSessionID,
			agentType: agentType,
			requested: l.RequestedModel,
			resolved:  l.ResolvedModel,
			actual:    actual,
			cost:      costOf(in, turns),
			asAsked:   costAs(in, turns, l.RequestedModel),
		})
	}
	if len(found) < 2 {
		return nil, nil
	}

	var saving float64
	overspent, underspent := 0, 0
	for _, m := range found {
		if d := m.cost - m.asAsked; d > 0 {
			saving += d
			overspent++
		} else if d < 0 {
			underspent++
		}
	}

	var pairs [][2]string
	for _, m := range found {
		pairs = append(pairs, [2]string{m.actual, m.requested})
	}
	approximate := saving > 0 && crossesTokenizer(pairs)

	f := &Finding{
		Direction:   Config,
		Confidence:  softenedBy(High, approximate),
		SavingUSD:   in.PerMonth(saving),
		SavingShare: in.Share(saving),
	}
	f.Title = requestedTitle(found, overspent, underspent)
	f.WhatHappened = requestedWhatHappened(in, found)
	f.WhyItCosts = requestedWhyItCosts(in, found, saving)
	if approximate {
		for _, m := range found {
			if c := tokenizerCaveat(m.actual, m.requested); c != "" {
				f.WhyItCosts += "\n\n" + c
				break
			}
		}
	}
	f.WhatToChange = requestedWhatToChange(in, found)
	f.WhatToExpect = "The requested and actual columns in `tallybook agents` should match after this. " +
		"If they still differ, the override is coming from somewhere else and the agent file is not the culprit."
	f.Evidence = requestedEvidence(found)
	return f, nil
}

// distinctActual lists the models the runs really used.
func distinctActual(found []mismatch) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range found {
		if !seen[m.actual] {
			seen[m.actual] = true
			out = append(out, m.actual)
		}
	}
	sort.Strings(out)
	return out
}

func distinctRequested(found []mismatch) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range found {
		if !seen[m.requested] {
			seen[m.requested] = true
			out = append(out, m.requested)
		}
	}
	sort.Strings(out)
	return out
}

func requestedTitle(found []mismatch, overspent, underspent int) string {
	actual := distinctActual(found)
	if len(actual) == 1 && underspent == 0 {
		return "You asked for a cheaper model but got " + modelDisplay(actual[0])
	}
	if overspent == 0 {
		return "Sub-agents ran on a cheaper model than you asked for"
	}
	return "Sub-agents did not run on the model you asked for"
}

func requestedWhatHappened(in Input, found []mismatch) string {
	requested := make([]string, 0)
	for _, r := range distinctRequested(found) {
		requested = append(requested, modelDisplay(canonical(in, r)))
	}
	actual := make([]string, 0)
	for _, a := range distinctActual(found) {
		actual = append(actual, modelDisplay(a))
	}
	return fmt.Sprintf("%s times %s your main session launched an agent and asked for %s, "+
		"but the agent actually ran on %s. The check is the child transcript itself, "+
		"not what the launch reported back.",
		fmtInt(int64(len(found))), windowPhrase(in), joinList(requested), joinList(actual))
}

func requestedWhyItCosts(in Input, found []mismatch, saving float64) string {
	var paid, asked float64
	for _, m := range found {
		paid += m.cost
		asked += m.asAsked
	}
	if saving > 0 {
		ratio := "more than"
		if asked > 0 {
			ratio = fmt.Sprintf("about %sx", fmtOneDP(paid/asked))
		}
		return fmt.Sprintf("You made the right call and it was ignored, so those runs cost %s what you intended: "+
			"%s instead of %s at list price.", ratio, fmtUSD(paid), fmtUSD(asked))
	}
	ratio := "less than"
	if asked > 0 {
		ratio = fmt.Sprintf("about %sx", fmtOneDP(paid/asked))
	}
	return fmt.Sprintf("Nothing was overspent here: the runs cost %s what you asked for, %s instead of %s, "+
		"because they went to a cheaper model than the one you named. "+
		"It is still worth fixing. The model you pick at the call site is not the model that runs, "+
		"so the next time you reach for a stronger one it may not arrive either.",
		ratio, fmtUSD(paid), fmtUSD(asked))
}

// requestedWhatToChange explains where the override is coming from. Claude
// Code resolves a sub-agent's model in this order: the model passed at the
// call site, then the agent file's frontmatter, then CLAUDE_CODE_SUBAGENT_MODEL,
// then the main conversation's model. The call site wins, so a run that did not
// honour the request was almost never overridden by the agent file.
//
// Order verified 2026-09-11 against code.claude.com/docs/en/sub-agents.
func requestedWhatToChange(in Input, found []mismatch) string {
	var parts []string
	seen := map[string]bool{}
	for _, m := range found {
		if seen[m.agentType] {
			continue
		}
		seen[m.agentType] = true

		def, haveFile := in.Agents.Lookup(m.agentType)
		switch {
		case haveFile && def.SetsModel() && in.Prices.SameModel(def.Model, m.actual):
			parts = append(parts, fmt.Sprintf(
				"%s already says %s in %s, and that is what the runs used. What did not survive is the %s you "+
					"asked for at the launch. Whatever is passing that model is being ignored, or is not "+
					"reaching the launch at all: check the call site.",
				def.Name, def.Model, displayPath(def.Path), m.requested))
		case haveFile && def.SetsModel():
			parts = append(parts, fmt.Sprintf(
				"%s sets %s, the launch asked for %s, and the runs used %s. None of those agree, so start by "+
					"deciding which one you meant and making the other two match it.",
				displayPath(def.Path), def.Model, m.requested, modelDisplay(m.actual)))
		case haveFile:
			parts = append(parts, fmt.Sprintf(
				"%s does not pin a model%s, so these runs fell back to whatever was in force. Add the model you "+
					"want to the block at the top of that file:\n\n    model: %s\n",
				displayPath(def.Path), inheritNote(def.Model), shortModelName(m.requested)))
		default:
			parts = append(parts, fmt.Sprintf(
				"There is no file for %s under .claude/agents, so the model can only be decided where it is "+
					"launched, or by the session's own model. Check what the launch is passing.", m.agentType))
		}
	}

	parts = append(parts, "Worth knowing which way this resolves: the model passed when the agent is launched "+
		"wins over the model line in the agent's own file. The file is the fallback, not the override.")
	return strings.Join(parts, "\n\n")
}

// inheritNote spells out what an explicit "inherit" means, since it looks like
// a setting but is a deliberate absence of one.
func inheritNote(model string) string {
	if strings.EqualFold(strings.TrimSpace(model), "inherit") {
		return ` (it says "inherit", which asks for whatever model launched it)`
	}
	return ""
}

func requestedEvidence(found []mismatch) Table {
	t := Table{Columns: []string{"session", "agent", "requested", "resolved", "actual", "cost"}}
	for _, m := range found {
		resolved := m.resolved
		if resolved == "" {
			resolved = "not reported"
		}
		t.Rows = append(t.Rows, []string{
			shortID(m.parentID), m.agentType, m.requested, resolved, m.actual, fmtUSD(m.cost),
		})
	}
	return t
}
