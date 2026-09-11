package ledger

import (
	"sort"
	"time"

	"github.com/magna-nz/tallybook/internal/pricing"
	"github.com/magna-nz/tallybook/internal/store"
)

// Verdict is the plain-English judgment attached to a model change.
type Verdict string

const (
	VerdictKeep     Verdict = "keep"      // cheaper and errors did not rise materially
	VerdictWatch    Verdict = "watch"     // cheaper but errors rose, or dearer but errors fell
	VerdictRevert   Verdict = "revert"    // dearer and errors did not fall, or errors rose sharply
	VerdictTooEarly Verdict = "too early" // fewer than minRuns on either side
)

// errorRiseRevert and errorTolerance are percentage points (as a fraction,
// 0..1) that decide the Verdict. See verdictOf.
const (
	errorRiseRevert = 0.10 // an error-rate jump bigger than this always reverts
	errorTolerance  = 0.02 // errors may move this much without changing the cost verdict
	costMoveShare   = 0.05 // a cost move smaller than this counts as "about the same"
)

// Side is one half of a model change: the runs before it, or the runs after
// it.
type Side struct {
	Runs      int
	AvgUSD    float64 // mean cost per run
	ErrorRate float64 // errored tool results / all tool results, 0..1
	AvgTurns  float64
}

// Change is a point where a sub-agent type's model flipped, with the runs on
// either side of the flip compared.
type Change struct {
	Agent         string
	From, To      string // canonical model ids
	At            time.Time
	Before, After Side
	Verdict       Verdict
	// MinRuns is the threshold this change was judged against, so a reader
	// told it is "too early" can be given the right number of further runs.
	MinRuns int
}

// runStat is one sub-agent run's derived stats: enough to place it on the
// agent's timeline and compare it against its neighbours.
type runStat struct {
	startedAt time.Time
	endedAt   time.Time
	model     string // canonical model id, resolved via pricing.Canonical
	usd       float64
	turns     int
	errors    int
	results   int
}

// Changes finds every point where a sub-agent type's model changed, and
// compares the runs either side of it.
func Changes(st *store.Store, pr *pricing.Table, f store.Filter, minRuns int) ([]Change, error) {
	rows, err := st.Sessions(f)
	if err != nil {
		return nil, err
	}

	byAgent := map[string][]runStat{}
	for _, r := range rows {
		if r.AgentID == "" || r.AgentType == "" {
			continue // not a sub-agent run
		}

		turns, err := st.Turns(r.ID)
		if err != nil {
			return nil, err
		}
		// The run's actual model is the most common canonical model across
		// its turns; a run with no turn priced by the table cannot be placed
		// on the timeline, so it is skipped rather than read as a change.
		canonical := mostCommon(modelCountsFor(turns, pr))
		if canonical == "" {
			continue
		}

		var usd float64
		for _, t := range turns {
			v, _ := pr.CostAt(t.Model, t.Usage, t.Timestamp)
			usd += v
		}

		results, err := st.ToolResults(r.ID)
		if err != nil {
			return nil, err
		}
		errs := 0
		for _, tr := range results {
			if tr.IsError {
				errs++
			}
		}

		byAgent[r.AgentType] = append(byAgent[r.AgentType], runStat{
			startedAt: r.StartedAt,
			endedAt:   r.EndedAt,
			model:     canonical,
			usd:       usd,
			turns:     r.Turns,
			errors:    errs,
			results:   len(results),
		})
	}

	var changes []Change
	for agent, runs := range byAgent {
		sort.Slice(runs, func(i, j int) bool { return runs[i].startedAt.Before(runs[j].startedAt) })

		// Split into maximal runs of consecutive same-model runs. Each
		// boundary between two segments is one change.
		var segments [][]runStat
		start := 0
		for i := 1; i <= len(runs); i++ {
			if i < len(runs) && runs[i].model == runs[i-1].model {
				continue
			}
			segments = append(segments, runs[start:i])
			start = i
		}

		for i := 1; i < len(segments); i++ {
			before, after := segments[i-1], segments[i]
			if len(before) == 0 || len(after) == 0 {
				continue
			}
			// Two models running side by side are not a change from one to the
			// other. Dispatching a wave of sub-agents with mixed models puts
			// runs of both on the timeline at once, and ordering them by start
			// time alone makes that look like a switch that never happened.
			if overlaps(before, after) {
				continue
			}
			beforeSide, afterSide := sideOf(before), sideOf(after)
			changes = append(changes, Change{
				Agent:   agent,
				From:    before[0].model,
				To:      after[0].model,
				At:      after[0].startedAt,
				Before:  beforeSide,
				After:   afterSide,
				Verdict: verdictOf(beforeSide, afterSide, minRuns),
				MinRuns: minRuns,
			})
		}
	}

	// Decided verdicts first, then the undecided ones, each newest first. A
	// change worth acting on must not be buried under a run of "too early"
	// blocks produced by flipping a model back and forth.
	sort.SliceStable(changes, func(i, j int) bool {
		ei, ej := changes[i].Verdict == VerdictTooEarly, changes[j].Verdict == VerdictTooEarly
		if ei != ej {
			return ej
		}
		return changes[i].At.After(changes[j].At)
	})
	if len(changes) > 20 {
		changes = changes[:20]
	}
	return changes, nil
}

// overlaps reports whether any run in the earlier segment was still going when
// the later segment began. A run with no recorded end is treated as ending
// when it started, which is the reading that assumes least.
func overlaps(before, after []runStat) bool {
	if len(before) == 0 || len(after) == 0 {
		return false
	}
	start := after[0].startedAt
	for _, r := range after {
		if r.startedAt.Before(start) {
			start = r.startedAt
		}
	}
	for _, r := range before {
		end := r.endedAt
		if end.IsZero() || end.Before(r.startedAt) {
			end = r.startedAt
		}
		if end.After(start) {
			return true
		}
	}
	return false
}

// sideOf rolls up one contiguous run of same-model runs into a Side.
func sideOf(runs []runStat) Side {
	var usd float64
	var turns, errs, results int
	for _, r := range runs {
		usd += r.usd
		turns += r.turns
		errs += r.errors
		results += r.results
	}
	n := float64(len(runs))
	s := Side{Runs: len(runs), AvgUSD: usd / n, AvgTurns: float64(turns) / n}
	if results > 0 {
		s.ErrorRate = float64(errs) / float64(results)
	}
	return s
}

// verdictOf judges a change from the stats on either side of it.
func verdictOf(before, after Side, minRuns int) Verdict {
	if before.Runs < minRuns || after.Runs < minRuns {
		return VerdictTooEarly
	}

	errDelta := after.ErrorRate - before.ErrorRate // positive = errors rose
	if errDelta > errorRiseRevert {
		return VerdictRevert
	}

	fell := before.AvgUSD > 0 && after.AvgUSD <= before.AvgUSD*(1-costMoveShare)
	rose := before.AvgUSD > 0 && after.AvgUSD >= before.AvgUSD*(1+costMoveShare)

	if fell && errDelta <= errorTolerance {
		return VerdictKeep
	}
	if rose && -errDelta <= errorTolerance {
		return VerdictRevert
	}
	return VerdictWatch
}
