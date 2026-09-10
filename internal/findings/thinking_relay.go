package findings

import (
	"fmt"
	"sort"
	"strings"
)

// thinkingRelayRule finds turns where the model thought and then did nothing
// but pass the work to the next tool. The thinking is billed as output, the
// dearest kind of token, and on a relay turn it buys nothing.
type thinkingRelayRule struct{}

func (thinkingRelayRule) ID() string { return "thinking-on-relay" }

// relayGroup is one agent type's relay turns that carried thinking.
type relayGroup struct {
	agent          string
	turns          int
	thinkingTokens int64
	cost           float64
}

// relayTextChars is the most visible text a turn can have and still count as
// a hand-off rather than an answer.
const relayTextChars = 200

func (thinkingRelayRule) Run(in Input) (*Finding, error) {
	rows, err := in.Store.Sessions(in.Filter)
	if err != nil {
		return nil, err
	}

	threshold := in.Cfg.ThinkingRelayTurns
	if threshold <= 0 {
		threshold = 20
	}

	byAgent := map[string]*relayGroup{}
	for _, row := range rows {
		agent := row.AgentType
		if agent == "" {
			if row.AgentID != "" {
				agent = "unnamed agent"
			} else {
				agent = "main session"
			}
		}
		turns, err := in.Store.Turns(row.ID)
		if err != nil {
			return nil, err
		}
		for _, t := range turns {
			if t.TextChars >= relayTextChars || len(t.ToolCalls) != 1 || t.Usage.Thinking <= 0 {
				continue
			}
			g := byAgent[agent]
			if g == nil {
				g = &relayGroup{agent: agent}
				byAgent[agent] = g
			}
			g.turns++
			g.thinkingTokens += t.Usage.Thinking
			if rate, ok := in.Prices.LookupAt(t.Model, t.Timestamp); ok {
				g.cost += float64(t.Usage.Thinking) * rate.Output / 1e6
			}
		}
	}

	var groups []*relayGroup
	for _, g := range byAgent {
		if g.turns >= threshold {
			groups = append(groups, g)
		}
	}
	if len(groups) == 0 {
		return nil, nil
	}
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].cost != groups[j].cost {
			return groups[i].cost > groups[j].cost
		}
		return groups[i].agent < groups[j].agent
	})

	var saving float64
	totalTurns := 0
	for _, g := range groups {
		saving += g.cost
		totalTurns += g.turns
	}
	if saving < 1 {
		return nil, nil
	}

	f := &Finding{
		Direction:   Effort,
		Confidence:  Low,
		SavingUSD:   in.PerMonth(saving),
		SavingShare: in.Share(saving),
		Title:       "Thinking was spent on turns that did no thinking",
	}

	var top []string
	for i, g := range groups {
		if i == 2 {
			break
		}
		top = append(top, g.agent)
	}
	f.WhatHappened = fmt.Sprintf(
		"On %s turns %s, the model spent thinking tokens and then did nothing but call the next tool. "+
			"Most were in %s.",
		fmtInt(int64(totalTurns)), windowPhrase(in), joinList(top))

	f.WhyItCosts = "Thinking tokens are billed as output, the most expensive kind. " +
		"On a turn that only relays a result, they buy nothing."

	f.WhatToChange = relayWhatToChange(groups)

	f.WhatToExpect = "Small saving, but it also makes those agents faster: " +
		"a hand-off turn that does not think comes back sooner."

	f.Patch = relayPatch(groups)

	f.Evidence = Table{Columns: []string{"agent", "relay turns with thinking", "thinking tokens", "cost"}}
	for _, g := range groups {
		f.Evidence.Rows = append(f.Evidence.Rows, []string{
			g.agent, fmtInt(int64(g.turns)), fmtInt(g.thinkingTokens), fmtUSD(g.cost),
		})
	}
	return f, nil
}

func relayWhatToChange(groups []*relayGroup) string {
	var parts []string
	onlyMain := true
	for _, g := range groups {
		if g.agent != "main session" {
			onlyMain = false
		}
	}
	for _, g := range groups {
		if file := agentFile(g.agent); file != "" {
			parts = append(parts, fmt.Sprintf("In %s add:\n\n    effort: low\n", file))
			continue
		}
		if g.agent == "main session" {
			parts = append(parts, "There is no command that turns the main session's thinking down mid-"+
				"conversation. That level is set in your Claude Code settings, and it is usually worth "+
				"leaving where it is.")
			continue
		}
		parts = append(parts, fmt.Sprintf(
			"%s has no file of its own, so pass a lower effort where you launch it rather than in a file.", g.agent))
	}
	if !onlyMain {
		parts = append(parts, "Leave the main session and implementers alone. "+
			"They are where the thinking earns its keep.")
	}
	return strings.TrimRight(strings.Join(parts, "\n"), "\n")
}

func relayPatch(groups []*relayGroup) string {
	var b strings.Builder
	for _, g := range groups {
		if file := agentFile(g.agent); file != "" {
			b.WriteString(frontmatterPatch(file, "effort: low"))
		}
	}
	return b.String()
}
