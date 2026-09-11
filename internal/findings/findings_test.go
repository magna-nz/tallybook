package findings

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/magna-nz/tallybook/internal/config"
	"github.com/magna-nz/tallybook/internal/model"
	"github.com/magna-nz/tallybook/internal/pricing"
	"github.com/magna-nz/tallybook/internal/store"
)

// now is the fixed clock every test runs against, so prices and window text
// never depend on the day the suite runs.
var now = time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)

// start is when the fixture sessions begin, comfortably inside a 30-day
// window ending at now.
var start = time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)

func newStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "tallybook.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func ingest(t *testing.T, st *store.Store, trs ...*model.Transcript) {
	t.Helper()
	for _, tr := range trs {
		if err := st.ReplaceTranscript(tr, 1024, now); err != nil {
			t.Fatalf("ingest %s: %v", tr.Session.ID, err)
		}
	}
}

// input builds the Input every rule takes, with the window total priced the
// same way the rules price everything.
func input(t *testing.T, st *store.Store) Input {
	t.Helper()
	cfg := config.Default().Findings
	cfg.MinRuns = 5 // the tests below build exactly five runs and expect one bad run to break the floor
	in := Input{
		Store:      st,
		Prices:     pricing.Default(),
		Cfg:        cfg,
		Plan:       config.PlanAPI,
		Filter:     store.Filter{},
		Now:        now,
		WindowDays: 30,
	}
	rows, err := st.Sessions(in.Filter)
	if err != nil {
		t.Fatalf("sessions: %v", err)
	}
	for _, row := range rows {
		turns, err := st.Turns(row.ID)
		if err != nil {
			t.Fatalf("turns: %v", err)
		}
		in.TotalUSD += costOf(in, turns)
	}
	return in
}

// usage is a compact literal for the token counts a turn reports.
func usage(input, cacheRead, write5m, output, thinking int64) model.Usage {
	return model.Usage{
		Input: input, CacheRead: cacheRead, CacheWrite5m: write5m,
		Output: output, Thinking: thinking,
	}
}

func session(id, project string, at time.Time) model.Session {
	return model.Session{
		ID: id, Source: model.SourceClaudeCode,
		Path:      "/transcripts/" + id + ".jsonl",
		Project:   project,
		StartedAt: at, EndedAt: at.Add(time.Hour),
		CLIVersion: "2.0.0",
	}
}

func childSession(id, parentID, agentID, project string, at time.Time) model.Session {
	s := session(id, project, at)
	s.ParentSessionID = parentID
	s.AgentID = agentID
	return s
}

// parentWithLaunches builds a main session that launched n sub-agents of one
// type. The child ids it expects are "<id>-child<i>" with agent id "<id>-a<i>".
func parentWithLaunches(id, subagentType, requested, resolved string, n int) *model.Transcript {
	tr := &model.Transcript{Session: session(id, "/work/app", start)}
	for i := 0; i < n; i++ {
		callID := fmt.Sprintf("%s-tc%d", id, i)
		tr.Turns = append(tr.Turns, model.Turn{
			SessionID: id,
			ID:        fmt.Sprintf("%s-t%d", id, i),
			Timestamp: start.Add(time.Duration(i) * time.Hour),
			Model:     "claude-opus-5",
			TextChars: 900,
			Usage:     usage(500, 10_000, 2_000, 800, 0),
			ToolCalls: []model.ToolCall{{
				ID: callID, Name: "Agent", InputChars: 400,
				Agent: &model.AgentLaunch{
					SubagentType:   subagentType,
					RequestedModel: requested,
					ResolvedModel:  resolved,
					AgentID:        fmt.Sprintf("%s-a%d", id, i),
					Description:    "look something up",
				},
			}},
		})
	}
	return tr
}

// agentRun builds one sub-agent transcript that used the named tools.
func agentRun(parentID string, i int, modelID string, tools []string) *model.Transcript {
	id := fmt.Sprintf("%s-child%d", parentID, i)
	agentID := fmt.Sprintf("%s-a%d", parentID, i)
	at := start.Add(time.Duration(i) * time.Hour)
	tr := &model.Transcript{Session: childSession(id, parentID, agentID, "/work/app", at)}

	for j := 0; j < 3; j++ {
		turn := model.Turn{
			SessionID: id,
			ID:        fmt.Sprintf("%s-t%d", id, j),
			Timestamp: at.Add(time.Duration(j) * time.Minute),
			Model:     modelID,
			TextChars: 700,
			Usage:     usage(1_000, 20_000, 5_000, 2_000, 0),
		}
		if j < len(tools) {
			turn.ToolCalls = []model.ToolCall{{
				ID: fmt.Sprintf("%s-tc%d", id, j), Name: tools[j], InputChars: 120,
			}}
		}
		tr.Turns = append(tr.Turns, turn)
	}
	return tr
}

// readOnlyFixture is a parent plus n read-only sub-agent runs of one type.
func readOnlyFixture(t *testing.T, st *store.Store, parentID, agentType, modelID string, n int, override map[int][]string) {
	t.Helper()
	trs := []*model.Transcript{parentWithLaunches(parentID, agentType, "", modelID, n)}
	for i := 0; i < n; i++ {
		tools := []string{"Read", "Grep", "Glob"}
		if o, ok := override[i]; ok {
			tools = o
		}
		trs = append(trs, agentRun(parentID, i, modelID, tools))
	}
	ingest(t, st, trs...)
}

// --- rule 1: read-only sub-agents on a strong model -------------------------

func TestReadOnlyAgentFires(t *testing.T) {
	st := newStore(t)
	readOnlyFixture(t, st, "p1", "researcher", "claude-opus-5", 5, nil)

	f, err := readOnlyAgentRule{}.Run(input(t, st))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if f == nil {
		t.Fatal("expected a finding for 5 read-only opus runs")
	}
	if f.SavingUSD <= 0 {
		t.Errorf("SavingUSD = %v, want > 0", f.SavingUSD)
	}
	if f.SavingShare <= 0 || f.SavingShare > 1 {
		t.Errorf("SavingShare = %v, want a fraction of the window total", f.SavingShare)
	}
	if f.Direction != Downgrade {
		t.Errorf("Direction = %q, want %q", f.Direction, Downgrade)
	}
	if f.Confidence != Medium {
		t.Errorf("Confidence = %q, want %q for 5 runs", f.Confidence, Medium)
	}
	if !strings.Contains(f.Title, "Opus") {
		t.Errorf("Title = %q, want it to name Opus", f.Title)
	}
	if !strings.Contains(f.Patch, ".claude/agents/researcher.md") {
		t.Errorf("Patch = %q, want it to name the agent file", f.Patch)
	}
	if !strings.Contains(f.Patch, "+model: sonnet") {
		t.Errorf("Patch = %q, want it to add the sonnet line", f.Patch)
	}
	if !strings.Contains(f.WhatToChange, ".claude/agents/researcher.md") {
		t.Errorf("WhatToChange = %q, want the exact file named", f.WhatToChange)
	}
	if !strings.Contains(f.WhatToChange, "model: sonnet") {
		t.Errorf("WhatToChange = %q, want the exact line to add", f.WhatToChange)
	}
	if got := len(f.Evidence.Rows); got != 1 {
		t.Fatalf("evidence rows = %d, want 1", got)
	}
	if f.Evidence.Rows[0][1] != "5" {
		t.Errorf("evidence runs = %q, want 5", f.Evidence.Rows[0][1])
	}
	assertPlainEnglish(t, *f)
}

func TestReadOnlyAgentHighConfidenceOnTenRuns(t *testing.T) {
	st := newStore(t)
	readOnlyFixture(t, st, "p1", "researcher", "claude-opus-5", 10, nil)

	f, err := readOnlyAgentRule{}.Run(input(t, st))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if f == nil {
		t.Fatal("expected a finding")
	}
	if f.Confidence != High {
		t.Errorf("Confidence = %q, want %q for 10 runs", f.Confidence, High)
	}
}

func TestReadOnlyAgentSkipsRunsThatEdited(t *testing.T) {
	st := newStore(t)
	// One of the five runs edited a file, so only four were read-only and the
	// evidence floor of five is not met.
	readOnlyFixture(t, st, "p1", "researcher", "claude-opus-5", 5, map[int][]string{
		2: {"Read", "Edit"},
	})

	f, err := readOnlyAgentRule{}.Run(input(t, st))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if f != nil {
		t.Fatalf("expected no finding, got %q", f.Title)
	}
}

func TestReadOnlyAgentSkipsBash(t *testing.T) {
	st := newStore(t)
	// Bash is opaque: the command is not stored, so it cannot be called read-only.
	readOnlyFixture(t, st, "p1", "researcher", "claude-opus-5", 5, map[int][]string{
		0: {"Read", "Bash"},
	})

	f, err := readOnlyAgentRule{}.Run(input(t, st))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if f != nil {
		t.Fatalf("expected no finding, got %q", f.Title)
	}
}

func TestReadOnlyAgentSkipsAlreadyCheapModel(t *testing.T) {
	st := newStore(t)
	readOnlyFixture(t, st, "p1", "researcher", "claude-sonnet-5", 5, nil)

	f, err := readOnlyAgentRule{}.Run(input(t, st))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if f != nil {
		t.Fatalf("expected no finding on sonnet, got %q", f.Title)
	}
}

// --- rule 2: requested model not honoured -----------------------------------

func TestRequestedModelFires(t *testing.T) {
	st := newStore(t)
	trs := []*model.Transcript{parentWithLaunches("p2", "researcher", "sonnet", "claude-sonnet-5", 3)}
	for i := 0; i < 3; i++ {
		trs = append(trs, agentRun("p2", i, "claude-opus-5", []string{"Read", "Grep", "Glob"}))
	}
	ingest(t, st, trs...)

	f, err := requestedModelRule{}.Run(input(t, st))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if f == nil {
		t.Fatal("expected a finding when sonnet was asked for and opus ran")
	}
	if f.Direction != Config {
		t.Errorf("Direction = %q, want %q", f.Direction, Config)
	}
	if f.Confidence != High {
		t.Errorf("Confidence = %q, want %q", f.Confidence, High)
	}
	if f.SavingUSD <= 0 {
		t.Errorf("SavingUSD = %v, want > 0", f.SavingUSD)
	}
	if !strings.Contains(f.Title, "Opus") {
		t.Errorf("Title = %q, want it to name what actually ran", f.Title)
	}
	if !strings.Contains(f.WhatToChange, ".claude/agents/researcher.md") {
		t.Errorf("WhatToChange = %q, want the agent file named", f.WhatToChange)
	}
	if !strings.Contains(f.WhatToChange, "wins over the model") {
		t.Errorf("WhatToChange = %q, want the mechanism explained first", f.WhatToChange)
	}
	if got := len(f.Evidence.Rows); got != 3 {
		t.Fatalf("evidence rows = %d, want 3", got)
	}
	if f.Evidence.Rows[0][0] != "p2" {
		t.Errorf("evidence session = %q, want the short parent id", f.Evidence.Rows[0][0])
	}
	assertPlainEnglish(t, *f)
}

func TestRequestedModelAliasCountsAsHonoured(t *testing.T) {
	st := newStore(t)
	// "sonnet" and "claude-sonnet-5" are the same model; asking for one and
	// getting the other is not a mismatch.
	trs := []*model.Transcript{parentWithLaunches("p2", "researcher", "sonnet", "claude-sonnet-5", 3)}
	for i := 0; i < 3; i++ {
		trs = append(trs, agentRun("p2", i, "claude-sonnet-5", []string{"Read", "Grep", "Glob"}))
	}
	ingest(t, st, trs...)

	f, err := requestedModelRule{}.Run(input(t, st))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if f != nil {
		t.Fatalf("expected no finding, got %q", f.Title)
	}
}

func TestRequestedModelCheaperThanAskedSavesNothing(t *testing.T) {
	st := newStore(t)
	trs := []*model.Transcript{parentWithLaunches("p2", "researcher", "opus", "claude-opus-5", 3)}
	for i := 0; i < 3; i++ {
		trs = append(trs, agentRun("p2", i, "claude-sonnet-5", []string{"Read", "Grep", "Glob"}))
	}
	ingest(t, st, trs...)

	f, err := requestedModelRule{}.Run(input(t, st))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if f == nil {
		t.Fatal("expected a finding: the requested model still did not run")
	}
	if f.SavingUSD != 0 {
		t.Errorf("SavingUSD = %v, want 0 when the mismatch ran cheaper", f.SavingUSD)
	}
	if !strings.Contains(f.WhyItCosts, "Nothing was overspent") {
		t.Errorf("WhyItCosts = %q, want it to say it ran cheaper than asked", f.WhyItCosts)
	}
	assertPlainEnglish(t, *f)
}

// --- rule 3: cache rebuilt mid-session --------------------------------------

// pausedSession builds a session whose turns re-send the whole conversation
// after a pause of gap.
func pausedSession(id string, gap time.Duration) *model.Transcript {
	tr := &model.Transcript{Session: session(id, "/work/app", start)}
	at := start
	for i := 0; i < 4; i++ {
		tr.Turns = append(tr.Turns, model.Turn{
			SessionID: id,
			ID:        fmt.Sprintf("%s-t%d", id, i),
			Timestamp: at,
			Model:     "claude-opus-5",
			TextChars: 800,
			Usage:     usage(1_000, 0, 60_000, 1_500, 0),
		})
		at = at.Add(gap)
	}
	return tr
}

func TestCacheRebuildFires(t *testing.T) {
	st := newStore(t)
	ingest(t, st, pausedSession("c1", 10*time.Minute))

	f, err := cacheRebuildRule{}.Run(input(t, st))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if f == nil {
		t.Fatal("expected a finding for three rebuilds ten minutes apart")
	}
	if f.Direction != Cache {
		t.Errorf("Direction = %q, want %q", f.Direction, Cache)
	}
	if f.Confidence != Medium {
		t.Errorf("Confidence = %q, want %q", f.Confidence, Medium)
	}
	if f.SavingUSD <= 0 {
		t.Errorf("SavingUSD = %v, want > 0", f.SavingUSD)
	}
	if !strings.Contains(f.WhatToChange, "~/.claude/settings.json") {
		t.Errorf("WhatToChange = %q, want the exact file named", f.WhatToChange)
	}
	if !strings.Contains(f.WhatToChange, "Check the setting name") {
		t.Errorf("WhatToChange = %q, want the caveat about the setting name", f.WhatToChange)
	}
	if got := len(f.Evidence.Rows); got != 1 {
		t.Fatalf("evidence rows = %d, want 1", got)
	}
	if f.Evidence.Rows[0][2] != "3" {
		t.Errorf("rebuilds = %q, want 3", f.Evidence.Rows[0][2])
	}
	assertPlainEnglish(t, *f)
}

func TestCacheRebuildIgnoresShortPauses(t *testing.T) {
	st := newStore(t)
	ingest(t, st, pausedSession("c1", time.Minute))

	f, err := cacheRebuildRule{}.Run(input(t, st))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if f != nil {
		t.Fatalf("expected no finding for one-minute pauses, got %q", f.Title)
	}
}

// --- rule 4: tool output bloat ----------------------------------------------

func bloatedFixture(id string) *model.Transcript {
	tr := &model.Transcript{Session: session(id, "/work/app", start)}
	for i := 0; i < 10; i++ {
		tr.Turns = append(tr.Turns, model.Turn{
			SessionID: id,
			ID:        fmt.Sprintf("%s-t%d", id, i),
			Timestamp: start.Add(time.Duration(i) * time.Minute),
			Model:     "claude-opus-5",
			TextChars: 300,
			Usage:     usage(0, 40_000, 0, 1_200, 0),
			ToolCalls: []model.ToolCall{{
				ID: fmt.Sprintf("%s-tc%d", id, i), Name: "Bash", InputChars: 60,
			}},
		})
	}
	chars := []int{140_000, 30_000, 30_000, 30_000}
	for i, c := range chars {
		tr.ToolResults = append(tr.ToolResults, model.ToolResult{
			SessionID:  id,
			ToolCallID: fmt.Sprintf("%s-tc%d", id, i),
			Timestamp:  start.Add(time.Duration(i) * time.Minute),
			Chars:      c,
		})
	}
	return tr
}

func TestToolOutputBloatFires(t *testing.T) {
	st := newStore(t)
	ingest(t, st, bloatedFixture("b1"))

	f, err := toolOutputRule{}.Run(input(t, st))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if f == nil {
		t.Fatal("expected a finding for a session that is mostly tool output")
	}
	if f.Direction != Context {
		t.Errorf("Direction = %q, want %q", f.Direction, Context)
	}
	if f.SavingUSD <= 0 {
		t.Errorf("SavingUSD = %v, want > 0", f.SavingUSD)
	}
	if !strings.Contains(f.WhatToChange, "CLAUDE.md") {
		t.Errorf("WhatToChange = %q, want CLAUDE.md named", f.WhatToChange)
	}
	if !strings.Contains(f.WhatToChange, "tail -50") {
		t.Errorf("WhatToChange = %q, want the exact command", f.WhatToChange)
	}
	if got := len(f.Evidence.Rows); got != 1 {
		t.Fatalf("evidence rows = %d, want 1", got)
	}
	if f.Evidence.Rows[0][4] != "35,000" {
		t.Errorf("largest result = %q, want 35,000 tokens", f.Evidence.Rows[0][4])
	}
	assertPlainEnglish(t, *f)
}

func TestToolOutputBloatIgnoresSmallSessions(t *testing.T) {
	st := newStore(t)
	tr := bloatedFixture("b1")
	// Same shape, but the session never grew past the context floor.
	for i := range tr.Turns {
		tr.Turns[i].Usage = usage(0, 2_000, 0, 1_200, 0)
	}
	ingest(t, st, tr)

	f, err := toolOutputRule{}.Run(input(t, st))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if f != nil {
		t.Fatalf("expected no finding below the context floor, got %q", f.Title)
	}
}

// --- rule 5: retry loops -----------------------------------------------------

func retryFixture(id string) *model.Transcript {
	tr := &model.Transcript{Session: session(id, "/work/app", start)}
	for i := 0; i < 6; i++ {
		callID := fmt.Sprintf("%s-tc%d", id, i)
		tr.Turns = append(tr.Turns, model.Turn{
			SessionID: id,
			ID:        fmt.Sprintf("%s-t%d", id, i),
			Timestamp: start.Add(time.Duration(i) * time.Minute),
			Model:     "claude-opus-5",
			TextChars: 400,
			Usage:     usage(500, 5_000, 1_000, 900, 0),
			ToolCalls: []model.ToolCall{{ID: callID, Name: "Bash", InputChars: 80}},
		})
		tr.ToolResults = append(tr.ToolResults, model.ToolResult{
			SessionID:  id,
			ToolCallID: callID,
			Timestamp:  start.Add(time.Duration(i) * time.Minute).Add(time.Second),
			Chars:      500,
			IsError:    i < 4, // four failures in a row, then it worked
		})
	}
	return tr
}

func TestRetryLoopFires(t *testing.T) {
	st := newStore(t)
	ingest(t, st, retryFixture("r1"))

	f, err := retryLoopRule{}.Run(input(t, st))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if f == nil {
		t.Fatal("expected a finding for four consecutive Bash errors")
	}
	if f.Confidence != Info {
		t.Errorf("Confidence = %q, want %q", f.Confidence, Info)
	}
	if f.Direction != Upgrade {
		t.Errorf("Direction = %q, want %q", f.Direction, Upgrade)
	}
	if f.SavingUSD != 0 || f.SavingShare != 0 {
		t.Errorf("saving = %v/%v, want zero: this finding never claims one", f.SavingUSD, f.SavingShare)
	}
	if !strings.Contains(f.WhyItCosts, "cheaper model would make it worse") {
		t.Errorf("WhyItCosts = %q, want the do-not-downgrade warning", f.WhyItCosts)
	}
	if got := len(f.Evidence.Rows); got != 1 {
		t.Fatalf("evidence rows = %d, want 1", got)
	}
	row := f.Evidence.Rows[0]
	if row[3] != "Bash" || row[4] != "4" || row[5] != "6" {
		t.Errorf("evidence row = %v, want Bash 4 errors of 6 calls", row)
	}
	assertPlainEnglish(t, *f)
}

func TestRetryLoopIgnoresCleanSessions(t *testing.T) {
	st := newStore(t)
	tr := retryFixture("r1")
	for i := range tr.ToolResults {
		tr.ToolResults[i].IsError = false
	}
	ingest(t, st, tr)

	f, err := retryLoopRule{}.Run(input(t, st))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if f != nil {
		t.Fatalf("expected no finding without errors, got %q", f.Title)
	}
}

// --- rule 6: thinking on relay turns -----------------------------------------

// relayFixture is a reviewer sub-agent whose turns all just hand off to the
// next tool, having thought first.
func relayFixture(t *testing.T, st *store.Store, relayTurns int) {
	t.Helper()
	parent := parentWithLaunches("p6", "reviewer", "", "claude-opus-5", 1)

	id, agentID := "p6-child0", "p6-a0"
	child := &model.Transcript{Session: childSession(id, "p6", agentID, "/work/app", start)}
	for i := 0; i < relayTurns; i++ {
		child.Turns = append(child.Turns, model.Turn{
			SessionID: id,
			ID:        fmt.Sprintf("%s-t%d", id, i),
			Timestamp: start.Add(time.Duration(i) * time.Minute),
			Model:     "claude-opus-5",
			Effort:    "high",
			TextChars: 40,
			Usage:     usage(200, 5_000, 0, 6_000, 5_000),
			ToolCalls: []model.ToolCall{{
				ID: fmt.Sprintf("%s-tc%d", id, i), Name: "Read", InputChars: 90,
			}},
		})
	}
	ingest(t, st, parent, child)
}

func TestThinkingRelayFires(t *testing.T) {
	st := newStore(t)
	relayFixture(t, st, 25)

	f, err := thinkingRelayRule{}.Run(input(t, st))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if f == nil {
		t.Fatal("expected a finding for 25 relay turns with thinking")
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
	if !strings.Contains(f.Patch, ".claude/agents/reviewer.md") {
		t.Errorf("Patch = %q, want the reviewer file", f.Patch)
	}
	if !strings.Contains(f.Patch, "+effort: low") {
		t.Errorf("Patch = %q, want the effort line", f.Patch)
	}
	if !strings.Contains(f.WhatToChange, "effort: low") {
		t.Errorf("WhatToChange = %q, want the exact line to add", f.WhatToChange)
	}
	if !strings.Contains(f.WhatToChange, "Leave the main session") {
		t.Errorf("WhatToChange = %q, want the do-not-touch note", f.WhatToChange)
	}
	if got := len(f.Evidence.Rows); got != 1 {
		t.Fatalf("evidence rows = %d, want 1", got)
	}
	if f.Evidence.Rows[0][1] != "25" {
		t.Errorf("relay turns = %q, want 25", f.Evidence.Rows[0][1])
	}
	assertPlainEnglish(t, *f)
}

func TestThinkingRelayBelowThreshold(t *testing.T) {
	st := newStore(t)
	relayFixture(t, st, 19)

	f, err := thinkingRelayRule{}.Run(input(t, st))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if f != nil {
		t.Fatalf("expected no finding below 20 relay turns, got %q", f.Title)
	}
}

// --- Run: ordering and the disabled list --------------------------------------

func TestRunSortsSavingsBeforeInfoAndHonoursDisabled(t *testing.T) {
	st := newStore(t)
	readOnlyFixture(t, st, "p1", "researcher", "claude-opus-5", 5, nil)
	ingest(t, st, retryFixture("r1"))

	in := input(t, st)
	found, err := Run(in)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(found) < 2 {
		t.Fatalf("findings = %d, want at least the downgrade and the retry pointer", len(found))
	}
	sawInfo := false
	for _, f := range found {
		if f.Confidence == Info {
			sawInfo = true
			continue
		}
		if sawInfo {
			t.Errorf("finding %q with a saving came after an Info finding", f.ID)
		}
	}
	if !sawInfo {
		t.Error("expected the retry-loops pointer among the findings")
	}
	for i := 1; i < len(found); i++ {
		if found[i-1].Confidence != Info && found[i].Confidence != Info &&
			found[i-1].SavingUSD < found[i].SavingUSD {
			t.Errorf("savings out of order: %v before %v", found[i-1].SavingUSD, found[i].SavingUSD)
		}
	}

	in.Cfg.Disabled = []string{"readonly-agent-on-strong-model", "retry-loops"}
	found, err = Run(in)
	if err != nil {
		t.Fatalf("run disabled: %v", err)
	}
	for _, f := range found {
		if f.ID == "readonly-agent-on-strong-model" || f.ID == "retry-loops" {
			t.Errorf("disabled rule %q still ran", f.ID)
		}
	}
}

func TestRunSetsFindingIDs(t *testing.T) {
	st := newStore(t)
	readOnlyFixture(t, st, "p1", "researcher", "claude-opus-5", 5, nil)

	found, err := Run(input(t, st))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(found) == 0 {
		t.Fatal("expected at least one finding")
	}
	for _, f := range found {
		if f.ID == "" {
			t.Errorf("finding %q has no id", f.Title)
		}
		assertPlainEnglish(t, f)
	}
}

// --- text quality -------------------------------------------------------------

// jargon is the vocabulary a report must not use without glossing it. The
// check runs over prose only: an indented snippet is something to paste, and
// a setting name is what it is (CLAUDE_CODE_CACHE_TTL contains one of these
// words and cannot be renamed).
var jargon = []string{"TTL", "ctx", "MTok", "ephemeral"}

// prose strips the indented snippet lines a finding tells the user to paste.
func prose(s string) string {
	var kept []string
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(line, "    ") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

func assertPlainEnglish(t *testing.T, f Finding) {
	t.Helper()
	fields := map[string]string{
		"WhatHappened": f.WhatHappened,
		"WhyItCosts":   f.WhyItCosts,
		"WhatToChange": f.WhatToChange,
		"WhatToExpect": f.WhatToExpect,
	}
	if strings.TrimSpace(f.Title) == "" {
		t.Errorf("%s: empty Title", f.ID)
	}
	if strings.HasSuffix(f.Title, ".") {
		t.Errorf("%s: Title %q ends in a period", f.ID, f.Title)
	}
	for name, text := range fields {
		if strings.TrimSpace(text) == "" {
			t.Errorf("%s: %s is empty", f.ID, name)
			continue
		}
		body := prose(text)
		for _, word := range jargon {
			if strings.Contains(strings.ToLower(body), strings.ToLower(word)) {
				t.Errorf("%s: %s uses unglossed jargon %q: %s", f.ID, name, word, text)
			}
		}
	}
	for _, row := range f.Evidence.Rows {
		if len(row) != len(f.Evidence.Columns) {
			t.Errorf("%s: evidence row %v has %d cells, want %d", f.ID, row, len(row), len(f.Evidence.Columns))
		}
	}
}

func TestHelpersFormatMoneyAndCounts(t *testing.T) {
	if got := fmtUSD(1234.5); got != "$1,234.50" {
		t.Errorf("fmtUSD(1234.5) = %q", got)
	}
	if got := fmtUSD(0.4); got != "$0.40" {
		t.Errorf("fmtUSD(0.4) = %q", got)
	}
	if got := fmtUSD(-12); got != "-$12.00" {
		t.Errorf("fmtUSD(-12) = %q", got)
	}
	if got := fmtInt(30000); got != "30,000" {
		t.Errorf("fmtInt(30000) = %q", got)
	}
	if got := fmtInt(999); got != "999" {
		t.Errorf("fmtInt(999) = %q", got)
	}
	if got := fmtInt(1_000_000); got != "1,000,000" {
		t.Errorf("fmtInt(1000000) = %q", got)
	}
}

func TestSameModelUsesCanonicalIDs(t *testing.T) {
	in := Input{Prices: pricing.Default()}
	if !sameModel(in, "sonnet", "claude-sonnet-5") {
		t.Error("sonnet and claude-sonnet-5 should be the same model")
	}
	if !sameModel(in, "claude-opus-5-20260401", "opus") {
		t.Error("a dated opus id should match the opus alias")
	}
	if sameModel(in, "sonnet", "claude-opus-5") {
		t.Error("sonnet and opus are not the same model")
	}
	if !sameModel(in, "some-unknown-model", "SOME-UNKNOWN-MODEL") {
		t.Error("unknown ids should fall back to a case-insensitive compare")
	}
}

func TestAgentFileKnowsBuiltins(t *testing.T) {
	if got := agentFile("researcher"); got != ".claude/agents/researcher.md" {
		t.Errorf("agentFile(researcher) = %q", got)
	}
	for _, builtin := range []string{"Explore", "Plan", "general-purpose", "claude", "unnamed", ""} {
		if got := agentFile(builtin); got != "" {
			t.Errorf("agentFile(%q) = %q, want no file", builtin, got)
		}
	}
}

func TestReadOnlyToolClassification(t *testing.T) {
	for _, name := range []string{"Read", "Grep", "Glob", "LS", "WebFetch", "mcp__notion__search_pages"} {
		if !isReadOnlyTool(name) {
			t.Errorf("%s should be read-only", name)
		}
	}
	for _, name := range []string{"Edit", "Write", "MultiEdit", "Bash", "apply_patch", "mcp__notion__create_page"} {
		if isReadOnlyTool(name) {
			t.Errorf("%s should not be read-only", name)
		}
	}
	if !allReadOnly(map[string]int{}) {
		t.Error("a run with no tool calls changed nothing")
	}
}

// Opus 4.6 counts tokens the older way; the Sonnet 5 it would move to counts
// them the newer way and needs roughly 30% more of them for the same text.
// Pricing the recorded counts at Sonnet rates therefore flatters Sonnet, so
// the finding must say the saving is likely smaller than shown.
func TestReadOnlyAgentFlagsTokenizerChange(t *testing.T) {
	st := newStore(t)
	readOnlyFixture(t, st, "tok", "researcher", "claude-opus-4-6", 12, nil)

	f, err := readOnlyAgentRule{}.Run(input(t, st))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if f == nil {
		t.Fatal("expected a finding for read-only opus 4.6 runs")
	}
	if !strings.Contains(f.WhyItCosts, "newer way") {
		t.Errorf("expected a tokenizer caveat, got:\n%s", f.WhyItCosts)
	}
	if !strings.Contains(f.WhyItCosts, "smaller than shown") {
		t.Errorf("expected the caveat to name the direction of the error, got:\n%s", f.WhyItCosts)
	}
	if f.Confidence != Medium {
		t.Errorf("12 runs alone would be High; a tokenizer change should soften it to Medium, got %q", f.Confidence)
	}
}

// Opus to Sonnet stays inside one tokenizer family, so there is nothing to
// caveat and nothing to soften.
func TestReadOnlyAgentSilentWhenTokenizerMatches(t *testing.T) {
	st := newStore(t)
	readOnlyFixture(t, st, "same", "researcher", "claude-opus-5", 12, nil)

	f, err := readOnlyAgentRule{}.Run(input(t, st))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if f == nil {
		t.Fatal("expected a finding for read-only opus runs")
	}
	if strings.Contains(f.WhyItCosts, "older way") || strings.Contains(f.WhyItCosts, "newer way") {
		t.Errorf("opus -> sonnet shares a tokenizer; no caveat expected, got:\n%s", f.WhyItCosts)
	}
	if f.Confidence != High {
		t.Errorf("Confidence = %q, want high", f.Confidence)
	}
}
