package findings

import (
	"fmt"
	"sort"
	"time"
)

// cacheRebuildRule finds sessions where a pause let the saved conversation
// expire, so the whole prompt was sent and charged again at the write rate.
type cacheRebuildRule struct{}

func (cacheRebuildRule) ID() string { return "cache-rebuilt-mid-session" }

// rebuiltSession is one session that paid to re-send its own history.
type rebuiltSession struct {
	id          string
	project     string
	rebuilds    int
	longestGap  time.Duration
	shortestGap time.Duration
	wasted      float64
	// on1h is true when this session's cache writes were already taking the
	// one-hour lifetime. The transcript records which was used, so the advice
	// does not have to guess.
	on1h bool
}

func (cacheRebuildRule) Run(in Input) (*Finding, error) {
	rows, err := in.Store.Sessions(in.Filter)
	if err != nil {
		return nil, err
	}

	minRebuilds := in.Cfg.MinCacheRebuilds
	if minRebuilds <= 0 {
		minRebuilds = 3
	}
	gapMinutes := in.Cfg.CacheGapMinutes
	if gapMinutes <= 0 {
		gapMinutes = 5
	}
	gap := time.Duration(gapMinutes) * time.Minute

	var hits []rebuiltSession
	for _, row := range rows {
		turns, err := in.Store.Turns(row.ID)
		if err != nil {
			return nil, err
		}
		s := rebuiltSession{id: row.ID, project: row.Project}
		for i := 1; i < len(turns); i++ {
			prev, cur := turns[i-1], turns[i]
			prevContext := prev.Usage.ContextTokens()
			if prevContext <= 0 {
				continue
			}
			written := cur.Usage.CacheWrite5m + cur.Usage.CacheWrite1h
			if float64(written) < 0.5*float64(prevContext) {
				continue
			}
			pause := cur.Timestamp.Sub(prev.Timestamp)
			if pause < gap {
				continue
			}

			s.rebuilds++
			if cur.Usage.CacheWrite1h > cur.Usage.CacheWrite5m {
				s.on1h = true
			}
			if pause > s.longestGap {
				s.longestGap = pause
			}
			if s.shortestGap == 0 || pause < s.shortestGap {
				s.shortestGap = pause
			}

			// What the re-send cost, less what reading the same tokens back
			// out of the cache would have cost.
			if rate, ok := in.Prices.LookupAt(cur.Model, cur.Timestamp); ok {
				write := float64(cur.Usage.CacheWrite5m)*rate.CacheWrite5m +
					float64(cur.Usage.CacheWrite1h)*rate.CacheWrite1h
				read := float64(written) * rate.CacheRead
				s.wasted += (write - read) / 1e6
			}
		}
		if s.rebuilds >= minRebuilds {
			hits = append(hits, s)
		}
	}
	if len(hits) == 0 {
		return nil, nil
	}

	sort.Slice(hits, func(i, j int) bool { return hits[i].wasted > hits[j].wasted })

	var wasted float64
	totalRebuilds := 0
	shortest, longest := hits[0].shortestGap, time.Duration(0)
	for _, h := range hits {
		wasted += h.wasted
		totalRebuilds += h.rebuilds
		if h.shortestGap < shortest {
			shortest = h.shortestGap
		}
		if h.longestGap > longest {
			longest = h.longestGap
		}
	}

	f := &Finding{
		Direction:   Cache,
		Confidence:  Medium,
		SavingUSD:   in.PerMonth(wasted),
		SavingShare: in.Share(wasted),
		Title:       "Your saved context was rebuilt mid-session",
	}

	f.WhatHappened = fmt.Sprintf(
		"In %s session%s %s, the conversation so far was re-sent and charged at the full rate %s times in total, "+
			"instead of being reused from the cache.",
		fmtInt(int64(len(hits))), plural(len(hits)), windowPhrase(in), fmtInt(int64(totalRebuilds)))

	pauses := fmt.Sprintf("pauses of %s to %s minutes", fmtMinutes(shortest), fmtMinutes(longest))
	if fmtMinutes(shortest) == fmtMinutes(longest) {
		pauses = fmt.Sprintf("pauses of about %s minutes", fmtMinutes(longest))
	}
	f.WhyItCosts = fmt.Sprintf(
		"The API remembers the start of your conversation for a short time so it only bills the new part of each "+
			"turn. When more than %d minutes pass between turns, that memory expires and the whole conversation "+
			"is charged again at up to ten times the cache-read price. In these sessions it happened during %s.",
		gapMinutes, pauses)

	already1h := 0
	for _, h := range hits {
		if h.on1h {
			already1h++
		}
	}
	f.WhatToChange = cacheWhatToChange(already1h, len(hits), longest)
	if already1h == len(hits) {
		f.WhatToExpect = "Nothing to change in settings. The pauses themselves are the only lever left, " +
			"so this is worth knowing rather than acting on."
	} else {
		f.WhatToExpect = "Cache rebuilds should drop to once or twice per session. " +
			"Sessions with long pauses will see the biggest difference."
	}

	f.Evidence = Table{Columns: []string{"session", "project", "rebuilds", "longest pause", "wasted"}}
	for _, h := range hits {
		f.Evidence.Rows = append(f.Evidence.Rows, []string{
			shortID(h.id),
			h.project,
			fmtInt(int64(h.rebuilds)),
			fmtMinutes(h.longestGap) + " min",
			fmtUSD(h.wasted),
		})
	}
	return f, nil
}

// cacheWhatToChange writes the advice, which depends on what the transcripts
// already show. Claude Code records whether each cache write took the
// five-minute or the one-hour lifetime, so a session already on the long one
// needs different advice from a session on the short one.
//
// Setting names verified 2026-09-11 against code.claude.com/docs/en/settings-reference.
func cacheWhatToChange(already1h, total int, longest time.Duration) string {
	const codex = "\n\nCodex has no equivalent setting. There, shorter pauses are the only lever."

	if already1h == total {
		return fmt.Sprintf(
			"Nothing to turn on: these sessions were already keeping the conversation for an hour, and the "+
				"pauses ran past it anyway, up to %s minutes. An hour is as long as the memory gets, so the "+
				"only thing that would help is coming back sooner, or starting a fresh session rather than "+
				"resuming a long one after a break.", fmtMinutes(longest)) + codex
	}

	advice := "Claude Code can keep the conversation for an hour instead of five minutes. Add this to " +
		"~/.claude/settings.json, as a top-level key rather than inside the \"env\" block:\n\n" +
		"    \"promptCacheTtl\": \"1h\"\n\n" +
		"For sub-agents, the same file takes \"subagentPromptCacheTtl\". Both need Claude Code 2.1.242 or " +
		"newer, and both accept only \"5m\" or \"1h\"; any other value is ignored without a word.\n\n" +
		"Hour-long entries cost more to write, so this pays off when your pauses sit between five minutes and " +
		"an hour, and not when they are usually longer than that."

	if already1h > 0 {
		advice += fmt.Sprintf("\n\nNote that %s of these %s sessions were already on the hour, so the setting "+
			"will not help those. Their pauses simply ran past it.",
			fmtInt(int64(already1h)), fmtInt(int64(total)))
	}
	return advice + codex
}
