package ledger

import (
	"github.com/magna-nz/tallybook/internal/model"
	"github.com/magna-nz/tallybook/internal/pricing"
	"github.com/magna-nz/tallybook/internal/store"
)

// SessionCost is a session priced at list rates.
type SessionCost struct {
	store.SessionRow
	USD   float64
	Known bool // false if any turn's model was not found in the price table
}

// Sessions returns every session matching f, each priced at the rate in
// force when its turns happened.
func Sessions(st *store.Store, pr *pricing.Table, f store.Filter) ([]SessionCost, error) {
	rows, err := st.Sessions(f)
	if err != nil {
		return nil, err
	}

	out := make([]SessionCost, 0, len(rows))
	for _, r := range rows {
		turns, err := st.Turns(r.ID)
		if err != nil {
			return nil, err
		}
		sc := SessionCost{SessionRow: r, Known: true}
		for _, t := range turns {
			usd, known := pr.CostTurn(t)
			sc.USD += usd
			if !known {
				sc.Known = false
			}
		}
		out = append(out, sc)
	}
	return out, nil
}

// Totals rolls up a set of priced sessions.
type Totals struct {
	USD, MainUSD, SubagentUSD  float64
	Sessions, Subagents, Turns int
	Usage                      model.Usage
	ByModel                    map[string]float64 // canonical model id -> USD
	UnknownModels              map[string]int     // model id as written -> turn count
	BySource                   map[model.Source]SourceTotal
}

// SourceTotal is the slice of the totals that came from one tool.
type SourceTotal struct {
	USD      float64
	Sessions int // main sessions, not sub-agent runs
	Turns    int
}

// Total rolls sessions up into a Totals. st and pr are used to re-walk each
// session's turns for the per-model breakdown.
func Total(sessions []SessionCost, st *store.Store, pr *pricing.Table) (Totals, error) {
	tot := Totals{
		ByModel:       map[string]float64{},
		UnknownModels: map[string]int{},
		BySource:      map[model.Source]SourceTotal{},
	}

	for _, sc := range sessions {
		tot.USD += sc.USD
		tot.Sessions++
		tot.Turns += sc.Turns

		if sc.AgentID != "" {
			tot.Subagents++
			tot.SubagentUSD += sc.USD
		} else {
			tot.MainUSD += sc.USD
		}
		src := tot.BySource[sc.Source]
		src.USD += sc.USD
		src.Turns += sc.Turns
		if sc.AgentID == "" {
			src.Sessions++
		}
		tot.BySource[sc.Source] = src

		tot.Usage.Input += sc.Usage.Input
		tot.Usage.CacheRead += sc.Usage.CacheRead
		tot.Usage.CacheWrite5m += sc.Usage.CacheWrite5m
		tot.Usage.CacheWrite1h += sc.Usage.CacheWrite1h
		tot.Usage.Output += sc.Usage.Output
		tot.Usage.Thinking += sc.Usage.Thinking

		turns, err := st.Turns(sc.ID)
		if err != nil {
			return Totals{}, err
		}
		for _, t := range turns {
			usd, known := pr.CostTurn(t)
			if !known {
				tot.UnknownModels[t.Model]++
				continue
			}
			id, ok := pr.Canonical(t.Model)
			if !ok {
				id = t.Model
			}
			tot.ByModel[id] += usd
		}
	}

	return tot, nil
}

// CacheHitRate is the share of everything sent to the model that was read
// back from the prompt cache rather than processed afresh: cache reads over
// every input-side token, which is fresh input, cache reads and both kinds
// of cache write. It is the one number that says whether caching is doing
// its job. Zero when nothing was sent.
func (t Totals) CacheHitRate() float64 {
	sent := t.Usage.ContextTokens()
	if sent <= 0 {
		return 0
	}
	return float64(t.Usage.CacheRead) / float64(sent)
}
