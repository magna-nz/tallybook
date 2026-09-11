package findings

import (
	"fmt"
	"sort"

	"github.com/magna-nz/tallybook/internal/model"
)

// repeatedCallsRule finds a read-only tool called with byte-identical input
// again and again inside one session. The store only ever holds a salted
// hash of a call's input, never the input itself, which is exactly enough to
// see the repeat without knowing what was being read. A repeated write or
// test run is not counted: running the same command twice can be the point,
// where reading the same file twice never changes what comes back.
type repeatedCallsRule struct{}

func (repeatedCallsRule) ID() string { return "repeated-tool-calls" }

// repeatThreshold is how many calls sharing one (tool, input) pair inside a
// session make a repeat rather than an ordinary re-check.
const repeatThreshold = 3

// repeatMinSessions is the floor on how many sessions must show a repeat
// before this is a pattern worth a finding rather than one odd session.
const repeatMinSessions = 2

// isReadOnlyCall reports whether a stored ToolCall could not have changed
// anything, using the one definition in internal/model. A shell-style call
// carries its classification as a "(read)"/"(write)" suffix on the name; a
// call with no class was never classified and is treated as mutating.
func isReadOnlyCall(tc model.ToolCall) bool {
	if tc.Class == "" {
		return model.IsReadOnlyTool(tc.Name)
	}
	return model.IsReadOnlyTool(tc.Name + "(" + tc.Class + ")")
}

// repeatKey groups calls that share a tool name and an input hash.
type repeatKey struct{ name, hash string }

// callRef is one call in a group, with enough to price what it carried.
type callRef struct {
	turnIndex int
	id        string
}

// repeatGroup is one (tool, input) pair called at least repeatThreshold times
// in a session.
type repeatGroup struct {
	tool         string
	calls        []callRef
	cost         float64 // carried cost of every call after the first
	resultTokens int64   // size of the first call's result: the baseline every later call repeats
}

// repeatedSession is one session with at least one repeat group.
type repeatedSession struct {
	id      string
	project string
	groups  []repeatGroup
	saving  float64 // sum of every group's carried cost
}

func (repeatedCallsRule) Run(in Input) (*Finding, error) {
	rows, err := in.Store.Sessions(in.Filter)
	if err != nil {
		return nil, err
	}

	threshold := in.Cfg.RepeatThreshold
	if threshold <= 0 {
		threshold = repeatThreshold
	}
	var hits []repeatedSession
	totalGroups := 0
	for _, row := range rows {
		turns, err := in.Store.Turns(row.ID)
		if err != nil {
			return nil, err
		}
		if len(turns) == 0 {
			continue
		}
		results, err := in.Store.ToolResults(row.ID)
		if err != nil {
			return nil, err
		}
		resultChars := map[string]int{}
		for _, r := range results {
			resultChars[r.ToolCallID] = r.Chars
		}

		byKey := map[repeatKey][]callRef{}
		for ti, t := range turns {
			for _, tc := range t.ToolCalls {
				if tc.InputHash == "" || !isReadOnlyCall(tc) {
					continue
				}
				k := repeatKey{tc.Name, tc.InputHash}
				byKey[k] = append(byKey[k], callRef{turnIndex: ti, id: tc.ID})
			}
		}

		var session repeatedSession
		session.id, session.project = row.ID, row.Project
		for k, calls := range byKey {
			if len(calls) < threshold {
				continue
			}
			var groupCost float64
			for _, c := range calls[1:] { // every call after the first carried its result forward
				chars, ok := resultChars[c.id]
				if !ok {
					continue
				}
				tokens := int64(chars / charsPerToken)
				turnsAfter := int64(len(turns) - c.turnIndex - 1)
				if turnsAfter <= 0 {
					continue
				}
				turn := turns[c.turnIndex]
				rate, ok := in.Prices.LookupAt(turn.Model, turn.Timestamp)
				if !ok {
					continue
				}
				groupCost += float64(tokens) * float64(turnsAfter) * rate.CacheRead / 1e6
			}
			var resultTokens int64
			if chars, ok := resultChars[calls[0].id]; ok {
				resultTokens = int64(chars / charsPerToken)
			}
			session.groups = append(session.groups, repeatGroup{
				tool: k.name, calls: calls, cost: groupCost, resultTokens: resultTokens,
			})
			session.saving += groupCost
			totalGroups++
		}
		if len(session.groups) > 0 {
			// Groups came out of a map; put them in a fixed order so the worst
			// case named in the prose is the same one on every run.
			sort.Slice(session.groups, func(i, j int) bool {
				a, b := session.groups[i], session.groups[j]
				if a.cost != b.cost {
					return a.cost > b.cost
				}
				if len(a.calls) != len(b.calls) {
					return len(a.calls) > len(b.calls)
				}
				return a.tool < b.tool
			})
			// The carry is a claim about what the repeats cost, made from
			// character counts. What the session actually paid to re-read its
			// context is a measured figure, and the claim can never exceed it.
			var cacheReadCost float64
			for _, t := range turns {
				if rate, ok := in.Prices.LookupAt(t.Model, t.Timestamp); ok {
					cacheReadCost += float64(t.Usage.CacheRead) * rate.CacheRead / 1e6
				}
			}
			if session.saving > cacheReadCost {
				scale := cacheReadCost / session.saving
				for i := range session.groups {
					session.groups[i].cost *= scale
				}
				session.saving = cacheReadCost
			}
			hits = append(hits, session)
		}
	}

	var total float64
	for _, h := range hits {
		total += h.saving
	}
	if len(hits) < repeatMinSessions || total < 1.0 {
		return nil, nil
	}

	sort.Slice(hits, func(i, j int) bool {
		if hits[i].saving != hits[j].saving {
			return hits[i].saving > hits[j].saving
		}
		return hits[i].id < hits[j].id
	})

	// The worst case: the group that carried the most cost, anywhere.
	var worstTool string
	var worstTimes int
	var worstSession string
	for _, h := range hits {
		for _, g := range h.groups {
			if g.cost > 0 && len(g.calls) > worstTimes {
				worstTimes = len(g.calls)
				worstTool = g.tool
				worstSession = h.id
			}
		}
	}

	f := &Finding{
		Direction:   Context,
		Confidence:  Medium,
		SavingUSD:   in.PerMonth(total),
		SavingShare: in.Share(total),
		Title:       "Identical tool calls were repeated",
	}

	f.WhatHappened = fmt.Sprintf(
		"In %s session%s %s, a read-only tool was called with the same input %s or more times in a row, "+
			"%s repeated group%s in total. The worst case: %s was called with the same input %s times in "+
			"session %s.",
		fmtInt(int64(len(hits))), plural(len(hits)), windowPhrase(in), fmtInt(int64(threshold)),
		fmtInt(int64(totalGroups)), plural(totalGroups), worstTool, fmtInt(int64(worstTimes)), shortID(worstSession))

	f.WhyItCosts = "Every call after the first puts the same result back into the conversation, and from then " +
		"on it is carried on every later turn, even though nothing about it changed."

	f.WhatToChange = "Habits, not settings. Add these lines to CLAUDE.md so they apply to every session:\n\n" +
		"    - Read a file once and quote the part you need, rather than reading it again.\n" +
		"    - After an edit, re-read only the changed range, not the whole file.\n" +
		"    - Use a sub-agent for a survey that would otherwise read the same files more than once.\n\n" +
		"For Codex, the same three lines belong in AGENTS.md."

	f.WhatToExpect = "Repeated reads of the same input should drop to zero. Sessions stay usable for longer " +
		"because less of the context is a copy of something already sent."

	f.Evidence = Table{Columns: []string{"session", "project", "tool", "times", "result tokens", "carried cost"}}
	for _, h := range hits {
		worst := h.groups[0]
		for _, g := range h.groups {
			if g.cost > worst.cost {
				worst = g
			}
		}
		f.Evidence.Rows = append(f.Evidence.Rows, []string{
			shortID(h.id), h.project, worst.tool, fmtInt(int64(len(worst.calls))),
			fmtInt(worst.resultTokens), fmtUSD(h.saving),
		})
	}

	return f, nil
}
