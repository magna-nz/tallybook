package findings

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/magna-nz/tallybook/internal/config"
	"github.com/magna-nz/tallybook/internal/model"
	"github.com/magna-nz/tallybook/internal/store"
)

// So the invented-setting guard in settings_test.go scans this rule's prose
// too: enough qualifying sessions for cache1hRule to produce a finding.
func init() {
	adviceFixtures = append(adviceFixtures, func(t *testing.T, st *store.Store) {
		for _, id := range []string{"c1h-a", "c1h-b", "c1h-c"} {
			ingest(t, st, cache1hFixture(id, 2*time.Minute, 40_000, 5_000, 3))
		}
	})
}

// cache1hFixture builds a session of n turns, gap apart, whose usage carries
// write1h tokens under the hour-long cache lifetime and write5m under the
// five-minute one.
func cache1hFixture(id string, gap time.Duration, write1h, write5m int64, n int) *model.Transcript {
	tr := &model.Transcript{Session: session(id, "/work/app", start)}
	at := start
	for i := 0; i < n; i++ {
		tr.Turns = append(tr.Turns, model.Turn{
			SessionID: id,
			ID:        fmt.Sprintf("%s-t%d", id, i),
			Timestamp: at,
			Model:     "claude-opus-5",
			TextChars: 400,
			Usage: model.Usage{
				Input: 500, CacheRead: 1_000,
				CacheWrite1h: write1h, CacheWrite5m: write5m,
				Output: 800,
			},
		})
		at = at.Add(gap)
	}
	return tr
}

func TestCache1hFires(t *testing.T) {
	st := newStore(t)
	for _, id := range []string{"c1h1", "c1h2", "c1h3", "c1h4"} {
		ingest(t, st, cache1hFixture(id, 2*time.Minute, 40_000, 5_000, 3))
	}

	f, err := cache1hRule{}.Run(input(t, st))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if f == nil {
		t.Fatal("expected a finding for 4 sessions kept on the hour-long lifetime with short pauses")
	}
	if f.Direction != Cache {
		t.Errorf("Direction = %q, want %q", f.Direction, Cache)
	}
	if f.Confidence != High {
		t.Errorf("Confidence = %q, want %q on PlanAPI", f.Confidence, High)
	}
	if f.SavingUSD <= 0 {
		t.Errorf("SavingUSD = %v, want > 0", f.SavingUSD)
	}
	if !strings.Contains(f.WhatToChange, "promptCacheTtl") {
		t.Errorf("WhatToChange = %q, want the real setting name", f.WhatToChange)
	}
	if !strings.Contains(f.WhatToChange, "subagentPromptCacheTtl") {
		t.Errorf("WhatToChange = %q, want the sub-agent setting named too", f.WhatToChange)
	}
	if !strings.Contains(f.WhatToChange, "Codex has no equivalent setting") {
		t.Errorf("WhatToChange = %q, want the Codex sentence", f.WhatToChange)
	}
	if got := len(f.Evidence.Rows); got != 4 {
		t.Fatalf("evidence rows = %d, want 4", got)
	}
	for _, col := range []string{"session", "project", "longest pause", "1h tokens written", "premium"} {
		found := false
		for _, c := range f.Evidence.Columns {
			if c == col {
				found = true
			}
		}
		if !found {
			t.Errorf("evidence columns %v missing %q", f.Evidence.Columns, col)
		}
	}
	assertPlainEnglish(t, *f)
	t.Logf("WhatHappened: %s", f.WhatHappened)
	t.Logf("WhyItCosts: %s", f.WhyItCosts)
	t.Logf("WhatToChange: %s", f.WhatToChange)
	t.Logf("WhatToExpect: %s", f.WhatToExpect)
}

func TestCache1hBelowSessionFloorIsSilent(t *testing.T) {
	st := newStore(t)
	// Only two qualifying sessions; the floor is three.
	for _, id := range []string{"c1h1", "c1h2"} {
		ingest(t, st, cache1hFixture(id, 2*time.Minute, 40_000, 5_000, 3))
	}

	f, err := cache1hRule{}.Run(input(t, st))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if f != nil {
		t.Fatalf("expected no finding below the session floor, got %q", f.Title)
	}
}

func TestCache1hBelowPremiumFloorIsSilent(t *testing.T) {
	st := newStore(t)
	// Three qualifying sessions, but the premium on cheap tokens is far below
	// the one-dollar floor.
	for _, id := range []string{"c1h1", "c1h2", "c1h3"} {
		tr := cache1hFixture(id, time.Minute, 100, 10, 1)
		for i := range tr.Turns {
			tr.Turns[i].Model = "claude-haiku-4-5"
		}
		ingest(t, st, tr)
	}

	f, err := cache1hRule{}.Run(input(t, st))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if f != nil {
		t.Fatalf("expected no finding below the premium floor, got %q", f.Title)
	}
}

func TestCache1hSilentWhenAGapExceedsTheLifetime(t *testing.T) {
	st := newStore(t)
	// Same shape as the firing fixture, but every gap is ten minutes: past
	// the five-minute lifetime, so none of these sessions qualify.
	for _, id := range []string{"c1h1", "c1h2", "c1h3"} {
		ingest(t, st, cache1hFixture(id, 10*time.Minute, 40_000, 5_000, 3))
	}

	f, err := cache1hRule{}.Run(input(t, st))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if f != nil {
		t.Fatalf("expected no finding when every gap outran the cache, got %q", f.Title)
	}
}

func TestCache1hSilentWhenFiveMinuteWritesDominate(t *testing.T) {
	st := newStore(t)
	// write5m outweighs write1h, so condition (a) never holds.
	for _, id := range []string{"c1h1", "c1h2", "c1h3"} {
		ingest(t, st, cache1hFixture(id, time.Minute, 1_000, 5_000, 3))
	}

	f, err := cache1hRule{}.Run(input(t, st))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if f != nil {
		t.Fatalf("expected no finding when five-minute writes dominate, got %q", f.Title)
	}
}

// The premium is measured, not estimated, so it must match a hand
// calculation exactly. claude-opus-5 costs 10 per million for a one-hour
// write and 6.25 for a five-minute one; the difference is 3.75 per million.
// Half a million 1h tokens on each of two turns therefore cost a premium of
// $3.75 per session, and three such sessions cost $11.25 in total.
func TestCache1hPremiumMatchesHandCalculation(t *testing.T) {
	st := newStore(t)
	for _, id := range []string{"c1h1", "c1h2", "c1h3"} {
		ingest(t, st, cache1hFixture(id, time.Minute, 500_000, 0, 2))
	}

	f, err := cache1hRule{}.Run(input(t, st))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if f == nil {
		t.Fatal("expected a finding")
	}
	const want = 11.25
	if f.SavingUSD != want {
		t.Errorf("SavingUSD = %v, want %v", f.SavingUSD, want)
	}
	for _, row := range f.Evidence.Rows {
		if row[4] != "$3.75" {
			t.Errorf("evidence premium = %q, want $3.75 per session", row[4])
		}
	}
}

func TestCache1hAPIPlanIsHighConfidenceAndNamesTheSetting(t *testing.T) {
	st := newStore(t)
	for _, id := range []string{"c1h1", "c1h2", "c1h3"} {
		ingest(t, st, cache1hFixture(id, 2*time.Minute, 40_000, 5_000, 3))
	}

	in := input(t, st)
	in.Plan = config.PlanAPI
	f, err := cache1hRule{}.Run(in)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if f == nil {
		t.Fatal("expected a finding")
	}
	if f.Confidence != High {
		t.Errorf("Confidence = %q, want %q on PlanAPI", f.Confidence, High)
	}
	if !strings.Contains(f.WhatToChange, "promptCacheTtl") {
		t.Errorf("WhatToChange = %q, want the setting named on PlanAPI", f.WhatToChange)
	}
	assertPlainEnglish(t, *f)
}

func TestCache1hSubscriptionPlanIsLowConfidenceAndSaysNothingToDo(t *testing.T) {
	st := newStore(t)
	for _, id := range []string{"c1h1", "c1h2", "c1h3"} {
		ingest(t, st, cache1hFixture(id, 2*time.Minute, 40_000, 5_000, 3))
	}

	in := input(t, st)
	in.Plan = config.PlanSubscription
	f, err := cache1hRule{}.Run(in)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if f == nil {
		t.Fatal("expected a finding")
	}
	if f.Confidence != Low {
		t.Errorf("Confidence = %q, want %q on PlanSubscription", f.Confidence, Low)
	}
	if !strings.Contains(f.WhatToChange, "not billed per token") {
		t.Errorf("WhatToChange = %q, want the plan explanation", f.WhatToChange)
	}
	if f.WhatToExpect != "There is nothing to act on today." {
		t.Errorf("WhatToExpect = %q, want the fixed sentence", f.WhatToExpect)
	}
	assertPlainEnglish(t, *f)
	t.Logf("Subscription WhatToChange: %s", f.WhatToChange)
	t.Logf("Subscription WhatToExpect: %s", f.WhatToExpect)
}

// A session with a single turn has no gap to judge, so it says nothing
// about which lifetime it needed and must not be counted.
func TestCache1hIgnoresOneTurnSessions(t *testing.T) {
	st := newStore(t)
	for _, id := range []string{"one1", "one2", "one3", "one4"} {
		ingest(t, st, cache1hFixture(id, time.Minute, 1_000_000, 0, 1))
	}
	f, err := cache1hRule{}.Run(input(t, st))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if f != nil {
		t.Fatalf("expected no finding from one-turn sessions, got %q", f.Title)
	}
}
