package findings

import (
	"fmt"
	"sort"
)

// toolOutputRule finds sessions whose context is mostly raw command output
// and whole-file reads. That output is re-sent on every later turn, so it is
// paid for again and again.
type toolOutputRule struct{}

func (toolOutputRule) ID() string { return "tool-output-bloat" }

// bloatedSession is one session carrying more tool output than conversation.
type bloatedSession struct {
	id           string
	project      string
	turns        int
	share        float64
	largestChars int
	cost         float64
	saving       float64
	toolTokens   int64
	results      int
	bigResults   int
}

// charsPerToken is the usual rough conversion for code and logs. It is an
// estimate, which is why this finding is Medium and not High.
const charsPerToken = 4

func (toolOutputRule) Run(in Input) (*Finding, error) {
	rows, err := in.Store.Sessions(in.Filter)
	if err != nil {
		return nil, err
	}

	threshold := in.Cfg.ToolOutputShare
	if threshold <= 0 {
		threshold = 0.5
	}

	var hits []bloatedSession
	for _, row := range rows {
		turns, err := in.Store.Turns(row.ID)
		if err != nil {
			return nil, err
		}
		if len(turns) == 0 {
			continue
		}
		var contextTokens int64
		var cacheReadCost float64
		for _, t := range turns {
			contextTokens += t.Usage.ContextTokens()
			if rate, ok := in.Prices.RateForTurn(t.Model, t); ok {
				cacheReadCost += float64(t.Usage.CacheRead) * rate.CacheRead / 1e6
			}
		}
		if contextTokens <= 200_000 {
			continue
		}

		results, err := in.Store.ToolResults(row.ID)
		if err != nil {
			return nil, err
		}
		var toolChars, largest int
		big := 0
		for _, r := range results {
			toolChars += r.Chars
			if r.Chars > largest {
				largest = r.Chars
			}
			if r.Chars/charsPerToken > 30_000 {
				big++
			}
		}
		if toolChars == 0 {
			continue
		}

		// Output landing at turn n is re-sent on every turn after it, so on
		// average it is carried by half the session.
		toolTokens := int64(toolChars / charsPerToken)
		share := float64(toolTokens) * float64(len(turns)) / 2 / float64(contextTokens)
		if share > 1 {
			share = 1
		}
		if share < threshold {
			continue
		}

		hits = append(hits, bloatedSession{
			id:           row.ID,
			project:      row.Project,
			turns:        len(turns),
			share:        share,
			largestChars: largest,
			cost:         costOf(in, turns),
			// Assume trimming halves what the carried output costs to re-read.
			saving:     cacheReadCost * share * 0.5,
			toolTokens: toolTokens,
			results:    len(results),
			bigResults: big,
		})
	}
	if len(hits) == 0 {
		return nil, nil
	}

	sort.Slice(hits, func(i, j int) bool { return hits[i].saving > hits[j].saving })

	var saving, shareSum float64
	var toolTokens int64
	results, big := 0, 0
	for _, h := range hits {
		saving += h.saving
		shareSum += h.share
		toolTokens += h.toolTokens
		results += h.results
		big += h.bigResults
	}
	avgShare := shareSum / float64(len(hits))
	avgResult := int64(0)
	if results > 0 {
		avgResult = toolTokens / int64(results)
	}

	f := &Finding{
		Direction:   Context,
		Confidence:  Medium,
		SavingUSD:   in.PerMonth(saving),
		SavingShare: in.Share(saving),
		Title:       "Command output is filling your context",
	}

	bigPhrase := fmt.Sprintf("%s of them were over 30,000", fmtInt(int64(big)))
	if big == 0 {
		bigPhrase = "none of them were over 30,000"
	} else if big == 1 {
		bigPhrase = "one of them was over 30,000"
	}
	f.WhatHappened = fmt.Sprintf(
		"In %s session%s %s, about %s%% of everything sent to the model was raw output from commands and full "+
			"file reads. The average result was about %s tokens, and %s.",
		fmtInt(int64(len(hits))), plural(len(hits)), windowPhrase(in), fmtPct(avgShare),
		fmtInt(avgResult), bigPhrase)

	f.WhyItCosts = "Every turn after that point carries all of that output again. A single 30,000 token build " +
		"log sent at turn 5 of a 60 turn session is paid for 55 more times. It is charged at the cache-read " +
		"price rather than the full one, which is cheap per turn and expensive per session."

	f.WhatToChange = "Three habits, in the order they pay off:\n\n" +
		"- Pipe test and build output through `tail -50`, and only read the whole log when the command fails.\n" +
		"- Write anything longer than a screen to a file and read it back in pieces, rather than letting it " +
		"land in the conversation whole.\n" +
		"- Use a sub-agent for reading large files, so the reading happens in a separate conversation that " +
		"gets thrown away once it reports back.\n\n" +
		"Write those three as lines in CLAUDE.md. They then apply to every session, rather than the ones you " +
		"remember to ask for."

	f.WhatToExpect = "The share of context spent on tool output should fall well below half. " +
		"Sessions will also stay usable for longer before compaction."

	f.Evidence = Table{Columns: []string{"session", "project", "turns", "tool output share", "largest result (tokens)", "cost"}}
	for _, h := range hits {
		f.Evidence.Rows = append(f.Evidence.Rows, []string{
			shortID(h.id),
			h.project,
			fmtInt(int64(h.turns)),
			fmtPct(h.share) + "%",
			fmtInt(int64(h.largestChars / charsPerToken)),
			fmtUSD(h.cost),
		})
	}
	return f, nil
}
