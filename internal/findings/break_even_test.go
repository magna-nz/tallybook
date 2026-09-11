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

// breakEvenFixture is a plain Claude Code session with a fixed, easy-to-hand-
// calculate cost: 4 turns on sonnet, each priced at (10,000*2 + 2,000*10)/1e6
// = $0.04, for a session total of $0.16.
func breakEvenFixture(id string) *model.Transcript {
	tr := &model.Transcript{Session: session(id, "/work/app", start)}
	for i := 0; i < 4; i++ {
		tr.Turns = append(tr.Turns, model.Turn{
			SessionID: id,
			ID:        fmt.Sprintf("%s-t%d", id, i),
			Timestamp: start.Add(time.Duration(i) * time.Minute),
			Model:     "claude-sonnet-5",
			TextChars: 200,
			Usage:     usage(10_000, 0, 0, 2_000, 0),
		})
	}
	return tr
}

// This rule never fires inside TestAdviceOnlyNamesKnownSettings, because that
// test's Input is built with Plan fixed to PlanAPI. The fixture is still
// registered so the traffic is there if that ever changes; the rule's own
// text is checked against the settings registry directly in
// TestBreakEvenAdviceOnlyNamesKnownSettings below.
func init() {
	adviceFixtures = append(adviceFixtures, func(t *testing.T, st *store.Store) {
		ingest(t, st, breakEvenFixture("be-advice"))
	})
}

func TestBreakEvenFiresWhenUsageBelowPlan(t *testing.T) {
	st := newStore(t)
	ingest(t, st, breakEvenFixture("be1"))

	in := input(t, st)
	in.Plan = config.PlanSubscription
	in.Cfg.PlanPriceUSD = 200

	f, err := breakEvenRule{}.Run(in)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if f == nil {
		t.Fatal("expected a finding when usage is well below the plan price")
	}
	if f.Direction != Config {
		t.Errorf("Direction = %q, want %q", f.Direction, Config)
	}
	if f.Confidence != Low {
		t.Errorf("Confidence = %q, want %q", f.Confidence, Low)
	}
	wantSaving := 200 - in.PerMonth(in.TotalUSD)
	if f.SavingUSD != wantSaving {
		t.Errorf("SavingUSD = %v, want %v", f.SavingUSD, wantSaving)
	}
	if f.SavingShare <= 0 {
		t.Errorf("SavingShare = %v, want > 0", f.SavingShare)
	}
	if !strings.Contains(f.Title, "worth less") {
		t.Errorf("Title = %q, want it to name the shortfall", f.Title)
	}
	if got := len(f.Evidence.Rows); got != 3 {
		t.Fatalf("evidence rows = %d, want 3 (Claude Code, Codex, total)", got)
	}
	if f.Evidence.Rows[0][0] != "Claude Code" || f.Evidence.Rows[1][0] != "Codex" || f.Evidence.Rows[2][0] != "total" {
		t.Errorf("evidence sources = %v %v %v", f.Evidence.Rows[0][0], f.Evidence.Rows[1][0], f.Evidence.Rows[2][0])
	}
	assertPlainEnglish(t, *f)
}

func TestBreakEvenInfoWhenUsagePaysForItself(t *testing.T) {
	st := newStore(t)
	ingest(t, st, breakEvenFixture("be1"))

	in := input(t, st)
	in.Plan = config.PlanSubscription
	in.Cfg.PlanPriceUSD = in.PerMonth(in.TotalUSD) / 2 // plan costs less than usage is worth

	f, err := breakEvenRule{}.Run(in)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if f == nil {
		t.Fatal("expected a finding when usage is at or above the plan price")
	}
	if f.Confidence != Info {
		t.Errorf("Confidence = %q, want %q", f.Confidence, Info)
	}
	if f.SavingUSD != 0 {
		t.Errorf("SavingUSD = %v, want 0 for an Info finding", f.SavingUSD)
	}
	if !strings.Contains(f.Title, "paying for itself") {
		t.Errorf("Title = %q, want it to say the plan pays for itself", f.Title)
	}
	assertPlainEnglish(t, *f)
}

func TestBreakEvenSilentOnAPIPlan(t *testing.T) {
	st := newStore(t)
	ingest(t, st, breakEvenFixture("be1"))

	in := input(t, st)
	in.Plan = config.PlanAPI
	in.Cfg.PlanPriceUSD = 200

	f, err := breakEvenRule{}.Run(in)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if f != nil {
		t.Fatalf("expected no finding on an API plan, got %q", f.Title)
	}
}

func TestBreakEvenSilentOnZeroPlanPrice(t *testing.T) {
	st := newStore(t)
	ingest(t, st, breakEvenFixture("be1"))

	in := input(t, st)
	in.Plan = config.PlanSubscription
	in.Cfg.PlanPriceUSD = 0

	f, err := breakEvenRule{}.Run(in)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if f != nil {
		t.Fatalf("expected no finding with plan_price unset, got %q", f.Title)
	}
}

func TestBreakEvenSilentOnShortWindow(t *testing.T) {
	st := newStore(t)
	ingest(t, st, breakEvenFixture("be1"))

	in := input(t, st)
	in.Plan = config.PlanSubscription
	in.Cfg.PlanPriceUSD = 200
	in.WindowDays = 7

	f, err := breakEvenRule{}.Run(in)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if f != nil {
		t.Fatalf("expected no finding on a 7-day window, got %q", f.Title)
	}
}

// The advice fixture helper in settings_test.go builds Input with Plan fixed
// to PlanAPI, so this rule never fires inside TestAdviceOnlyNamesKnownSettings.
// This test runs the same detector directly against this rule's own output.
func TestBreakEvenAdviceOnlyNamesKnownSettings(t *testing.T) {
	st := newStore(t)
	ingest(t, st, breakEvenFixture("be1"))

	for _, plan := range []float64{200, 0.01} {
		in := input(t, st)
		in.Plan = config.PlanSubscription
		in.Cfg.PlanPriceUSD = plan

		f, err := breakEvenRule{}.Run(in)
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
}
