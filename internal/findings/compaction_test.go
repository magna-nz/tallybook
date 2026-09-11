package findings

import (
	"fmt"
	"testing"
	"time"

	"github.com/magna-nz/tallybook/internal/model"
	"github.com/magna-nz/tallybook/internal/store"
)

// compactionFixture is a main session of five turns on claude-opus-5, with a
// compaction before turn 1 (context just before it: 10,000) and before turn
// 3 (context just before it: 5,000), so the largest is 10,000 and there are
// two compactions.
//
// Hand calculation of cost at claude-opus-5's rates (input $5, output $25 per
// million tokens):
//
//	turn 0: 10,000*5 +   300*25 =  57,500 -> $0.0575
//	turn 1: 50,000*5 +   300*25 = 257,500 -> $0.2575
//	turn 2:  5,000*5 +   300*25 =  32,500 -> $0.0325
//	turn 3: 80,000*5 +   300*25 = 407,500 -> $0.4075
//	turn 4:  5,000*5 +   300*25 =  32,500 -> $0.0325
//	total:                                   $0.7875
func compactionFixture(id string) *model.Transcript {
	tr := &model.Transcript{Session: session(id, "/work/app", start)}
	contexts := []int64{10_000, 50_000, 5_000, 80_000, 5_000}
	compactBefore := []bool{false, true, false, true, false}
	for i, ctx := range contexts {
		tr.Turns = append(tr.Turns, model.Turn{
			SessionID:        id,
			ID:               fmt.Sprintf("%s-t%d", id, i),
			Timestamp:        start.Add(time.Duration(i) * time.Minute),
			Model:            "claude-opus-5",
			TextChars:        100,
			Usage:            usage(ctx, 0, 0, 300, 0),
			CompactionBefore: compactBefore[i],
		})
	}
	return tr
}

// compactionChildFixture is a sub-agent session with the same compaction
// shape as compactionFixture but a larger context, so a bug that failed to
// exclude sub-agent sessions would change both the count and the largest
// context reported.
func compactionChildFixture(parentID string, i int) *model.Transcript {
	id := fmt.Sprintf("%s-child%d", parentID, i)
	agentID := fmt.Sprintf("%s-a%d", parentID, i)
	tr := &model.Transcript{Session: childSession(id, parentID, agentID, "/work/app", start)}
	contexts := []int64{200_000, 300_000, 200_000, 400_000, 200_000}
	compactBefore := []bool{false, true, false, true, false}
	for i, ctx := range contexts {
		tr.Turns = append(tr.Turns, model.Turn{
			SessionID:        id,
			ID:               fmt.Sprintf("%s-t%d", id, i),
			Timestamp:        start.Add(time.Duration(i) * time.Minute),
			Model:            "claude-opus-5",
			TextChars:        100,
			Usage:            usage(ctx, 0, 0, 300, 0),
			CompactionBefore: compactBefore[i],
		})
	}
	return tr
}

func TestCompactionFiresAndMatchesHandCalculation(t *testing.T) {
	st := newStore(t)
	ingest(t, st, compactionFixture("cp1"), compactionFixture("cp2"))

	f, err := compactionRule{}.Run(input(t, st))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if f == nil {
		t.Fatal("expected a finding for two sessions that each compacted twice")
	}
	if f.Direction != Context {
		t.Errorf("Direction = %q, want %q", f.Direction, Context)
	}
	if f.Confidence != Info {
		t.Errorf("Confidence = %q, want %q", f.Confidence, Info)
	}
	if f.SavingUSD != 0 {
		t.Errorf("SavingUSD = %v, want 0: this finding cannot price the summary request", f.SavingUSD)
	}
	if got := len(f.Evidence.Rows); got != 2 {
		t.Fatalf("evidence rows = %d, want 2", got)
	}
	for _, row := range f.Evidence.Rows {
		if row[2] != "2" {
			t.Errorf("evidence compactions = %q, want 2", row[2])
		}
		if row[3] != "10,000" {
			t.Errorf("evidence largest context = %q, want 10,000", row[3])
		}
		if row[4] != "$0.79" {
			t.Errorf("evidence cost = %q, want $0.79", row[4])
		}
	}
	assertPlainEnglish(t, *f)
}

func TestCompactionBelowSessionFloor(t *testing.T) {
	st := newStore(t)
	ingest(t, st, compactionFixture("cp1"))

	f, err := compactionRule{}.Run(input(t, st))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if f != nil {
		t.Fatalf("expected no finding from a single qualifying session, got %q", f.Title)
	}
}

func TestCompactionSessionNeedsAtLeastTwoCompactions(t *testing.T) {
	st := newStore(t)
	oneCompaction := compactionFixture("cp2")
	oneCompaction.Turns[3].CompactionBefore = false // now only one compaction in this session
	ingest(t, st, compactionFixture("cp1"), oneCompaction)

	f, err := compactionRule{}.Run(input(t, st))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if f != nil {
		t.Fatalf("expected no finding: only one session reaches two compactions, got %q", f.Title)
	}
}

func TestCompactionIgnoresSubAgentSessions(t *testing.T) {
	st := newStore(t)
	ingest(t, st,
		compactionFixture("cp1"), compactionFixture("cp2"),
		compactionChildFixture("cp1", 0),
	)

	f, err := compactionRule{}.Run(input(t, st))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if f == nil {
		t.Fatal("expected a finding from the two main sessions")
	}
	if got := len(f.Evidence.Rows); got != 2 {
		t.Fatalf("evidence rows = %d, want 2: the sub-agent session must not appear", got)
	}
	for _, row := range f.Evidence.Rows {
		if row[3] != "10,000" {
			t.Errorf("evidence largest context = %q, want 10,000 (the sub-agent's larger context must be excluded)", row[3])
		}
	}
}

func init() {
	adviceFixtures = append(adviceFixtures, func(t *testing.T, st *store.Store) {
		ingest(t, st, compactionFixture("cp-advice1"), compactionFixture("cp-advice2"))
	})
}

func TestCompactionAdviceNamesRealSettings(t *testing.T) {
	st := newStore(t)
	ingest(t, st, compactionFixture("cp-advice1"), compactionFixture("cp-advice2"))

	f, err := compactionRule{}.Run(input(t, st))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if f == nil {
		t.Fatal("expected a finding")
	}
	for field, text := range map[string]string{
		"WhatHappened": f.WhatHappened,
		"WhyItCosts":   f.WhyItCosts,
		"WhatToChange": f.WhatToChange,
		"WhatToExpect": f.WhatToExpect,
	} {
		for _, m := range settingLike.FindAllStringSubmatch(text, -1) {
			name := firstNonEmpty(m[1:])
			if !settingsRegistry[name] {
				t.Errorf("%s names %q, which is not in settingsRegistry:\n%s", field, name, text)
			}
		}
	}
}
