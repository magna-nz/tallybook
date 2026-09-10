package pricing

import (
	"math"
	"sort"
	"testing"
	"time"

	"github.com/magna-nz/tallybook/internal/model"
)

func almostEqual(a, b float64) bool {
	return math.Abs(a-b) < 1e-9
}

func TestCost(t *testing.T) {
	tab := Default()
	usd, known := tab.Cost("claude-opus-5", model.Usage{
		Input:        2,
		CacheRead:    34538,
		CacheWrite1h: 18179,
		Output:       176,
	})
	if !known {
		t.Fatalf("expected known=true")
	}
	want := (2*5 + 34538*0.5 + 18179*10 + 176*25) / 1e6
	if !almostEqual(usd, want) {
		t.Fatalf("Cost = %v, want %v", usd, want)
	}
}

func TestLookupExactAliasAndCase(t *testing.T) {
	tab := Default()
	want, ok := tab.rateAt("claude-opus-5", time.Time{})
	if !ok {
		t.Fatalf("claude-opus-5 missing from default table")
	}

	for _, id := range []string{"opus", "claude-opus-5-20260401", "CLAUDE-OPUS-5"} {
		got, ok := tab.Lookup(id)
		if !ok {
			t.Fatalf("Lookup(%q) not ok", id)
		}
		if got != want {
			t.Fatalf("Lookup(%q) = %+v, want %+v", id, got, want)
		}
	}
}

func TestLookupCodexFallback(t *testing.T) {
	tab := Default()
	want, ok := tab.rateAt("gpt-5.5", time.Time{})
	if !ok {
		t.Fatalf("gpt-5.5 missing from default table")
	}
	got, ok := tab.Lookup("gpt-5.5-codex")
	if !ok {
		t.Fatalf("Lookup(gpt-5.5-codex) not ok")
	}
	if got != want {
		t.Fatalf("Lookup(gpt-5.5-codex) = %+v, want %+v", got, want)
	}
}

func TestLookupOpenAIDateSuffix(t *testing.T) {
	tab := Default()
	want, ok := tab.rateAt("gpt-5.4-mini", time.Time{})
	if !ok {
		t.Fatalf("gpt-5.4-mini missing from default table")
	}
	got, ok := tab.Lookup("gpt-5.4-mini-2026-03-05")
	if !ok {
		t.Fatalf("Lookup(gpt-5.4-mini-2026-03-05) not ok")
	}
	if got != want {
		t.Fatalf("Lookup(gpt-5.4-mini-2026-03-05) = %+v, want %+v", got, want)
	}
}

func TestLookupUnknown(t *testing.T) {
	tab := Default()
	if _, ok := tab.Lookup("nonsense"); ok {
		t.Fatalf("Lookup(nonsense) unexpectedly ok")
	}
	usd, known := tab.Cost("nonsense", model.Usage{Input: 100})
	if known {
		t.Fatalf("Cost(nonsense) unexpectedly known")
	}
	if usd != 0 {
		t.Fatalf("Cost(nonsense) = %v, want 0", usd)
	}
}

func TestTierAndCheaperAlternative(t *testing.T) {
	cases := []struct {
		model    string
		wantTier string
		wantAlt  string
	}{
		{"claude-opus-5", "opus", "claude-sonnet-5"},
		{"claude-sonnet-5", "sonnet", "claude-haiku-4-5"},
		{"claude-haiku-4-5", "haiku", ""},
		{"gpt-5.5", "gpt", "gpt-5.4-mini"},
		{"gpt-5.4", "gpt", "gpt-5.4-mini"},
		{"gpt-5.4-mini", "gpt-mini", "gpt-5.4-nano"},
		{"gpt-5.4-nano", "gpt-nano", ""},
		{"gpt-5.2-pro", "gpt-pro", "gpt-5.2"},
		{"gpt-6-astra", "gpt-pro", ""},
	}
	for _, c := range cases {
		if got := Tier(c.model); got != c.wantTier {
			t.Errorf("Tier(%q) = %q, want %q", c.model, got, c.wantTier)
		}
		if got := CheaperAlternative(c.model); got != c.wantAlt {
			t.Errorf("CheaperAlternative(%q) = %q, want %q", c.model, got, c.wantAlt)
		}
	}
}

func TestSetOverrides(t *testing.T) {
	tab := Default()
	original, ok := tab.rateAt("claude-opus-5", time.Time{})
	if !ok {
		t.Fatalf("claude-opus-5 missing from default table")
	}
	override := Rate{Input: 1, CacheRead: 1, CacheWrite5m: 1, CacheWrite1h: 1, Output: 1}
	tab.Set("claude-opus-5", override)
	got, ok := tab.rateAt("claude-opus-5", time.Time{})
	if !ok {
		t.Fatalf("claude-opus-5 missing after Set")
	}
	if got != override {
		t.Fatalf("Set did not override: got %+v, original %+v", got, original)
	}
}

func TestModelsSortedAndComplete(t *testing.T) {
	tab := Default()
	models := tab.Models()

	if !sort.StringsAreSorted(models) {
		t.Fatalf("Models() not sorted: %v", models)
	}

	want := []string{
		"claude-fable-5-1", "claude-mythos-5-1", "claude-fable-5", "claude-mythos-5",
		"claude-opus-5", "claude-opus-4-8", "claude-opus-4-7", "claude-opus-4-6", "claude-opus-4-5",
		"claude-opus-4-1", "claude-opus-4",
		"claude-sonnet-5",
		"claude-sonnet-4-6", "claude-sonnet-4-5", "claude-sonnet-4",
		"claude-haiku-4-5", "claude-haiku-3-5",
		"gpt-6-astra", "gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna",
		"gpt-5.5", "gpt-5.5-pro",
		"gpt-5.4", "gpt-5.4-mini", "gpt-5.4-nano", "gpt-5.4-pro",
		"gpt-5.3-codex",
		"gpt-5.2", "gpt-5.2-codex", "gpt-5.2-pro",
		"gpt-5.1", "gpt-5.1-codex", "gpt-5.1-codex-mini",
		"gpt-5", "gpt-5-codex", "gpt-5-mini", "gpt-5-nano", "gpt-5-pro",
	}

	present := make(map[string]bool, len(models))
	for _, m := range models {
		present[m] = true
	}
	for _, id := range want {
		if !present[id] {
			t.Errorf("Models() missing %q", id)
		}
	}
	if len(models) != len(want) {
		t.Errorf("Models() has %d ids, want %d", len(models), len(want))
	}
}

func TestCostAtUsesHistory(t *testing.T) {
	tb := Default()
	change := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	old := Rate{Input: 3, CacheRead: 0.3, CacheWrite5m: 3.75, CacheWrite1h: 6, Output: 15}
	tb.AddHistory("claude-sonnet-5", change, old)
	u := model.Usage{Input: 1_000_000}
	before, ok := tb.CostAt("sonnet", u, change.Add(-time.Hour))
	if !ok || before != 3 {
		t.Fatalf("before change: got %v %v, want 3", before, ok)
	}
	after, _ := tb.CostAt("sonnet", u, change)
	if after != 2 {
		t.Fatalf("at change: got %v, want 2", after)
	}
	today, _ := tb.Cost("sonnet", u)
	if today != 2 {
		t.Fatalf("today: got %v, want 2", today)
	}
	tb.Set("claude-sonnet-5", Rate{Input: 9})
	if c, _ := tb.CostAt("sonnet", u, change.Add(-time.Hour)); c != 9 {
		t.Fatalf("Set should discard history, got %v", c)
	}
}

func TestCanonicalMakesAliasesComparable(t *testing.T) {
	tb := Default()
	a, _ := tb.Canonical("sonnet")
	b, _ := tb.Canonical("claude-sonnet-5")
	if a != b {
		t.Fatalf("alias and id differ: %q vs %q", a, b)
	}
}

func TestSameModel(t *testing.T) {
	tb := Default()
	yes := [][2]string{{"opus", "claude-opus-4-8"}, {"opus", "claude-opus-5"}, {"fable", "claude-fable-5"}, {"sonnet", "claude-sonnet-5"}, {"claude-opus-5-20260401", "claude-opus-5"}, {"CLAUDE-OPUS-5", "claude-opus-5"}}
	no := [][2]string{{"sonnet", "claude-opus-5"}, {"opus", "claude-sonnet-5"}, {"claude-opus-4-8", "claude-opus-5"}, {"", "claude-opus-5"}}
	for _, c := range yes {
		if !tb.SameModel(c[0], c[1]) {
			t.Errorf("SameModel(%q,%q) = false, want true", c[0], c[1])
		}
	}
	for _, c := range no {
		if tb.SameModel(c[0], c[1]) {
			t.Errorf("SameModel(%q,%q) = true, want false", c[0], c[1])
		}
	}
}
