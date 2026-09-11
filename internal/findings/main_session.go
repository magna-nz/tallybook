package findings

import (
	"fmt"
	"sort"
	"strings"

	"github.com/magna-nz/tallybook/internal/model"
	"github.com/magna-nz/tallybook/internal/pricing"
)

// mainSessionRule finds main sessions that were a question and an answer and
// nothing more, yet ran on the strongest model available.
//
// Every other downgrade finding in this package is about sub-agents, while the
// main session is where nearly all of the money goes. A session that ran to a
// handful of turns and only read the project never needed the expensive model,
// and there is nothing to edit to fix it: the change is which model you start a
// quick question on, which is why the advice is a command rather than a file.
type mainSessionRule struct{}

func (mainSessionRule) ID() string { return "main-session-on-strong-model" }

// mainSessionMaxTurns is the most turns a main session can run to and still be
// a look-up rather than a piece of work. Ten is generous for "what does this
// function do"; past that the session is building something and the strong
// model may well be earning its keep. This will be wired to the config later.
const mainSessionMaxTurns = 10

// mainSessionMinSessions is the evidence floor. One quick question on the
// strong model is an accident, three is a habit, and only a habit is worth a
// paragraph. This will be wired to the config later.
const mainSessionMinSessions = 3

// mainSessionHit is one short, read-only main session.
type mainSessionHit struct {
	id      string
	project string
	turns   int
	tools   map[string]int
	cost    float64
	altCost float64
}

func (h mainSessionHit) saving() float64 { return h.cost - h.altCost }

// mainGroup is every qualifying session that ran on one model, and so would
// move to one cheaper model.
type mainGroup struct {
	modelID string
	alt     string
	// codex is true when these sessions came from Codex, which has neither the
	// slash command nor the settings key the Claude Code advice names.
	codex   bool
	hits    []mainSessionHit
	current float64
	altCost float64
}

func (g *mainGroup) saving() float64 { return g.current - g.altCost }

func (r mainSessionRule) Run(in Input) (*Finding, error) {
	rows, err := in.Store.Sessions(in.Filter)
	if err != nil {
		return nil, err
	}
	counts, err := in.Store.ToolCounts(in.Filter)
	if err != nil {
		return nil, err
	}

	byKey := map[string]*mainGroup{}
	// mainTotal is every main session with any traffic in it, so the advice can
	// tell a habit worth changing the default for from an occasional look-up.
	maxTurns := in.Cfg.MainSessionTurns
	if maxTurns <= 0 {
		maxTurns = mainSessionMaxTurns
	}
	mainTotal := 0
	for _, row := range rows {
		if row.AgentID != "" {
			continue // only main sessions; the sub-agent case has its own rule
		}
		turns, err := in.Store.Turns(row.ID)
		if err != nil {
			return nil, err
		}
		if len(turns) == 0 {
			continue
		}
		mainTotal++

		if len(turns) > maxTurns {
			continue
		}
		// A session that called no tools at all counts as read-only: it was a
		// question and an answer, and it changed nothing.
		if !model.AllReadOnly(counts[row.ID]) {
			continue
		}
		ran := mostCommonModel(turns)
		if ran == "" {
			continue
		}
		canon := canonical(in, ran)
		if !strongTier(pricing.Tier(canon)) {
			continue
		}
		alt := pricing.CheaperAlternative(canon)
		if alt == "" {
			continue
		}

		// Keyed on the model and the alternative together, the same way the
		// sub-agent rule keys its groups: sessions on two different strong
		// models have two different models to move to, and merging them would
		// price the saving against a mixture while the advice named one of them.
		key := canon + "\x00" + alt
		g := byKey[key]
		if g == nil {
			g = &mainGroup{modelID: canon, alt: alt}
			byKey[key] = g
		}
		if row.Source == model.SourceCodex {
			g.codex = true
		}
		hit := mainSessionHit{
			id:      row.ID,
			project: row.Project,
			turns:   len(turns),
			tools:   counts[row.ID],
			cost:    costOf(in, turns),
			altCost: costAs(in, turns, alt),
		}
		g.hits = append(g.hits, hit)
		g.current += hit.cost
		g.altCost += hit.altCost
	}

	var groups []*mainGroup
	qualifying := 0
	for _, g := range byKey {
		groups = append(groups, g)
		qualifying += len(g.hits)
	}
	if qualifying < mainSessionMinSessions {
		return nil, nil
	}
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].saving() != groups[j].saving() {
			return groups[i].saving() > groups[j].saving()
		}
		return groups[i].modelID < groups[j].modelID
	})

	var saving, current, altTotal float64
	for _, g := range groups {
		saving += g.saving()
		current += g.current
		altTotal += g.altCost
	}
	if saving <= 0 {
		return nil, nil
	}

	f := &Finding{
		Direction:   Downgrade,
		Confidence:  Medium,
		SavingUSD:   in.PerMonth(saving),
		SavingShare: in.Share(saving),
		Title:       mainSessionTitle(groups),
	}
	var pairs [][2]string
	for _, g := range groups {
		pairs = append(pairs, [2]string{g.modelID, g.alt})
	}
	f.Confidence = softenedBy(f.Confidence, crossesTokenizer(pairs))

	f.WhatHappened = mainSessionWhatHappened(in, groups, qualifying, maxTurns, current)
	f.WhyItCosts = mainSessionWhyItCosts(in, groups[0])
	f.WhatToChange = mainSessionWhatToChange(groups, qualifying, mainTotal)
	f.WhatToExpect = mainSessionWhatToExpect(current, altTotal)
	// The confidence was softened if any group crosses a tokenizer family, so
	// the sentence explaining why must come from that group, not just the
	// first one.
	for _, g := range groups {
		if c := tokenizerCaveat(g.modelID, g.alt); c != "" {
			f.WhyItCosts += "\n\n" + c
			break
		}
	}
	f.Evidence = mainSessionEvidence(groups)
	return f, nil
}

func mainSessionTitle(groups []*mainGroup) string {
	seen := map[string]bool{}
	var names []string
	for _, g := range groups {
		d := modelDisplay(g.modelID)
		if !seen[d] {
			seen[d] = true
			names = append(names, d)
		}
	}
	return "Short look-ups ran on " + joinList(names)
}

func mainSessionWhatHappened(in Input, groups []*mainGroup, qualifying, maxTurns int, current float64) string {
	silent := 0
	for _, g := range groups {
		for _, h := range g.hits {
			if len(h.tools) == 0 {
				silent++
			}
		}
	}

	s := fmt.Sprintf("%s of your own session%s %s ran to %s turns or fewer and only read: they looked at "+
		"files and searched, and never edited anything or ran a command that changed the project. "+
		"These are the sessions you drove yourself, not the sub-agents they launched. Together they cost %s.",
		fmtInt(int64(qualifying)), plural(qualifying), windowPhrase(in),
		fmtInt(int64(maxTurns)), fmtUSD(current))

	if silent > 0 {
		s += fmt.Sprintf(" In %s of them the model called no tools at all: a question and an answer.",
			fmtInt(int64(silent)))
	}
	return s
}

func mainSessionWhyItCosts(in Input, top *mainGroup) string {
	name := modelDisplay(top.modelID)
	alt := modelDisplay(top.alt)
	ratio := outputRatio(in, top.modelID, top.alt, windowUntil(in))

	rate := "a good deal more than"
	if ratio > 0 {
		rate = fmt.Sprintf("about %sx the rate of", fmtOneDP(ratio))
	}
	return fmt.Sprintf("%s is billed at %s %s for the same tokens. A question that takes a few file reads "+
		"and a paragraph of answer is work the cheaper model does about as well, so on these sessions you "+
		"paid the premium without getting anything for it.", name, rate, alt)
}

// mainSessionWhatToChange writes the change to make. There is no file behind
// this finding, so the first lever is always the per-session switch, which
// leaves the saved default alone. Only when most of the window's main sessions
// look like this is it worth turning the default round instead.
//
// Mechanisms verified 2026-09-11 against code.claude.com/docs/en/model-config
// and code.claude.com/docs/en/settings-reference.
func mainSessionWhatToChange(groups []*mainGroup, qualifying, mainTotal int) string {
	var parts []string
	var claude *mainGroup
	// Two strong models moving to the same cheaper one (Opus 5 and Opus 4.8
	// both to Sonnet, say) get one paragraph, not one each.
	seenAlt := map[string]bool{}
	for _, g := range groups {
		if g.codex {
			parts = append(parts, fmt.Sprintf(
				"These ran under Codex, which has no switch to press part-way through: pick the smaller model "+
					"when you open a session that is only going to ask a question, and keep %s for the work "+
					"that earns it.", modelDisplay(g.modelID)))
			continue
		}
		if claude == nil {
			claude = g
		}
		if seenAlt[g.alt] {
			continue
		}
		seenAlt[g.alt] = true
		parts = append(parts, fmt.Sprintf(
			"This is a habit rather than a file, so there is nothing to edit. Start a quick look-up with "+
				"`%s %s`, then press s to keep that choice for this session only, which leaves your saved "+
				"default untouched.", cmdModel, shortModelName(g.alt)))
	}

	// More than half of the window's main sessions being look-ups makes the
	// default the thing that is wrong, rather than the individual session.
	if claude != nil && qualifying*2 > mainTotal {
		parts = append(parts, fmt.Sprintf(
			"More than half of the sessions in this window look like this, so it is worth turning it round: "+
				"set \"model\": %q in ~/.claude/settings.json to make %s the default, and use `%s` to switch "+
				"up when you start on something real.",
			shortModelName(claude.alt), modelDisplay(claude.alt), cmdModel))
	}
	return strings.TrimRight(strings.Join(parts, "\n\n"), "\n")
}

func mainSessionWhatToExpect(current, alt float64) string {
	share := 0.0
	if current > 0 {
		share = alt / current
	}
	return fmt.Sprintf("Sessions like these should cost about %s%% of what they do now. "+
		"If the answers start getting worse, switch back: there is nothing to undo.", fmtPct(share))
}

func mainSessionEvidence(groups []*mainGroup) Table {
	altHeader := "as the cheaper model"
	same := true
	for _, g := range groups[1:] {
		if g.alt != groups[0].alt {
			same = false
		}
	}
	if same {
		altHeader = "as " + shortModelName(groups[0].alt)
	}

	var hits []mainSessionHit
	for _, g := range groups {
		hits = append(hits, g.hits...)
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].saving() != hits[j].saving() {
			return hits[i].saving() > hits[j].saving()
		}
		return hits[i].id < hits[j].id
	})

	t := Table{Columns: []string{"session", "project", "turns", "tools", "cost", altHeader}}
	for _, h := range hits {
		tools := joinList(sortedKeys(h.tools))
		if tools == "" {
			tools = "none"
		}
		t.Rows = append(t.Rows, []string{
			shortID(h.id),
			h.project,
			fmtInt(int64(h.turns)),
			tools,
			fmtUSD(h.cost),
			fmtUSD(h.altCost),
		})
	}
	return t
}
