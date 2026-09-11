package findings

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/magna-nz/tallybook/internal/model"
	"github.com/magna-nz/tallybook/internal/store"
)

// repeatedCallsFixture is a main session where one read-only tool is called
// with byte-identical input on the first four of ten turns, then never
// again. Each of those four results is 400,000 chars (100,000 tokens at
// charsPerToken). The remaining six turns carry no tool calls at all; they
// exist only so the repeated results have turns left to be carried across.
//
// Hand calculation, at claude-opus-5's cache-read rate of $0.5/million:
//
//	call at turn 1: 100,000 tokens * (10-1-1)=8 turns after * 0.5/1e6 = $0.40
//	call at turn 2: 100,000 tokens * (10-2-1)=7 turns after * 0.5/1e6 = $0.35
//	call at turn 3: 100,000 tokens * (10-3-1)=6 turns after * 0.5/1e6 = $0.30
//	session total (the call at turn 0 is the first, not an extra):    $1.05
//
// Each turn reads 400,000 tokens back from the cache, so the session paid
// $2.00 to carry its context: more than the $1.05 the repeats account for,
// which keeps the measured cap out of the arithmetic here.
func repeatedCallsFixture(id, tool string, readOnly bool, sameDigest bool) *model.Transcript {
	return repeatedCallsFixtureWithCacheReads(id, tool, readOnly, sameDigest, 400_000)
}

func repeatedCallsFixtureWithCacheReads(id, tool string, readOnly, sameDigest bool, cacheRead int64) *model.Transcript {
	tr := &model.Transcript{Session: session(id, "/work/app", start)}
	digest := model.DigestInput(tool, []byte("same input every time"))
	for i := 0; i < 10; i++ {
		turn := model.Turn{
			SessionID: id,
			ID:        fmt.Sprintf("%s-t%d", id, i),
			Timestamp: start.Add(time.Duration(i) * time.Minute),
			Model:     "claude-opus-5",
			TextChars: 100,
			Usage:     usage(500, cacheRead, 0, 200, 0),
		}
		if i < 4 {
			callID := fmt.Sprintf("%s-tc%d", id, i)
			tc := model.ToolCall{ID: callID, Name: tool, InputChars: 40}
			if sameDigest {
				tc.InputDigest = digest
			}
			if !readOnly {
				tc.Class = "write"
			}
			turn.ToolCalls = []model.ToolCall{tc}
			tr.ToolResults = append(tr.ToolResults, model.ToolResult{
				SessionID:  id,
				ToolCallID: callID,
				Timestamp:  turn.Timestamp,
				Chars:      400_000,
			})
		}
		tr.Turns = append(tr.Turns, turn)
	}
	return tr
}

func TestRepeatedCallsFiresAndMatchesHandCalculation(t *testing.T) {
	st := newStore(t)
	ingest(t, st,
		repeatedCallsFixture("rep1", "Read", true, true),
		repeatedCallsFixture("rep2", "Read", true, true),
	)

	f, err := repeatedCallsRule{}.Run(input(t, st))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if f == nil {
		t.Fatal("expected a finding for two sessions repeating the same read")
	}
	if f.Direction != Context {
		t.Errorf("Direction = %q, want %q", f.Direction, Context)
	}
	if f.Confidence != Medium {
		t.Errorf("Confidence = %q, want %q", f.Confidence, Medium)
	}
	wantSavingPerSession := 0.40 + 0.35 + 0.30 // $1.05
	wantTotal := wantSavingPerSession * 2
	if got := f.SavingUSD; got < wantTotal-0.001 || got > wantTotal+0.001 {
		t.Errorf("SavingUSD = %v, want about %v", got, wantTotal)
	}
	if got := len(f.Evidence.Rows); got != 2 {
		t.Fatalf("evidence rows = %d, want 2", got)
	}
	for _, row := range f.Evidence.Rows {
		if row[2] != "Read" {
			t.Errorf("evidence tool = %q, want Read", row[2])
		}
		if row[3] != "4" {
			t.Errorf("evidence times = %q, want 4", row[3])
		}
		if row[4] != "100,000" {
			t.Errorf("evidence result tokens = %q, want 100,000", row[4])
		}
		if row[5] != "$1.05" {
			t.Errorf("evidence carried cost = %q, want $1.05", row[5])
		}
	}
	assertPlainEnglish(t, *f)
}

func TestRepeatedCallsIgnoresEmptyInputHash(t *testing.T) {
	st := newStore(t)
	ingest(t, st,
		repeatedCallsFixture("rep1", "Read", true, false),
		repeatedCallsFixture("rep2", "Read", true, false),
	)

	f, err := repeatedCallsRule{}.Run(input(t, st))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if f != nil {
		t.Fatalf("expected no finding when no call carries an input hash, got %q", f.Title)
	}
}

func TestRepeatedCallsIgnoresNonReadOnly(t *testing.T) {
	st := newStore(t)
	ingest(t, st,
		repeatedCallsFixture("rep1", "Bash", false, true),
		repeatedCallsFixture("rep2", "Bash", false, true),
	)

	f, err := repeatedCallsRule{}.Run(input(t, st))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if f != nil {
		t.Fatalf("expected no finding for a repeated write, got %q", f.Title)
	}
}

func TestRepeatedCallsBelowSessionFloor(t *testing.T) {
	st := newStore(t)
	ingest(t, st, repeatedCallsFixture("rep1", "Read", true, true))

	f, err := repeatedCallsRule{}.Run(input(t, st))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if f != nil {
		t.Fatalf("expected no finding from a single qualifying session, got %q", f.Title)
	}
}

func TestRepeatedCallsBelowSavingFloor(t *testing.T) {
	st := newStore(t)
	// Same shape, but the results are tiny, so the carried cost never reaches
	// the $1 floor even with two qualifying sessions.
	tiny := func(id string) *model.Transcript {
		tr := repeatedCallsFixture(id, "Read", true, true)
		for i := range tr.ToolResults {
			tr.ToolResults[i].Chars = 40
		}
		return tr
	}
	ingest(t, st, tiny("rep1"), tiny("rep2"))

	f, err := repeatedCallsRule{}.Run(input(t, st))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if f != nil {
		t.Fatalf("expected no finding below the $1 saving floor, got %q", f.Title)
	}
}

// The store salts and re-hashes every call's input at ingest time, so this
// checks the grouping still works on what Store.Turns hands back, not on the
// in-memory digest a test could otherwise cheat with.
func TestRepeatedCallsCountsTheSameDigestAcrossAStoreRoundTrip(t *testing.T) {
	st := newStore(t)
	ingest(t, st,
		repeatedCallsFixture("rep1", "Read", true, true),
		repeatedCallsFixture("rep2", "Read", true, true),
	)

	turns, err := st.Turns("rep1")
	if err != nil {
		t.Fatalf("turns: %v", err)
	}
	var hashes []string
	for _, tn := range turns {
		for _, tc := range tn.ToolCalls {
			if tc.Name == "Read" {
				hashes = append(hashes, tc.InputHash)
			}
		}
	}
	if len(hashes) != 4 {
		t.Fatalf("expected 4 Read calls, got %d", len(hashes))
	}
	for _, h := range hashes {
		if h == "" || h != hashes[0] {
			t.Fatalf("expected every call's stored hash to match, got %v", hashes)
		}
	}
}

func init() {
	adviceFixtures = append(adviceFixtures, func(t *testing.T, st *store.Store) {
		ingest(t, st,
			repeatedCallsFixture("rc-advice1", "Read", true, true),
			repeatedCallsFixture("rc-advice2", "Read", true, true),
		)
	})
}

func TestRepeatedCallsFixtureUsedByAdvice(t *testing.T) {
	// Sanity check that the registered advice fixture on its own is enough
	// for the rule to fire, so TestAdviceOnlyNamesKnownSettings actually
	// scans this rule's prose rather than skipping it silently.
	st := newStore(t)
	ingest(t, st,
		repeatedCallsFixture("rc-advice1", "Read", true, true),
		repeatedCallsFixture("rc-advice2", "Read", true, true),
	)
	f, err := repeatedCallsRule{}.Run(input(t, st))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if f == nil {
		t.Fatal("advice fixture does not make the rule fire")
	}
	if !strings.Contains(f.WhatToChange, "CLAUDE.md") {
		t.Errorf("WhatToChange = %q, want CLAUDE.md named", f.WhatToChange)
	}
}

// A claim about carried output cannot exceed what the session measurably paid
// to re-read its context. A screenshot-heavy session once claimed hundreds of
// dollars from character counts alone; this is the guard.
func TestRepeatedCallsSavingIsCappedByMeasuredCacheReads(t *testing.T) {
	st := newStore(t)
	// 10 turns × 20,000 cache reads × $0.50/M = $0.10 per session, well under
	// the $1.05 the repeats would otherwise claim.
	ingest(t, st,
		repeatedCallsFixtureWithCacheReads("cap1", "Read", true, true, 20_000),
		repeatedCallsFixtureWithCacheReads("cap2", "Read", true, true, 20_000),
	)
	in := input(t, st)
	in.WindowDays = 30

	f, err := repeatedCallsRule{}.Run(in)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if f != nil {
		// $0.20 across both sessions is under the $1 floor, so the rule stays
		// quiet: the cap did its job.
		t.Fatalf("expected no finding once the carry is capped at $0.20, got saving %.2f", f.SavingUSD)
	}
}
