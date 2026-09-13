package findings

import (
	"fmt"
	"sort"
	"time"

	"github.com/magna-nz/tallybook/internal/config"
)

// Thresholds below will move into config.Findings alongside the other rules'
// knobs once that wiring lands; for now they are fixed here.
const (
	// cache1hGapMinutes is the longest gap between two consecutive turns that
	// the five-minute cache lifetime could still have served without
	// expiring. A session with every gap at or under this counts as one that
	// never needed the hour.
	cache1hGapMinutes = 5
	// cache1hMinSessions is the fewest qualifying sessions worth reporting.
	cache1hMinSessions = 3
)

// cache1hRule is the mirror of cacheRebuildRule: that rule finds sessions
// whose pauses let the cache expire and paid to rebuild it; this one finds
// sessions that paid for the one-hour cache lifetime and, because every pause
// was short, never once needed more than the five-minute default.
type cache1hRule struct{}

func (cache1hRule) ID() string { return "cache-1h-without-pauses" }

// cache1hSession is one session that spent its cache writes on the hour-long
// lifetime without a pause long enough to justify it.
type cache1hSession struct {
	id         string
	project    string
	model      string // the model most of the session's turns ran on
	longestGap time.Duration
	tokens1h   int64
	premium    float64
}

func (cache1hRule) Run(in Input) (*Finding, error) {
	rows, err := in.Store.Sessions(in.Filter)
	if err != nil {
		return nil, err
	}

	gap := cache1hGapMinutes * time.Minute

	var hits []cache1hSession
	for _, row := range rows {
		turns, err := in.Store.Turns(row.ID)
		if err != nil {
			return nil, err
		}
		// One turn has no gap to judge, so it can say nothing about lifetimes.
		if len(turns) < 2 {
			continue
		}

		var write1h, write5m int64
		var longestGap time.Duration
		var premium float64
		withinLifetime := true
		for i, t := range turns {
			write1h += t.Usage.CacheWrite1h
			write5m += t.Usage.CacheWrite5m
			if rate, ok := in.Prices.RateForTurn(t.Model, t); ok {
				premium += float64(t.Usage.CacheWrite1h) * (rate.CacheWrite1h - rate.CacheWrite5m) / 1e6
			}
			if i == 0 {
				continue
			}
			pause := t.Timestamp.Sub(turns[i-1].Timestamp)
			if pause > longestGap {
				longestGap = pause
			}
			if pause > gap {
				withinLifetime = false
			}
		}
		if !withinLifetime || write1h <= write5m {
			continue
		}

		hits = append(hits, cache1hSession{
			id:         row.ID,
			project:    row.Project,
			model:      mostCommonModel(turns),
			longestGap: longestGap,
			tokens1h:   write1h,
			premium:    premium,
		})
	}

	if len(hits) < cache1hMinSessions {
		return nil, nil
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].premium != hits[j].premium {
			return hits[i].premium > hits[j].premium
		}
		return hits[i].id < hits[j].id
	})

	var totalPremium float64
	var totalTokens1h int64
	longestOverall := time.Duration(0)
	for _, h := range hits {
		totalPremium += h.premium
		totalTokens1h += h.tokens1h
		if h.longestGap > longestOverall {
			longestOverall = h.longestGap
		}
	}
	if totalPremium < 1 {
		return nil, nil
	}

	f := &Finding{
		Direction: Cache,
		Title:     "An hour of cache lifetime went unused",
		SavingUSD: in.PerMonth(totalPremium),
	}
	f.SavingShare = in.Share(totalPremium)

	f.WhatHappened = fmt.Sprintf(
		"In %s session%s %s, most of the cache writes were made with the hour-long lifetime, but the longest "+
			"pause between turns in any of them was only %s minutes. %s tokens were written under the hour-long "+
			"lifetime in total.",
		fmtInt(int64(len(hits))), plural(len(hits)), windowPhrase(in), fmtMinutes(longestOverall), fmtInt(totalTokens1h))

	f.WhyItCosts = cache1hWhyItCosts(in, hits[0].model)

	if in.Plan == config.PlanSubscription {
		f.Confidence = Low
		f.WhatToChange = cache1hWhatToChangeSubscription
		f.WhatToExpect = "There is nothing to act on today."
	} else {
		f.Confidence = High
		f.WhatToChange = cache1hWhatToChangeAPI
		f.WhatToExpect = "Switching the setting should remove this premium from next month's bill, " +
			"with nothing else about these sessions changing."
	}

	f.Evidence = Table{Columns: []string{"session", "project", "longest pause", "1h tokens written", "premium"}}
	for _, h := range hits {
		f.Evidence.Rows = append(f.Evidence.Rows, []string{
			shortID(h.id),
			h.project,
			fmtMinutes(h.longestGap) + " min",
			fmtInt(h.tokens1h),
			fmtUSD(h.premium),
		})
	}
	return f, nil
}

// cache1hRatio is how many times more the hour-long cache write costs than
// the five-minute one, for a given model at a given moment. It returns 0
// when the rate is unknown, which callers must treat as "no ratio to quote".
//
// The standard tier is enough: a fast or long-context tier multiplies both
// write columns by the same factor, so the 1h-to-5m ratio does not move.
func cache1hRatio(in Input, modelID string, at time.Time) float64 {
	r, ok := in.Prices.LookupAt(modelID, at)
	if !ok || r.CacheWrite5m <= 0 {
		return 0
	}
	return r.CacheWrite1h / r.CacheWrite5m
}

func cache1hWhyItCosts(in Input, modelID string) string {
	name := modelDisplay(modelID)
	ratio := cache1hRatio(in, modelID, windowUntil(in))
	rate := "noticeably more than"
	if ratio > 0 {
		rate = fmt.Sprintf("about %sx", fmtOneDP(ratio))
	}
	return fmt.Sprintf(
		"Writing to the cache with the hour-long lifetime costs %s the five-minute write on %s. "+
			"A lifetime that is never used before it would have expired anyway buys nothing.", rate, name)
}

// Setting names verified 2026-09-11 against code.claude.com/docs/en/settings-reference.
const cache1hCodex = "\n\nCodex has no equivalent setting."

const cache1hWhatToChangeAPI = "The main conversation's lifetime is chosen with \"promptCacheTtl\" in " +
	"~/.claude/settings.json; the only accepted values are \"5m\" and \"1h\". If it is set to \"1h\", set it " +
	"to \"5m\" or remove the line so Claude Code falls back to the five-minute default. The same file takes " +
	"\"subagentPromptCacheTtl\" for sub-agents." + cache1hCodex

const cache1hWhatToChangeSubscription = "On a subscription plan, the hour is the default Claude Code picks " +
	"within your plan's usage and is not billed per token. The \"promptCacheTtl\" setting in " +
	"~/.claude/settings.json only starts to matter once usage credits or an API key are paying for the tokens." +
	cache1hCodex
