package ledger

import (
	"github.com/magna-nz/tallybook/internal/model"
	"sort"

	"github.com/magna-nz/tallybook/internal/pricing"
	"github.com/magna-nz/tallybook/internal/store"
)

// AgentRow is one sub-agent type's rollup across every run in the window.
type AgentRow struct {
	Agent       string
	Runs        int
	Model       string // most common canonical model across the agent's runs
	Effort      string // most common effort level across the agent's turns; "" when none recorded
	AvgUSD      float64
	ReadOnlyPct float64
	Errors      int
	Mismatched  int // requested model differs from what the run actually used (pricing.SameModel)
}

// agentAgg accumulates one agent type's rollup while walking sessions.
type agentAgg struct {
	runs        int
	modelCounts map[string]int
	effortCount map[string]int
	usdTotal    float64
	readOnly    int
	errors      int
	mismatched  int
}

// Agents returns spend broken down by sub-agent type, for sessions matching
// f that are sub-agent runs (AgentID != "").
func Agents(st *store.Store, pr *pricing.Table, f store.Filter) ([]AgentRow, error) {
	rows, err := st.Sessions(f)
	if err != nil {
		return nil, err
	}
	toolCounts, err := st.ToolCounts(f)
	if err != nil {
		return nil, err
	}

	byAgent := map[string]*agentAgg{}

	for _, r := range rows {
		if r.AgentID == "" || r.AgentType == "" {
			continue
		}
		a, ok := byAgent[r.AgentType]
		if !ok {
			a = &agentAgg{modelCounts: map[string]int{}, effortCount: map[string]int{}}
			byAgent[r.AgentType] = a
		}
		a.runs++
		a.errors += r.ToolErrors

		turns, err := st.Turns(r.ID)
		if err != nil {
			return nil, err
		}
		var sessionUSD float64
		for _, t := range turns {
			usd, _ := pr.CostTurn(t)
			sessionUSD += usd
			if t.Effort != "" {
				a.effortCount[t.Effort]++
			}
			if t.Model == "" {
				continue
			}
			id, ok := pr.Canonical(t.Model)
			if !ok {
				id = t.Model
			}
			a.modelCounts[id]++
		}
		a.usdTotal += sessionUSD

		if model.AllReadOnly(toolCounts[r.ID]) {
			a.readOnly++
		}

		if actual := mostCommon(modelCountsFor(turns, pr)); r.RequestedModel != "" && actual != "" && !pr.SameModel(r.RequestedModel, actual) {
			a.mismatched++
		}
	}

	out := make([]AgentRow, 0, len(byAgent))
	for name, a := range byAgent {
		out = append(out, AgentRow{
			Agent:       name,
			Runs:        a.runs,
			Model:       mostCommon(a.modelCounts),
			Effort:      mostCommon(a.effortCount),
			AvgUSD:      a.usdTotal / float64(a.runs),
			ReadOnlyPct: float64(a.readOnly) / float64(a.runs),
			Errors:      a.errors,
			Mismatched:  a.mismatched,
		})
	}

	sort.Slice(out, func(i, j int) bool {
		si := out[i].AvgUSD * float64(out[i].Runs)
		sj := out[j].AvgUSD * float64(out[j].Runs)
		if si != sj {
			return si > sj
		}
		return out[i].Agent < out[j].Agent
	})
	return out, nil
}

// mostCommon returns the key with the highest count, breaking ties
// alphabetically for determinism.
func mostCommon(counts map[string]int) string {
	best := ""
	bestN := -1
	for k, n := range counts {
		if n > bestN || (n == bestN && k < best) {
			best, bestN = k, n
		}
	}
	return best
}

// modelCountsFor tallies the canonical model of each turn.
func modelCountsFor(turns []model.Turn, pr *pricing.Table) map[string]int {
	counts := map[string]int{}
	for _, t := range turns {
		if t.Model == "" {
			continue
		}
		id, ok := pr.Canonical(t.Model)
		if !ok {
			id = t.Model
		}
		counts[id]++
	}
	return counts
}
