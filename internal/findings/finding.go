// Package findings holds the rules that turn the ledger into advice. Each
// rule is a query over the store plus a plain-English explanation. Rules
// never write anything; the most they do is print a patch for the user to
// apply.
package findings

import (
	"github.com/magna-nz/tallybook/internal/agentfile"
	"sort"
	"time"

	"github.com/magna-nz/tallybook/internal/config"
	"github.com/magna-nz/tallybook/internal/pricing"
	"github.com/magna-nz/tallybook/internal/store"
)

// Confidence is how sure the rule is that acting on the finding is safe.
type Confidence string

const (
	High   Confidence = "high"
	Medium Confidence = "medium"
	Low    Confidence = "low"
	Info   Confidence = "info" // nothing to save; a pointer, not a recommendation
)

// Direction says what kind of change the finding asks for, so the report
// can group them and so "do not downgrade" findings are never mistaken for
// savings.
type Direction string

const (
	Downgrade Direction = "downgrade" // move work to a cheaper model
	Upgrade   Direction = "upgrade"   // this work needs a stronger model or a better brief
	Context   Direction = "context"   // shrink what is sent every turn
	Cache     Direction = "cache"     // keep the prompt cache warm
	Effort    Direction = "effort"    // lower thinking effort
	Config    Direction = "config"    // a setting is not doing what the user thinks
)

// Table is the evidence behind a finding, rendered as-is by the report.
type Table struct {
	Columns []string
	Rows    [][]string
}

// Finding is one piece of advice. The four text fields are complete
// paragraphs in plain English, each answering the question its name asks.
// They must not use jargon without a gloss and must name exact files and
// lines when there is something to change.
type Finding struct {
	ID          string  // rule slug, stable across versions
	Title       string  // one line, sentence case, no trailing period
	SavingUSD   float64 // estimated saving per 30 days at list price; 0 for Info
	SavingShare float64 // the same saving as a fraction of the window's total spend, 0..1
	Confidence  Confidence
	Direction   Direction

	WhatHappened string
	WhyItCosts   string
	WhatToChange string // may contain an indented snippet on its own lines
	WhatToExpect string

	Patch    string // optional unified diff for --patch; empty when there is no file to change
	Evidence Table
}

// Input is everything a rule may look at.
type Input struct {
	Store  *store.Store
	Prices *pricing.Table
	// Agents is what the project's own sub-agent files say, so advice can be
	// checked against the config rather than assuming it. A nil Set simply
	// knows nothing and the advice falls back to the general case.
	Agents agentfile.Set
	Cfg    config.Findings
	Plan   config.Plan
	Filter store.Filter // the reporting window; Until zero means Now
	Now    time.Time

	// TotalUSD is the list-price total for every turn in the window, so a rule
	// can express its saving as a share. WindowDays is the window length, so a
	// rule can normalise a saving to 30 days.
	TotalUSD   float64
	WindowDays float64
}

// PerMonth scales a saving measured over the window to a 30-day figure.
func (in Input) PerMonth(savingInWindow float64) float64 {
	if in.WindowDays <= 0 {
		return savingInWindow
	}
	return savingInWindow * 30 / in.WindowDays
}

// Share expresses a window saving as a fraction of the window total.
func (in Input) Share(savingInWindow float64) float64 {
	if in.TotalUSD <= 0 {
		return 0
	}
	return savingInWindow / in.TotalUSD
}

// Rule is one check. Run returns nil when there is nothing worth saying.
type Rule interface {
	ID() string
	Run(in Input) (*Finding, error)
}

// Run executes every registered rule except those disabled in the config,
// and returns findings sorted: savings first by descending SavingUSD, then
// Info findings, in registration order within ties.
func Run(in Input) ([]Finding, error) {
	disabled := map[string]bool{}
	for _, id := range in.Cfg.Disabled {
		disabled[id] = true
	}
	var out []Finding
	for _, r := range All() {
		if disabled[r.ID()] {
			continue
		}
		f, err := r.Run(in)
		if err != nil {
			return nil, err
		}
		if f != nil {
			f.ID = r.ID()
			out = append(out, *f)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if (a.Confidence == Info) != (b.Confidence == Info) {
			return a.Confidence != Info
		}
		return a.SavingUSD > b.SavingUSD
	})
	return out, nil
}
