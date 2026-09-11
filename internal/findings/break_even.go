package findings

import (
	"fmt"

	"github.com/magna-nz/tallybook/internal/config"
	"github.com/magna-nz/tallybook/internal/model"
)

// breakEvenRule compares what a Max or Pro plan costs against what the same
// usage would cost priced at list, so a subscription user can see whether the
// flat fee is still the cheaper way to pay. On API billing this question does
// not exist: the bill already is the usage.
type breakEvenRule struct{}

func (breakEvenRule) ID() string { return "subscription-break-even" }

// sourceDisplay is the name to use in a sentence for a transcript source.
func sourceDisplay(s model.Source) string {
	switch s {
	case model.SourceClaudeCode:
		return "Claude Code"
	case model.SourceCodex:
		return "Codex"
	}
	return string(s)
}

func (breakEvenRule) Run(in Input) (*Finding, error) {
	if in.Plan != config.PlanSubscription || in.Cfg.PlanPriceUSD <= 0 || in.WindowDays < 14 {
		return nil, nil
	}
	plan := in.Cfg.PlanPriceUSD
	usage := in.PerMonth(in.TotalUSD)

	rows, err := in.Store.Sessions(in.Filter)
	if err != nil {
		return nil, err
	}
	perSource := map[model.Source]float64{}
	for _, row := range rows {
		turns, err := in.Store.Turns(row.ID)
		if err != nil {
			return nil, err
		}
		perSource[row.Source] += costOf(in, turns)
	}

	f := &Finding{Direction: Config}

	if usage < plan {
		saving := plan - usage
		// SavingUSD above is already a monthly figure, so SavingShare has to
		// undo that scaling before calling in.Share, which expects a saving
		// measured over the window rather than normalised to 30 days.
		windowSaving := saving * in.WindowDays / 30
		f.SavingUSD = saving
		f.SavingShare = in.Share(windowSaving)
		f.Confidence = Low
		f.Title = "Your usage is worth less at list price than the plan costs"

		f.WhatHappened = fmt.Sprintf(
			"At list price, your usage %s works out to about %s a month. The plan you pay for costs %s a month.",
			windowPhrase(in), fmtUSD(usage), fmtUSD(plan))

		f.WhyItCosts = fmt.Sprintf(
			"The plan is a flat fee, so it costs the same whether you use it heavily or lightly. At this rate, "+
				"the same usage billed through the API would cost about %s against the %s you pay for the plan.",
			fmtUSD(usage), fmtUSD(plan))

		f.WhatToChange = "This is a decision, not a setting: if the pattern holds for a couple more months, API " +
			"billing (or a smaller plan) would cost less at list price. Rate limits, the features that come " +
			"bundled with the plan, and the one-hour cache default differ between the two, so the dollar figure " +
			"above is not the whole picture."

		f.WhatToExpect = "Nothing changes in your reports either way. Re-check this after another month of usage."
	} else {
		saving := usage - plan
		f.SavingUSD = 0
		f.SavingShare = 0
		f.Confidence = Info
		f.Title = "Your plan is paying for itself"

		f.WhatHappened = fmt.Sprintf(
			"At list price, your usage %s works out to about %s a month, more than the %s a month the plan costs.",
			windowPhrase(in), fmtUSD(usage), fmtUSD(plan))

		f.WhyItCosts = fmt.Sprintf(
			"This is not a cost to fix. At list price you are getting about %s a month more value than the plan "+
				"charges, which is the point of a flat fee.", fmtUSD(saving))

		f.WhatToChange = "Nothing to change here. Keep an eye on it if your usage drops, since the comparison " +
			"can flip the other way."

		f.WhatToExpect = "Nothing changes in your reports. Re-check this after another month of usage."
	}

	f.Evidence = Table{Columns: []string{"source", "list price / 30 days", "share of plan"}}
	total := 0.0
	for _, src := range []model.Source{model.SourceClaudeCode, model.SourceCodex} {
		monthly := in.PerMonth(perSource[src])
		total += monthly
		share := 0.0
		if plan > 0 {
			share = monthly / plan
		}
		f.Evidence.Rows = append(f.Evidence.Rows, []string{
			sourceDisplay(src), fmtUSD(monthly), fmtPct(share) + "%",
		})
	}
	totalShare := 0.0
	if plan > 0 {
		totalShare = total / plan
	}
	f.Evidence.Rows = append(f.Evidence.Rows, []string{"total", fmtUSD(total), fmtPct(totalShare) + "%"})

	return f, nil
}
