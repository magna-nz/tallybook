package ledger_test

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/magna-nz/tallybook/internal/ledger"
	"github.com/magna-nz/tallybook/internal/model"
	"github.com/magna-nz/tallybook/internal/pricing"
	"github.com/magna-nz/tallybook/internal/store"
)

// now is the fixed clock every test runs against.
var now = time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)

// start is when the fixture sessions begin.
var start = time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)

func newStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "tallybook.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func ingestTranscripts(t *testing.T, st *store.Store, trs ...*model.Transcript) {
	t.Helper()
	for _, tr := range trs {
		if err := st.ReplaceTranscript(tr, 1024, now); err != nil {
			t.Fatalf("ingest %s: %v", tr.Session.ID, err)
		}
	}
}

func usage(input, output int64) model.Usage {
	return model.Usage{Input: input, Output: output}
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

// runSpec describes one sub-agent run to build: the model it used, when it
// started, how many tool calls it made, and how many of those tool results
// errored.
type runSpec struct {
	model     string
	at        time.Time
	toolCalls int
	errors    int
	// duration is how long the run lasted. Zero means an hour, which is what
	// most of these fixtures want; tests about overlapping runs set it so a
	// short run can sit inside a long one.
	duration time.Duration
}

// buildAgentRuns builds one parent transcript that launches len(specs)
// sub-agents of agentType (one per spec, in order), plus one child
// transcript per run, and ingests all of it into st.
func buildAgentRuns(t *testing.T, st *store.Store, parentID, agentType string, specs []runSpec) {
	t.Helper()
	parent := &model.Transcript{Session: session(parentID, "/work/app", start)}

	trs := []*model.Transcript{parent}
	for i, spec := range specs {
		agentID := fmt.Sprintf("%s-a%d", parentID, i)
		childID := fmt.Sprintf("%s-child%d", parentID, i)
		launchCallID := fmt.Sprintf("%s-launch%d", parentID, i)

		parent.Turns = append(parent.Turns, model.Turn{
			SessionID: parentID,
			ID:        fmt.Sprintf("%s-t%d", parentID, i),
			Timestamp: spec.at,
			Model:     "claude-opus-5",
			Usage:     usage(500, 500),
			ToolCalls: []model.ToolCall{{
				ID: launchCallID, Name: "Agent", InputChars: 100,
				Agent: &model.AgentLaunch{
					SubagentType:  agentType,
					ResolvedModel: spec.model,
					AgentID:       agentID,
				},
			}},
		})

		n := spec.toolCalls
		if n <= 0 {
			n = 1
		}
		childSess := childSession(childID, parentID, agentID, "/work/app", spec.at)
		if spec.duration > 0 {
			childSess.EndedAt = spec.at.Add(spec.duration)
		}
		child := &model.Transcript{Session: childSess}
		for j := 0; j < n; j++ {
			tcID := fmt.Sprintf("%s-tc%d", childID, j)
			child.Turns = append(child.Turns, model.Turn{
				SessionID: childID,
				ID:        fmt.Sprintf("%s-t%d", childID, j),
				Timestamp: spec.at.Add(time.Duration(j) * time.Minute),
				Model:     spec.model,
				Usage:     usage(2_000, 1_000),
				ToolCalls: []model.ToolCall{{ID: tcID, Name: "Bash", InputChars: 50}},
			})
			child.ToolResults = append(child.ToolResults, model.ToolResult{
				SessionID:  childID,
				ToolCallID: tcID,
				Timestamp:  spec.at.Add(time.Duration(j) * time.Minute).Add(time.Second),
				IsError:    j < spec.errors,
			})
		}
		trs = append(trs, child)
	}

	ingestTranscripts(t, st, trs...)
}

// runsAt builds n runSpecs of one model, five minutes apart starting at at,
// each with 5 tool calls and errRate*5 errors.
func runsAt(modelID string, at time.Time, n, toolCalls, errors int) []runSpec {
	specs := make([]runSpec, n)
	for i := range specs {
		specs[i] = runSpec{
			model:     modelID,
			at:        at.Add(time.Duration(i) * 5 * time.Minute),
			toolCalls: toolCalls,
			errors:    errors,
		}
	}
	return specs
}

func TestChangesCleanSwitchGivesKeep(t *testing.T) {
	st := newStore(t)
	pr := pricing.Default()

	var specs []runSpec
	specs = append(specs, runsAt("claude-opus-5", start, 5, 5, 0)...)
	specs = append(specs, runsAt("claude-sonnet-5", start.Add(24*time.Hour), 5, 5, 0)...)
	buildAgentRuns(t, st, "p1", "researcher", specs)

	changes, err := ledger.Changes(st, pr, store.Filter{}, 3)
	if err != nil {
		t.Fatalf("Changes: %v", err)
	}
	if len(changes) != 1 {
		t.Fatalf("len(changes) = %d, want 1", len(changes))
	}
	c := changes[0]
	if c.Agent != "researcher" {
		t.Errorf("Agent = %q, want researcher", c.Agent)
	}
	if c.From != "claude-opus-5" || c.To != "claude-sonnet-5" {
		t.Errorf("From/To = %q/%q, want claude-opus-5/claude-sonnet-5", c.From, c.To)
	}
	if c.Before.Runs != 5 || c.After.Runs != 5 {
		t.Errorf("Before/After runs = %d/%d, want 5/5", c.Before.Runs, c.After.Runs)
	}
	if c.After.AvgUSD >= c.Before.AvgUSD {
		t.Errorf("After.AvgUSD = %v, want less than Before.AvgUSD = %v", c.After.AvgUSD, c.Before.AvgUSD)
	}
	if c.Verdict != ledger.VerdictKeep {
		t.Errorf("Verdict = %q, want %q", c.Verdict, ledger.VerdictKeep)
	}
}

func TestChangesErrorRateJumpGivesRevert(t *testing.T) {
	st := newStore(t)
	pr := pricing.Default()

	var specs []runSpec
	specs = append(specs, runsAt("claude-opus-5", start, 5, 5, 0)...)
	specs = append(specs, runsAt("claude-sonnet-5", start.Add(24*time.Hour), 5, 5, 2)...) // 2/5 = 40% errors
	buildAgentRuns(t, st, "p1", "researcher", specs)

	changes, err := ledger.Changes(st, pr, store.Filter{}, 3)
	if err != nil {
		t.Fatalf("Changes: %v", err)
	}
	if len(changes) != 1 {
		t.Fatalf("len(changes) = %d, want 1", len(changes))
	}
	c := changes[0]
	if c.Before.ErrorRate != 0 {
		t.Errorf("Before.ErrorRate = %v, want 0", c.Before.ErrorRate)
	}
	if c.After.ErrorRate < 0.35 {
		t.Errorf("After.ErrorRate = %v, want about 0.4", c.After.ErrorRate)
	}
	if c.Verdict != ledger.VerdictRevert {
		t.Errorf("Verdict = %q, want %q (cost dropping does not save it from a sharp error rise)", c.Verdict, ledger.VerdictRevert)
	}
}

func TestChangesTooFewRunsAfterGivesTooEarly(t *testing.T) {
	st := newStore(t)
	pr := pricing.Default()

	var specs []runSpec
	specs = append(specs, runsAt("claude-opus-5", start, 5, 5, 0)...)
	specs = append(specs, runsAt("claude-sonnet-5", start.Add(24*time.Hour), 2, 5, 0)...)
	buildAgentRuns(t, st, "p1", "researcher", specs)

	changes, err := ledger.Changes(st, pr, store.Filter{}, 3)
	if err != nil {
		t.Fatalf("Changes: %v", err)
	}
	if len(changes) != 1 {
		t.Fatalf("len(changes) = %d, want 1", len(changes))
	}
	if got := changes[0].Verdict; got != ledger.VerdictTooEarly {
		t.Errorf("Verdict = %q, want %q for only 2 runs after the switch", got, ledger.VerdictTooEarly)
	}
}

func TestChangesAliasEqualityProducesNoChange(t *testing.T) {
	st := newStore(t)
	pr := pricing.Default()

	var specs []runSpec
	specs = append(specs, runsAt("sonnet", start, 5, 5, 0)...)
	specs = append(specs, runsAt("claude-sonnet-5", start.Add(24*time.Hour), 5, 5, 0)...)
	buildAgentRuns(t, st, "p1", "researcher", specs)

	changes, err := ledger.Changes(st, pr, store.Filter{}, 3)
	if err != nil {
		t.Fatalf("Changes: %v", err)
	}
	if len(changes) != 0 {
		t.Fatalf("len(changes) = %d, want 0: \"sonnet\" and \"claude-sonnet-5\" are the same model", len(changes))
	}
}

func TestChangesNoSwitchProducesNothing(t *testing.T) {
	st := newStore(t)
	pr := pricing.Default()

	specs := runsAt("claude-opus-5", start, 5, 5, 0)
	buildAgentRuns(t, st, "p1", "researcher", specs)

	changes, err := ledger.Changes(st, pr, store.Filter{}, 3)
	if err != nil {
		t.Fatalf("Changes: %v", err)
	}
	if len(changes) != 0 {
		t.Fatalf("len(changes) = %d, want 0 for an agent that never changed model", len(changes))
	}
}

func TestChangesAreNewestFirst(t *testing.T) {
	st := newStore(t)
	pr := pricing.Default()

	var researcherSpecs []runSpec
	researcherSpecs = append(researcherSpecs, runsAt("claude-opus-5", start, 5, 5, 0)...)
	researcherSpecs = append(researcherSpecs, runsAt("claude-sonnet-5", start.Add(24*time.Hour), 5, 5, 0)...)
	buildAgentRuns(t, st, "p1", "researcher", researcherSpecs)

	var reviewerSpecs []runSpec
	reviewerSpecs = append(reviewerSpecs, runsAt("claude-sonnet-5", start, 5, 5, 0)...)
	reviewerSpecs = append(reviewerSpecs, runsAt("claude-opus-5", start.Add(72*time.Hour), 5, 5, 0)...)
	buildAgentRuns(t, st, "p2", "reviewer", reviewerSpecs)

	changes, err := ledger.Changes(st, pr, store.Filter{}, 3)
	if err != nil {
		t.Fatalf("Changes: %v", err)
	}
	if len(changes) != 2 {
		t.Fatalf("len(changes) = %d, want 2", len(changes))
	}
	if changes[0].Agent != "reviewer" || changes[1].Agent != "researcher" {
		t.Fatalf("changes = %v, %v; want reviewer's later change first", changes[0].Agent, changes[1].Agent)
	}
	if !changes[0].At.After(changes[1].At) {
		t.Errorf("changes[0].At = %v, want it after changes[1].At = %v", changes[0].At, changes[1].At)
	}
}

// Flipping a model back and forth produces a change per flip, most of them
// undecided. The decided ones must come first so they are not buried.
func TestChangesOrdersDecidedVerdictsFirst(t *testing.T) {
	st := newStore(t)

	var specs []runSpec
	specs = append(specs, runsAt("claude-opus-5", start, 5, 5, 0)...)
	specs = append(specs, runsAt("claude-sonnet-5", start.Add(24*time.Hour), 5, 5, 0)...)
	// One lone run back on opus: a more recent change, but undecidable.
	specs = append(specs, runsAt("claude-opus-5", start.Add(48*time.Hour), 1, 5, 0)...)
	buildAgentRuns(t, st, "p1", "researcher", specs)

	changes, err := ledger.Changes(st, pricing.Default(), store.Filter{}, 3)
	if err != nil {
		t.Fatalf("Changes: %v", err)
	}
	if len(changes) != 2 {
		t.Fatalf("len(changes) = %d, want 2", len(changes))
	}
	if changes[0].Verdict == ledger.VerdictTooEarly {
		t.Errorf("undecided change came first: %+v", changes[0])
	}
	if changes[1].Verdict != ledger.VerdictTooEarly {
		t.Errorf("expected the undecided change last, got %+v", changes[1])
	}
}

// Dispatching a wave of sub-agents with mixed models puts runs of both on the
// timeline at once. That is not a change from one model to the other, and
// reading it as one is exactly what this user's real history produced.
func TestChangesIgnoresConcurrentRunsOfDifferentModels(t *testing.T) {
	st := newStore(t)

	// Five opus runs and five sonnet runs, all launched within the same few
	// minutes and all still running when the next began.
	var specs []runSpec
	for i := 0; i < 5; i++ {
		specs = append(specs, runSpec{
			model: "claude-opus-5", at: start.Add(time.Duration(i) * time.Minute),
			toolCalls: 5,
		})
		specs = append(specs, runSpec{
			model: "claude-sonnet-5", at: start.Add(time.Duration(i)*time.Minute + 30*time.Second),
			toolCalls: 5,
		})
	}
	buildAgentRuns(t, st, "wave", "implementer", specs)

	changes, err := ledger.Changes(st, pricing.Default(), store.Filter{}, 3)
	if err != nil {
		t.Fatalf("Changes: %v", err)
	}
	for _, c := range changes {
		t.Errorf("runs that overlapped in time were read as a change: %s %s to %s at %s",
			c.Agent, c.From, c.To, c.At.Format(time.RFC3339))
	}
}

// A real switch, where every run of the old model finished before the first
// run of the new one started, must still be found.
func TestChangesStillFindsASequentialSwitch(t *testing.T) {
	st := newStore(t)

	var specs []runSpec
	specs = append(specs, runsAt("claude-opus-5", start, 5, 5, 0)...)
	// A clear day between the two groups, so nothing overlaps.
	specs = append(specs, runsAt("claude-sonnet-5", start.Add(24*time.Hour), 5, 5, 0)...)
	buildAgentRuns(t, st, "seq", "implementer", specs)

	changes, err := ledger.Changes(st, pricing.Default(), store.Filter{}, 3)
	if err != nil {
		t.Fatalf("Changes: %v", err)
	}
	if len(changes) != 1 {
		t.Fatalf("a genuine sequential switch should still be found, got %d changes", len(changes))
	}
	if changes[0].MinRuns != 3 {
		t.Errorf("MinRuns = %d, want the threshold it was judged against", changes[0].MinRuns)
	}
}

// A run that spans a later segment makes the whole stretch one concurrent
// wave, however the start times happen to order. Comparing only against the
// immediately preceding segment misses that and reports a change that never
// happened.
//
// This is a guard against a real shape, not one observed in the wild: the
// corpus this was written against has waves that abut closely but do not
// actually span each other, so the narrower guard happened to be enough there.
func TestChangesIgnoresAWaveSpannedByALongRun(t *testing.T) {
	st := newStore(t)

	// Opus runs long. A short Sonnet run sits entirely inside it, and another
	// Opus run starts after the Sonnet finished but while the first is still
	// going. Segmented by start time that reads Opus, Sonnet, Opus.
	specs := []runSpec{
		// Runs for a full hour.
		{model: "claude-opus-5", at: start, toolCalls: 5, duration: time.Hour},
		// Starts and finishes well inside that hour.
		{model: "claude-sonnet-5", at: start.Add(time.Minute), toolCalls: 1, duration: 5 * time.Minute},
		// Starts after the Sonnet run ended, but while the first Opus run is
		// still going. Looking only at the Sonnet segment sees a clear gap.
		{model: "claude-opus-5", at: start.Add(30 * time.Minute), toolCalls: 5, duration: 10 * time.Minute},
	}
	buildAgentRuns(t, st, "span", "implementer", specs)

	changes, err := ledger.Changes(st, pricing.Default(), store.Filter{}, 1)
	if err != nil {
		t.Fatalf("Changes: %v", err)
	}
	for _, c := range changes {
		t.Errorf("a wave spanned by a long run was read as a change: %s to %s at %s",
			c.From, c.To, c.At.Format(time.RFC3339))
	}
}

// The guard must not swallow a real switch that happens to follow a long run.
// Once everything before has finished, a later change is genuine.
func TestChangesFindsASwitchAfterALongRunFinished(t *testing.T) {
	st := newStore(t)

	var specs []runSpec
	specs = append(specs, runsAt("claude-opus-5", start, 3, 5, 0)...)
	// Well after every opus run has ended.
	specs = append(specs, runsAt("claude-sonnet-5", start.Add(48*time.Hour), 3, 5, 0)...)
	buildAgentRuns(t, st, "after", "implementer", specs)

	changes, err := ledger.Changes(st, pricing.Default(), store.Filter{}, 3)
	if err != nil {
		t.Fatalf("Changes: %v", err)
	}
	if len(changes) != 1 {
		t.Fatalf("a switch after everything finished should still be found, got %d", len(changes))
	}
	if changes[0].From != "claude-opus-5" || changes[0].To != "claude-sonnet-5" {
		t.Errorf("unexpected change: %s to %s", changes[0].From, changes[0].To)
	}
}
