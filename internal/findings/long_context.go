package findings

import (
	"fmt"
	"sort"
)

// longContextRule finds sessions that grew large enough that most of what they
// re-sent every turn was history the task in hand no longer needed.
//
// Every turn sends the whole conversation again. That is cheap while the
// conversation is small and it never stops growing, so the cost of the history
// arrives quietly: no single turn looks expensive, and the session as a whole
// does. The rule charges each oversized turn only for the part of its
// input-side cost that sits above the threshold, so a session that merely got
// big is not blamed for the whole of it.
type longContextRule struct{}

func (longContextRule) ID() string { return "long-context-tax" }

// longContextTokens is the conversation size past which the history stops
// paying for itself. Below it a re-sent conversation is mostly the task; above
// it, mostly what came before the task. This will be wired to the config later.
const longContextTokens = 100_000

// longContextMinTaxUSD is the smallest per-session figure worth naming. Under
// fifty cents a session, the advice costs the reader more attention than the
// money is worth. This will be wired to the config later.
const longContextMinTaxUSD = 0.50

// longContextMinSessions is the evidence floor: one long session is a long
// session, three is a way of working. This will be wired to the config later.
const longContextMinSessions = 3

// longContextSession is one session that paid to carry its own history.
type longContextSession struct {
	id        string
	project   string
	turnsOver int   // turns whose conversation was above the threshold
	peak      int64 // the largest conversation the session sent
	tax       float64
}

func (longContextRule) Run(in Input) (*Finding, error) {
	rows, err := in.Store.Sessions(in.Filter)
	if err != nil {
		return nil, err
	}

	threshold := in.Cfg.LongContextTokens
	if threshold <= 0 {
		threshold = longContextTokens
	}
	var hits []longContextSession
	for _, row := range rows {
		if row.AgentID != "" {
			continue // a sub-agent's conversation is thrown away when it reports back
		}
		turns, err := in.Store.Turns(row.ID)
		if err != nil {
			return nil, err
		}

		s := longContextSession{id: row.ID, project: row.Project}
		for _, t := range turns {
			ctx := t.Usage.ContextTokens()
			if ctx <= threshold {
				continue
			}
			rate, ok := in.Prices.LookupAt(t.Model, t.Timestamp)
			if !ok {
				continue // a missing price is never guessed at
			}
			// Only the input side of the turn: the output is the answer, which
			// the size of the history did not pay for.
			inputSide := (float64(t.Usage.Input)*rate.Input +
				float64(t.Usage.CacheRead)*rate.CacheRead +
				float64(t.Usage.CacheWrite5m)*rate.CacheWrite5m +
				float64(t.Usage.CacheWrite1h)*rate.CacheWrite1h) / 1e6

			s.turnsOver++
			s.tax += inputSide * float64(ctx-threshold) / float64(ctx)
			if ctx > s.peak {
				s.peak = ctx
			}
		}
		if s.tax >= longContextMinTaxUSD {
			hits = append(hits, s)
		}
	}
	if len(hits) < longContextMinSessions {
		return nil, nil
	}

	sort.Slice(hits, func(i, j int) bool {
		if hits[i].tax != hits[j].tax {
			return hits[i].tax > hits[j].tax
		}
		return hits[i].id < hits[j].id
	})

	var tax float64
	turnsOver := 0
	peak := int64(0)
	for _, h := range hits {
		tax += h.tax
		turnsOver += h.turnsOver
		if h.peak > peak {
			peak = h.peak
		}
	}
	// Half, not all of it: a fresh session still has to be told what it needs,
	// and being told costs something.
	saving := tax / 2
	if saving <= 0 {
		return nil, nil
	}

	f := &Finding{
		Direction:   Context,
		Confidence:  Medium,
		SavingUSD:   in.PerMonth(saving),
		SavingShare: in.Share(saving),
		Title:       "Long sessions pay to carry their own history",
	}

	f.WhatHappened = fmt.Sprintf(
		"%s session%s %s grew past %s tokens of conversation. Across them, %s turn%s ran above that mark, "+
			"and the largest conversation sent was %s tokens.",
		fmtInt(int64(len(hits))), plural(len(hits)), windowPhrase(in), fmtInt(threshold),
		fmtInt(int64(turnsOver)), plural(turnsOver), fmtInt(peak))

	f.WhyItCosts = fmt.Sprintf(
		"Every turn sends the whole conversation again, so once a session is this long, most of what is sent "+
			"is history the task in hand no longer needs. Most of it comes back from the cache at the cheaper "+
			"read price, and the rest is fresh input and cache writes, which is why it never looks like much on "+
			"any one turn and still comes to about %s a month across these sessions. The figure above is half "+
			"of that, because a fresh session still has to be told what it needs before it can carry on, and "+
			"being told costs something too.", fmtUSD(in.PerMonth(tax)))

	f.WhatToChange = "Four things, in the order they pay off:\n\n" +
		"- Run `/context` to see what is actually filling the window: it is often one big file read or one " +
		"long command output.\n" +
		"- Run `/clear` between unrelated tasks, rather than carrying the last task's history into the next one.\n" +
		"- Run `/compact` at a natural break, while you can still say what matters, rather than waiting for " +
		"the automatic one to fire in the middle of something.\n" +
		"- For a change that lasts, lower \"autoCompactWindow\" in ~/.claude/settings.json so compaction starts " +
		"earlier. It takes a token count from 100k to 1M, written like \"150k\", or \"auto\".\n\n" +
		"Codex has no equivalent setting; there, starting a new session between tasks is the lever."

	f.WhatToExpect = fmt.Sprintf(
		"The share of turns running above %s tokens should fall, and sessions should stay quick and accurate "+
			"for longer before they need clearing.", fmtInt(threshold))

	f.Evidence = Table{Columns: []string{
		"session", "project",
		"turns over " + fmtInt(threshold/1000) + "k",
		"peak context", "tax",
	}}
	for _, h := range hits {
		f.Evidence.Rows = append(f.Evidence.Rows, []string{
			shortID(h.id),
			h.project,
			fmtInt(int64(h.turnsOver)),
			fmtInt(h.peak),
			fmtUSD(h.tax),
		})
	}
	return f, nil
}
