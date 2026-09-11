package findings

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/magna-nz/tallybook/internal/model"
	"github.com/magna-nz/tallybook/internal/store"
)

// So the invented-setting guard in settings_test.go scans this rule's prose
// too: enough qualifying runs for effortAgentsRule to produce a finding.
func init() {
	adviceFixtures = append(adviceFixtures, func(t *testing.T, st *store.Store) {
		effortReadOnlyFixture(t, st, "effadv", "researcher", "claude-opus-5", "high", 5, 10_000, nil)
	})
}

// effortAgentRun builds one read-only sub-agent run of three turns, each at
// the given effort level and carrying thinkingPerTurn thinking tokens. tools
// overrides the default read-only tool calls; nil keeps them read-only.
func effortAgentRun(parentID string, i int, modelID, effort string, thinkingPerTurn int64, tools []string) *model.Transcript {
	id := fmt.Sprintf("%s-child%d", parentID, i)
	agentID := fmt.Sprintf("%s-a%d", parentID, i)
	at := start.Add(time.Duration(i) * time.Hour)
	tr := &model.Transcript{Session: childSession(id, parentID, agentID, "/work/app", at)}

	if tools == nil {
		tools = []string{"Read", "Grep", "Glob"}
	}
	for j := 0; j < 3; j++ {
		turn := model.Turn{
			SessionID: id,
			ID:        fmt.Sprintf("%s-t%d", id, j),
			Timestamp: at.Add(time.Duration(j) * time.Minute),
			Model:     modelID,
			Effort:    effort,
			TextChars: 200,
			Usage:     usage(1_000, 5_000, 2_000, 3_000, thinkingPerTurn),
		}
		if j < len(tools) {
			turn.ToolCalls = []model.ToolCall{{
				ID: fmt.Sprintf("%s-tc%d", id, j), Name: tools[j], InputChars: 90,
			}}
		}
		tr.Turns = append(tr.Turns, turn)
	}
	return tr
}

// effortAgentRunMixed builds a run whose turns alternate between two effort
// levels, which the rule must treat as a run it cannot say anything about.
func effortAgentRunMixed(parentID string, i int, modelID, effortA, effortB string, thinkingPerTurn int64) *model.Transcript {
	tr := effortAgentRun(parentID, i, modelID, effortA, thinkingPerTurn, nil)
	tr.Turns[1].Effort = effortB
	return tr
}

// effortReadOnlyFixture is a parent plus n read-only sub-agent runs of one
// type, all at the given effort level.
func effortReadOnlyFixture(t *testing.T, st *store.Store, parentID, agentType, modelID, effort string, n int, thinkingPerTurn int64, override map[int]*model.Transcript) {
	t.Helper()
	trs := []*model.Transcript{parentWithLaunches(parentID, agentType, "", modelID, n)}
	for i := 0; i < n; i++ {
		if o, ok := override[i]; ok {
			trs = append(trs, o)
			continue
		}
		trs = append(trs, effortAgentRun(parentID, i, modelID, effort, thinkingPerTurn, nil))
	}
	ingest(t, st, trs...)
}

func TestEffortAgentsFires(t *testing.T) {
	st := newStore(t)
	effortReadOnlyFixture(t, st, "e1", "researcher", "claude-opus-5", "high", 5, 10_000, nil)

	in := input(t, st)
	in.Agents = agents(t, "researcher:")
	f, err := effortAgentsRule{}.Run(in)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if f == nil {
		t.Fatal("expected a finding for 5 read-only runs at high effort")
	}
	if f.Direction != Effort {
		t.Errorf("Direction = %q, want %q", f.Direction, Effort)
	}
	if f.Confidence != Low {
		t.Errorf("Confidence = %q, want %q", f.Confidence, Low)
	}
	if f.SavingUSD <= 0 {
		t.Errorf("SavingUSD = %v, want > 0", f.SavingUSD)
	}
	if !strings.Contains(f.Patch, "researcher.md") {
		t.Errorf("Patch = %q, want the agent file named", f.Patch)
	}
	if !strings.Contains(f.Patch, "+effort: medium") {
		t.Errorf("Patch = %q, want the effort line", f.Patch)
	}
	if !strings.Contains(f.WhatToChange, "effort: medium") {
		t.Errorf("WhatToChange = %q, want the exact line to add", f.WhatToChange)
	}
	if !strings.Contains(f.WhatToChange, "Codex has no per-agent effort") {
		t.Errorf("WhatToChange = %q, want the Codex sentence", f.WhatToChange)
	}
	if got := len(f.Evidence.Rows); got != 1 {
		t.Fatalf("evidence rows = %d, want 1", got)
	}
	if f.Evidence.Rows[0][1] != "5" {
		t.Errorf("evidence runs = %q, want 5", f.Evidence.Rows[0][1])
	}
	if f.Evidence.Rows[0][2] != "high" {
		t.Errorf("evidence effort = %q, want high", f.Evidence.Rows[0][2])
	}
	assertPlainEnglish(t, *f)
	t.Logf("WhatHappened: %s", f.WhatHappened)
	t.Logf("WhyItCosts: %s", f.WhyItCosts)
	t.Logf("WhatToChange: %s", f.WhatToChange)
	t.Logf("WhatToExpect: %s", f.WhatToExpect)
}

func TestEffortAgentsBelowRunFloorIsSilent(t *testing.T) {
	st := newStore(t)
	effortReadOnlyFixture(t, st, "e1", "researcher", "claude-opus-5", "high", 2, 10_000, nil)

	in := input(t, st)
	in.Agents = agents(t, "researcher:")
	f, err := effortAgentsRule{}.Run(in)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if f != nil {
		t.Fatalf("expected no finding below the run floor of three, got %q", f.Title)
	}
}

func TestEffortAgentsBelowSavingFloorIsSilent(t *testing.T) {
	st := newStore(t)
	effortReadOnlyFixture(t, st, "e1", "researcher", "claude-opus-5", "high", 3, 10, nil)

	f, err := effortAgentsRule{}.Run(input(t, st))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if f != nil {
		t.Fatalf("expected no finding below the one-dollar saving floor, got %q", f.Title)
	}
}

func TestEffortAgentsSkipsMixedEffortRuns(t *testing.T) {
	st := newStore(t)
	override := map[int]*model.Transcript{}
	for i := 0; i < 5; i++ {
		override[i] = effortAgentRunMixed("e1", i, "claude-opus-5", "high", "medium", 10_000)
	}
	effortReadOnlyFixture(t, st, "e1", "researcher", "claude-opus-5", "high", 5, 10_000, override)

	f, err := effortAgentsRule{}.Run(input(t, st))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if f != nil {
		t.Fatalf("expected no finding: every run has a mix of effort levels, got %q", f.Title)
	}
}

func TestEffortAgentsSkipsRunsWithNoEffortRecorded(t *testing.T) {
	st := newStore(t)
	effortReadOnlyFixture(t, st, "e1", "researcher", "claude-opus-5", "", 5, 10_000, nil)

	f, err := effortAgentsRule{}.Run(input(t, st))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if f != nil {
		t.Fatalf("expected no finding: no run recorded an effort level, got %q", f.Title)
	}
}

func TestEffortAgentsSkipsRunsThatWrote(t *testing.T) {
	st := newStore(t)
	override := map[int]*model.Transcript{
		2: effortAgentRun("e1", 2, "claude-opus-5", "high", 10_000, []string{"Read", "Edit"}),
	}
	// Only 4 of the 5 runs are read-only; below the floor of 5... use exactly
	// the floor of 3 so the one writing run would have kept it at 3 read-only
	// runs, but with it excluded there are only 2 left.
	effortReadOnlyFixture(t, st, "e1", "researcher", "claude-opus-5", "high", 3, 10_000, override)

	f, err := effortAgentsRule{}.Run(input(t, st))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if f != nil {
		t.Fatalf("expected no finding: the run that wrote should drop the group below the floor, got %q", f.Title)
	}
}

// The saving is estimated, but it must still match a hand calculation over a
// tiny fixture: claude-opus-5 output is 25 per million tokens, so 10,000
// thinking tokens on a turn cost 0.25. Three runs of three turns each cost
// 2.25 in thinking, halved to a 1.125 saving.
func TestEffortAgentsSavingMatchesHandCalculation(t *testing.T) {
	st := newStore(t)
	effortReadOnlyFixture(t, st, "e1", "researcher", "claude-opus-5", "high", 3, 10_000, nil)

	f, err := effortAgentsRule{}.Run(input(t, st))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if f == nil {
		t.Fatal("expected a finding")
	}
	const wantSaving = 1.125
	if f.SavingUSD != wantSaving {
		t.Errorf("SavingUSD = %v, want %v", f.SavingUSD, wantSaving)
	}
	row := f.Evidence.Rows[0]
	if row[3] != "90,000" {
		t.Errorf("thinking tokens = %q, want 90,000", row[3])
	}
	if row[4] != fmtUSD(2.25) {
		t.Errorf("thinking cost = %q, want %q", row[4], fmtUSD(2.25))
	}
	if row[5] != fmtUSD(wantSaving) {
		t.Errorf("saving = %q, want %q", row[5], fmtUSD(wantSaving))
	}
}

func TestEffortAgentsAdviceWithAgentFile(t *testing.T) {
	st := newStore(t)
	effortReadOnlyFixture(t, st, "e1", "researcher", "claude-opus-5", "high", 5, 10_000, nil)

	in := input(t, st)
	in.Agents = agents(t, "researcher:")
	f, err := effortAgentsRule{}.Run(in)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if f == nil {
		t.Fatal("expected a finding")
	}
	if !strings.Contains(f.WhatToChange, "researcher.md") {
		t.Errorf("WhatToChange = %q, want the agent file named", f.WhatToChange)
	}
	if !strings.Contains(f.Patch, "researcher.md") || !strings.Contains(f.Patch, "+effort: medium") {
		t.Errorf("Patch = %q, want a frontmatter patch adding effort: medium", f.Patch)
	}
	assertPlainEnglish(t, *f)
}

func TestEffortAgentsAdviceWithoutAgentFile(t *testing.T) {
	st := newStore(t)
	effortReadOnlyFixture(t, st, "e1", "researcher", "claude-opus-5", "high", 5, 10_000, nil)

	in := input(t, st)
	in.Agents = agents(t) // no files at all
	f, err := effortAgentsRule{}.Run(in)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if f == nil {
		t.Fatal("expected a finding")
	}
	if !strings.Contains(f.WhatToChange, "does not exist") {
		t.Errorf("WhatToChange = %q, want it to say the file does not exist", f.WhatToChange)
	}
	if !strings.Contains(f.WhatToChange, "researcher.md") {
		t.Errorf("WhatToChange = %q, want the missing file path named", f.WhatToChange)
	}
	if f.Patch != "" {
		t.Errorf("Patch = %q, want no patch when there is no file to change", f.Patch)
	}
	assertPlainEnglish(t, *f)
}

// A run that never used a tool at all still counts as read-only.
func TestEffortAgentsCountsToollessRunsAsReadOnly(t *testing.T) {
	st := newStore(t)
	override := map[int]*model.Transcript{}
	for i := 0; i < 5; i++ {
		override[i] = effortAgentRun("e1", i, "claude-opus-5", "high", 10_000, []string{})
	}
	effortReadOnlyFixture(t, st, "e1", "researcher", "claude-opus-5", "high", 5, 10_000, override)

	f, err := effortAgentsRule{}.Run(input(t, st))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if f == nil {
		t.Fatal("expected a finding: no tool calls at all is still read-only")
	}
}
