package ledger_test

import (
	"testing"
	"time"

	"github.com/magna-nz/tallybook/internal/ledger"
	"github.com/magna-nz/tallybook/internal/pricing"
	"github.com/magna-nz/tallybook/internal/store"
)

// The agents table names the effort level a sub-agent mostly ran at, and
// leaves it blank when the transcripts never recorded one.
func TestAgentsReportsMostCommonEffort(t *testing.T) {
	st := newStore(t)
	buildAgentRuns(t, st, "p1", "researcher", []runSpec{
		{model: "claude-opus-5", at: start, toolCalls: 2, effort: "high"},
		{model: "claude-opus-5", at: start.Add(10 * time.Minute), toolCalls: 2, effort: "xhigh"},
		{model: "claude-opus-5", at: start.Add(20 * time.Minute), toolCalls: 2, effort: "high"},
	})
	buildAgentRuns(t, st, "p2", "implementer", []runSpec{
		{model: "claude-sonnet-5", at: start, toolCalls: 2},
	})

	rows, err := ledger.Agents(st, pricing.Default(), store.Filter{})
	if err != nil {
		t.Fatalf("Agents: %v", err)
	}
	got := map[string]string{}
	for _, r := range rows {
		got[r.Agent] = r.Effort
	}
	if got["researcher"] != "high" {
		t.Errorf("researcher effort = %q, want high", got["researcher"])
	}
	if got["implementer"] != "" {
		t.Errorf("implementer effort = %q, want empty", got["implementer"])
	}
}
