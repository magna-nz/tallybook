package findings

import (
	"fmt"
	"sort"

	"github.com/magna-nz/tallybook/internal/model"
)

// compactionRule finds main sessions whose context filled often enough that
// Claude Code compacted them more than once. One compaction in a long
// session is ordinary; repeated compaction means the context keeps refilling
// right after being trimmed, and every compaction throws away detail the
// model then has to re-read. What the summarising request itself cost is not
// in the transcript at all, so this finding never claims a saving.
type compactionRule struct{}

func (compactionRule) ID() string { return "repeated-compaction" }

// compactionMinPerSession is how many compactions inside one session make it
// a repeat rather than the one compaction a long session is expected to hit.
const compactionMinPerSession = 2

// compactionMinSessions is the floor on how many sessions must show the
// pattern before it is worth a finding.
const compactionMinSessions = 2

// compactedSession is one main session that compacted more than once.
type compactedSession struct {
	id             string
	project        string
	compactions    int
	largestContext int64
	cost           float64
}

func (compactionRule) Run(in Input) (*Finding, error) {
	rows, err := in.Store.Sessions(in.Filter)
	if err != nil {
		return nil, err
	}

	var hits []compactedSession
	for _, row := range rows {
		// Only main sessions: a sub-agent's context is not the one the user
		// is watching fill up, and Codex has no compaction marker to find.
		if row.ParentSessionID != "" || row.AgentID != "" || row.Source != model.SourceClaudeCode {
			continue
		}
		turns, err := in.Store.Turns(row.ID)
		if err != nil {
			return nil, err
		}
		var compactions int
		var largest int64
		for i, t := range turns {
			if !t.CompactionBefore {
				continue
			}
			compactions++
			if i > 0 {
				if ctx := turns[i-1].Usage.ContextTokens(); ctx > largest {
					largest = ctx
				}
			}
		}
		if compactions < compactionMinPerSession {
			continue
		}
		hits = append(hits, compactedSession{
			id: row.ID, project: row.Project, compactions: compactions,
			largestContext: largest, cost: costOf(in, turns),
		})
	}
	if len(hits) < compactionMinSessions {
		return nil, nil
	}

	sort.Slice(hits, func(i, j int) bool {
		if hits[i].compactions != hits[j].compactions {
			return hits[i].compactions > hits[j].compactions
		}
		return hits[i].id < hits[j].id
	})

	var totalCompactions int
	var largestOverall int64
	for _, h := range hits {
		totalCompactions += h.compactions
		if h.largestContext > largestOverall {
			largestOverall = h.largestContext
		}
	}

	f := &Finding{
		Direction:  Context,
		Confidence: Info,
		SavingUSD:  0,
		Title:      "Your sessions are compacting more than once",
	}

	f.WhatHappened = fmt.Sprintf(
		"In %s session%s %s, the context filled and compacted %s or more times, %s compactions in total. "+
			"The largest context recorded right before a compaction was about %s tokens.",
		fmtInt(int64(len(hits))), plural(len(hits)), windowPhrase(in), fmtInt(int64(compactionMinPerSession)),
		fmtInt(int64(totalCompactions)), fmtInt(largestOverall))

	f.WhyItCosts = "Each compaction throws away detail that the model then has to re-read, and the turn right " +
		"after it rebuilds the prompt cache from scratch. This finding cannot put a dollar figure on any of " +
		"that: the summarising request itself is a separate call that never shows up as a turn in the transcript."

	f.WhatToChange = "Try `/clear` between unrelated tasks, so the next one starts from nothing rather than a " +
		"summary of the last. Run `/compact` yourself at a natural break, with a short note on what to keep, " +
		"rather than waiting for the automatic one to guess. Put a large read or survey in a sub-agent, so it " +
		"never lands in the main transcript at all. To have Claude Code compact earlier by default, set " +
		"\"autoCompactWindow\" in ~/.claude/settings.json to a token count between 100k and 1M (for example " +
		"\"150k\"), or to \"auto\". Codex sessions are not counted here: this rule looks for Claude Code's own " +
		"compaction marker, which a Codex transcript does not carry."

	f.WhatToExpect = "Compactions per session should fall to one or none."

	f.Evidence = Table{Columns: []string{"session", "project", "compactions", "largest context at compaction", "cost"}}
	for _, h := range hits {
		f.Evidence.Rows = append(f.Evidence.Rows, []string{
			shortID(h.id), h.project, fmtInt(int64(h.compactions)), fmtInt(h.largestContext), fmtUSD(h.cost),
		})
	}

	return f, nil
}
