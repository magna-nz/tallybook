package findings

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/magna-nz/tallybook/internal/model"
	"github.com/magna-nz/tallybook/internal/store"
)

// contextSession builds a main session whose turns re-sent a conversation of
// the given size, one turn per entry. The whole conversation is charged at the
// cache-read rate, which is what re-sending history actually looks like.
func contextSession(id string, contexts []int64) *model.Transcript {
	tr := &model.Transcript{Session: session(id, "/work/app", start)}
	for i, ctx := range contexts {
		tr.Turns = append(tr.Turns, model.Turn{
			SessionID: id,
			ID:        fmt.Sprintf("%s-t%d", id, i),
			Timestamp: start.Add(time.Duration(i) * time.Minute),
			Model:     "claude-opus-5",
			TextChars: 500,
			Usage:     usage(0, ctx, 0, 1_000, 0),
		})
	}
	return tr
}

// grownContexts is two small turns followed by n turns above the threshold, so
// a fixture exercises both sides of the line. At 300,000 tokens of cache read
// on Opus each big turn is taxed 300,000 x $0.50 per million x 200/300 = $0.10.
func grownContexts(big int) []int64 {
	out := []int64{50_000, 60_000}
	for i := 0; i < big; i++ {
		out = append(out, 300_000)
	}
	return out
}

// longContextFixture ingests n sessions that each pay about $0.60 of tax.
func longContextFixture(t *testing.T, st *store.Store, prefix string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		ingest(t, st, contextSession(fmt.Sprintf("%s%d", prefix, i), grownContexts(6)))
	}
}

func init() {
	// So TestAdviceOnlyNamesKnownSettings scans this rule's prose once the rule
	// is registered in All().
	adviceFixtures = append(adviceFixtures, func(t *testing.T, st *store.Store) {
		longContextFixture(t, st, "longctx", longContextMinSessions)
	})
}

func TestLongContextFires(t *testing.T) {
	st := newStore(t)
	longContextFixture(t, st, "lc", 3)

	f, err := longContextRule{}.Run(input(t, st))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if f == nil {
		t.Fatal("expected a finding for three sessions well past the threshold")
	}
	if f.Direction != Context {
		t.Errorf("Direction = %q, want %q", f.Direction, Context)
	}
	if f.Confidence != Medium {
		t.Errorf("Confidence = %q, want %q", f.Confidence, Medium)
	}
	if f.SavingUSD <= 0 {
		t.Errorf("SavingUSD = %v, want > 0", f.SavingUSD)
	}
	for _, want := range []string{"3 sessions", "100,000 tokens", "18 turns", "300,000 tokens"} {
		if !strings.Contains(f.WhatHappened, want) {
			t.Errorf("WhatHappened missing %q:\n%s", want, f.WhatHappened)
		}
	}
	if !strings.Contains(f.WhyItCosts, "half of that") {
		t.Errorf("WhyItCosts should state the half assumption:\n%s", f.WhyItCosts)
	}
	for _, want := range []string{"`/context`", "`/clear`", "`/compact`", "autoCompactWindow",
		"~/.claude/settings.json", "Codex has no equivalent setting"} {
		if !strings.Contains(f.WhatToChange, want) {
			t.Errorf("WhatToChange missing %q:\n%s", want, f.WhatToChange)
		}
	}
	if got := len(f.Evidence.Rows); got != 3 {
		t.Fatalf("evidence rows = %d, want 3", got)
	}
	if got := f.Evidence.Columns[2]; got != "turns over 100k" {
		t.Errorf("evidence column = %q, want %q", got, "turns over 100k")
	}
	row := f.Evidence.Rows[0]
	if row[2] != "6" {
		t.Errorf("turns over the threshold = %q, want 6: the two small turns do not count", row[2])
	}
	if row[3] != "300,000" {
		t.Errorf("peak context = %q, want 300,000", row[3])
	}
	if row[4] != "$0.60" {
		t.Errorf("tax = %q, want $0.60", row[4])
	}
	assertPlainEnglish(t, *f)
	assertKnownSettings(t, *f)

	t.Logf("long-context-tax\n\nTITLE: %s\n\nWHAT HAPPENED: %s\n\nWHY IT COSTS: %s\n\n"+
		"WHAT TO CHANGE: %s\n\nWHAT TO EXPECT: %s\n\nEVIDENCE: %v\n%v",
		f.Title, f.WhatHappened, f.WhyItCosts, f.WhatToChange, f.WhatToExpect,
		f.Evidence.Columns, f.Evidence.Rows)
}

func TestLongContextBelowSessionFloor(t *testing.T) {
	st := newStore(t)
	longContextFixture(t, st, "lc", longContextMinSessions-1)

	f, err := longContextRule{}.Run(input(t, st))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if f != nil {
		t.Fatalf("two qualifying sessions is below the floor of %d, got %q", longContextMinSessions, f.Title)
	}
}

// A session can run above the threshold and still not be worth a paragraph.
func TestLongContextBelowTaxFloor(t *testing.T) {
	st := newStore(t)
	for i := 0; i < 4; i++ {
		// 110,000 tokens of cache read is $0.055 a turn, of which 10/110 is the
		// part above the threshold: half a cent a turn, nowhere near the floor.
		ingest(t, st, contextSession(fmt.Sprintf("small%d", i), []int64{110_000, 110_000, 110_000}))
	}

	f, err := longContextRule{}.Run(input(t, st))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if f != nil {
		t.Fatalf("expected no finding below %v of tax per session, got %q", longContextMinTaxUSD, f.Title)
	}
}

func TestLongContextIgnoresTurnsAtTheThreshold(t *testing.T) {
	st := newStore(t)
	for i := 0; i < 4; i++ {
		contexts := make([]int64, 20)
		for j := range contexts {
			contexts[j] = longContextTokens
		}
		ingest(t, st, contextSession(fmt.Sprintf("edge%d", i), contexts))
	}

	f, err := longContextRule{}.Run(input(t, st))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if f != nil {
		t.Fatalf("a turn exactly at the threshold is not above it, got %q", f.Title)
	}
}

func TestLongContextIgnoresSubAgentSessions(t *testing.T) {
	st := newStore(t)
	// The same traffic, but in sub-agent transcripts: their conversation is
	// thrown away when they report back, so there is nothing to clear.
	for i := 0; i < 4; i++ {
		id := fmt.Sprintf("kid%d", i)
		tr := contextSession(id, grownContexts(6))
		tr.Session = childSession(id, "parent", fmt.Sprintf("agent%d", i), "/work/app", start)
		ingest(t, st, tr)
	}

	f, err := longContextRule{}.Run(input(t, st))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if f != nil {
		t.Fatalf("sub-agent sessions must be ignored, got %q", f.Title)
	}
}

// The tax is the input side of each oversized turn, scaled by how much of the
// conversation sat above the threshold, and the claimed saving is half of it.
func TestLongContextSavingMatchesHandCalculation(t *testing.T) {
	st := newStore(t)
	// One turn per session, 600,000 tokens of conversation on Opus:
	//   100,000 uncached input  x $5.00 per million = $0.500
	//   400,000 cache reads     x $0.50 per million = $0.200
	//   100,000 cache writes    x $6.25 per million = $0.625
	// The 2,000 output tokens are the answer, not the history, so they are not
	// taxed. 500,000 of the 600,000 tokens sit above the threshold.
	for i := 0; i < 3; i++ {
		tr := contextSession(fmt.Sprintf("calc%d", i), []int64{600_000})
		tr.Turns[0].Usage = usage(100_000, 400_000, 100_000, 2_000, 0)
		ingest(t, st, tr)
	}

	f, err := longContextRule{}.Run(input(t, st))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if f == nil {
		t.Fatal("expected a finding")
	}
	const perTurn = (0.500 + 0.200 + 0.625) * 500_000 / 600_000
	const want = 3 * perTurn / 2 // half the tax; the window is 30 days, so this is the monthly figure
	if math.Abs(f.SavingUSD-want) > 1e-9 {
		t.Errorf("SavingUSD = %v, want %v", f.SavingUSD, want)
	}
	if !strings.Contains(f.WhyItCosts, fmtUSD(3*perTurn)) {
		t.Errorf("WhyItCosts should name the full tax of %s:\n%s", fmtUSD(3*perTurn), f.WhyItCosts)
	}
}
