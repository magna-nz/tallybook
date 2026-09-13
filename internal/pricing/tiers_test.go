package pricing

import (
	"testing"
	"time"

	"github.com/magna-nz/tallybook/internal/model"
)

// turnAt builds a turn with a given prompt size and speed. Output is kept
// separate from the prompt so a test can prove which one the long-context
// threshold reads.
func turnAt(modelID, speed string, promptTokens, outputTokens int64) model.Turn {
	return model.Turn{
		Model:     modelID,
		Speed:     speed,
		Timestamp: time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC),
		Usage:     model.Usage{Input: promptTokens, Output: outputTokens},
	}
}

func TestForSelectsFastTier(t *testing.T) {
	tab := Default()
	base, ok := tab.Lookup("claude-opus-5")
	if !ok {
		t.Fatal("claude-opus-5 missing")
	}
	fast := base.For("fast", 1000)
	if fast.Input != 2*base.Input || fast.Output != 2*base.Output {
		t.Errorf("fast tier = in %v out %v, want double of in %v out %v",
			fast.Input, fast.Output, base.Input, base.Output)
	}
	// Caching multipliers apply on top of fast pricing: 1.25x, 2x, 0.1x.
	if fast.CacheWrite5m != 1.25*fast.Input {
		t.Errorf("fast 5m write = %v, want %v", fast.CacheWrite5m, 1.25*fast.Input)
	}
	if fast.CacheWrite1h != 2*fast.Input {
		t.Errorf("fast 1h write = %v, want %v", fast.CacheWrite1h, 2*fast.Input)
	}
	if fast.CacheRead != 0.1*fast.Input {
		t.Errorf("fast cache read = %v, want %v", fast.CacheRead, 0.1*fast.Input)
	}
}

func TestForIgnoresSpeedWhenModelHasNoFastTier(t *testing.T) {
	tab := Default()
	// Fast mode is not offered on Opus 4.7, and 4.6 bills standard even
	// when asked for. Neither may quietly pick up a premium.
	for _, id := range []string{"claude-opus-4-7", "claude-opus-4-6", "claude-sonnet-5", "gpt-6-astra"} {
		base, ok := tab.Lookup(id)
		if !ok {
			t.Fatalf("%s missing", id)
		}
		if got := base.For("fast", 1000); got.Input != base.Input || got.Output != base.Output {
			t.Errorf("%s with speed=fast changed rate: %v -> %v", id, base, got)
		}
	}
}

func TestForSpeedMatchingIsLenient(t *testing.T) {
	base, _ := Default().Lookup("claude-opus-5")
	for _, speed := range []string{"fast", "Fast", "FAST", " fast "} {
		if got := base.For(speed, 1000); got.Input != 10 {
			t.Errorf("speed %q did not select the fast tier (input %v)", speed, got.Input)
		}
	}
	for _, speed := range []string{"", "standard", "Standard", "turbo"} {
		if got := base.For(speed, 1000); got.Input != base.Input {
			t.Errorf("speed %q should bill standard, got input %v", speed, got.Input)
		}
	}
}

func TestForLongContextThresholdIsExclusive(t *testing.T) {
	base, ok := Default().Lookup("gpt-6-astra")
	if !ok {
		t.Fatal("gpt-6-astra missing")
	}
	if base.LongContextFrom != 272_000 {
		t.Fatalf("threshold = %d, want 272000", base.LongContextFrom)
	}
	if got := base.For("", 272_000); got.Input != base.Input {
		t.Errorf("exactly at the threshold should bill standard, got %v", got.Input)
	}
	if got := base.For("", 272_001); got.Input != 20 {
		t.Errorf("past the threshold should bill long, got %v", got.Input)
	}
}

// The vendor prices the long tier on the size of the prompt, so a turn that
// merely produced a lot of output must not be pushed over the line.
func TestLongContextReadsPromptNotOutput(t *testing.T) {
	tab := Default()
	huge := turnAt("gpt-6-astra", "", 1000, 500_000)
	r, ok := tab.RateForTurn("gpt-6-astra", huge)
	if !ok {
		t.Fatal("rate not found")
	}
	if r.Input != 10 {
		t.Errorf("large output pushed the turn into the long tier: input %v, want 10", r.Input)
	}
}

func TestRateForTurnAppliesTiers(t *testing.T) {
	tab := Default()
	cases := []struct {
		name      string
		turn      model.Turn
		wantInput float64
	}{
		{"opus-5 standard", turnAt("claude-opus-5", "standard", 1000, 10), 5},
		{"opus-5 fast", turnAt("claude-opus-5", "fast", 1000, 10), 10},
		{"astra short", turnAt("gpt-6-astra", "", 1000, 10), 10},
		{"astra long", turnAt("gpt-6-astra", "", 300_000, 10), 20},
		{"sonnet-4-5 under 200k", turnAt("claude-sonnet-4-5", "", 100_000, 10), 3},
		{"sonnet-4-5 over 200k", turnAt("claude-sonnet-4-5", "", 250_000, 10), 6},
		{"sonnet-4-6 has no long tier", turnAt("claude-sonnet-4-6", "", 900_000, 10), 3},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, ok := tab.RateForTurn(c.turn.Model, c.turn)
			if !ok {
				t.Fatalf("rate not found for %s", c.turn.Model)
			}
			if r.Input != c.wantInput {
				t.Errorf("input = %v, want %v", r.Input, c.wantInput)
			}
		})
	}
}

func TestCostTurnPricesFastAtDouble(t *testing.T) {
	tab := Default()
	std := turnAt("claude-opus-5", "standard", 1_000_000, 1_000_000)
	fast := turnAt("claude-opus-5", "fast", 1_000_000, 1_000_000)

	stdUSD, ok := tab.CostTurn(std)
	if !ok {
		t.Fatal("standard turn not priced")
	}
	fastUSD, ok := tab.CostTurn(fast)
	if !ok {
		t.Fatal("fast turn not priced")
	}
	if want := 5.0 + 25.0; stdUSD != want {
		t.Errorf("standard = %v, want %v", stdUSD, want)
	}
	if want := 10.0 + 50.0; fastUSD != want {
		t.Errorf("fast = %v, want %v", fastUSD, want)
	}
}

// A turn on a model the table does not know must stay unpriced rather than
// pick up a tier from somewhere.
func TestCostTurnUnknownModel(t *testing.T) {
	usd, known := Default().CostTurn(turnAt("no-such-model", "fast", 1000, 10))
	if known || usd != 0 {
		t.Errorf("unknown model priced: usd=%v known=%v", usd, known)
	}
}

// Tiers must not disturb effective dating: a historical rate still wins on
// the day it applied, and the tier is selected from that rate.
func TestTiersRespectPriceHistory(t *testing.T) {
	tab := Default()
	change := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	tab.AddHistory("claude-opus-5", change, Rate{
		Input: 1, CacheRead: 0.1, CacheWrite5m: 1.25, CacheWrite1h: 2, Output: 4,
		Fast: &Rate{Input: 2, CacheRead: 0.2, CacheWrite5m: 2.5, CacheWrite1h: 4, Output: 8},
	})
	old := model.Turn{
		Model: "claude-opus-5", Speed: "fast",
		Timestamp: change.Add(-24 * time.Hour),
		Usage:     model.Usage{Input: 1_000_000},
	}
	r, ok := tab.RateForTurn(old.Model, old)
	if !ok {
		t.Fatal("rate not found")
	}
	if r.Input != 2 {
		t.Errorf("historical fast input = %v, want 2", r.Input)
	}
}

// The two tier fields have to agree: For() checks LongContextFrom > 0 as
// well as Long != nil, so a Long rate with no threshold is a tier that
// silently never applies, and a threshold with no rate is dead weight.
// Neither is a compile error, and 14 of these were hand-written at once.
func TestTierFieldsAreConsistent(t *testing.T) {
	tab := Default()
	for _, id := range tab.Models() {
		r, ok := tab.Lookup(id)
		if !ok {
			t.Fatalf("%s: in Models() but not Lookup()", id)
		}
		if (r.Long != nil) != (r.LongContextFrom > 0) {
			t.Errorf("%s: Long=%v but LongContextFrom=%d; the two must be set together",
				id, r.Long != nil, r.LongContextFrom)
		}
		// A tier that is not a premium is either a typo or pointless, and
		// either way it would price turns wrongly rather than harmlessly.
		if r.Long != nil && r.Long.Input < r.Input {
			t.Errorf("%s: long input %v is cheaper than standard %v", id, r.Long.Input, r.Input)
		}
		if r.Fast != nil && r.Fast.Input < r.Input {
			t.Errorf("%s: fast input %v is cheaper than standard %v", id, r.Fast.Input, r.Input)
		}
		// Nested tiers must not nest further: For() returns *r.Fast or
		// *r.Long directly and would silently ignore a deeper tier.
		for name, tier := range map[string]*Rate{"Fast": r.Fast, "Long": r.Long} {
			if tier == nil {
				continue
			}
			if tier.Fast != nil || tier.Long != nil {
				t.Errorf("%s: %s tier carries its own nested tier, which For() never reads", id, name)
			}
			if tier.Input <= 0 || tier.Output <= 0 {
				t.Errorf("%s: %s tier has a zero column (in %v out %v)", id, name, tier.Input, tier.Output)
			}
		}
	}
}

// Every model that publishes a fast tier must be one the vendor actually
// offers it on. Anthropic offers it on Opus 5 and 4.8 only; picking it up
// anywhere else would double that model's bill.
func TestFastTierOnlyWhereOffered(t *testing.T) {
	tab := Default()
	want := map[string]bool{"claude-opus-5": true, "claude-opus-4-8": true}
	for _, id := range tab.Models() {
		r, _ := tab.Lookup(id)
		if (r.Fast != nil) != want[id] {
			t.Errorf("%s: has fast tier = %v, want %v", id, r.Fast != nil, want[id])
		}
	}
}

// For() must hand back a rate that cannot be tiered again. When it returned
// the receiver intact, a caller could apply a second tier to an already
// tiered rate and double a premium twice over.
func TestForReturnsAFlatRate(t *testing.T) {
	base, _ := Default().Lookup("claude-opus-5")

	std := base.For("standard", 1000)
	if std.Fast != nil || std.Long != nil || std.LongContextFrom != 0 {
		t.Errorf("standard tier still carries tier fields: %+v", std)
	}
	if got := std.For("fast", 1000); got.Input != std.Input {
		t.Errorf("a rate from For() could be tiered again: %v -> %v", std.Input, got.Input)
	}

	fast := base.For("fast", 1000)
	if fast.Fast != nil || fast.Long != nil {
		t.Errorf("fast tier still carries tier fields: %+v", fast)
	}

	long, _ := Default().Lookup("gpt-6-astra")
	lt := long.For("", 400_000)
	if lt.Long != nil || lt.LongContextFrom != 0 {
		t.Errorf("long tier still carries tier fields: %+v", lt)
	}
}

// Tables must not share tier pointers with the package-level basePrices.
// They did, so one write through any Rate reachable from a table silently
// repriced that model for every table in the process, forever.
func TestTablesDoNotShareTierPointers(t *testing.T) {
	t1, t2 := Default(), Default()
	r1, _ := t1.Lookup("claude-opus-5")
	r2, _ := t2.Lookup("claude-opus-5")
	if r1.Fast == nil || r2.Fast == nil {
		t.Fatal("claude-opus-5 lost its fast tier")
	}
	if r1.Fast == r2.Fast {
		t.Fatal("two independent tables share the same *Rate for the fast tier")
	}

	r1.Fast.Input = 999
	fresh, _ := Default().Lookup("claude-opus-5")
	if fresh.Fast.Input != 10 {
		t.Errorf("writing through one table's tier changed the built-in table: input = %v, want 10", fresh.Fast.Input)
	}
	// Same check for a long tier.
	l1, _ := Default().Lookup("gpt-6-astra")
	l2, _ := Default().Lookup("gpt-6-astra")
	if l1.Long == l2.Long {
		t.Error("two independent tables share the same *Rate for the long tier")
	}
}

// gpt-5.6-cyber costs more than the flagship astra, so classifying it as
// mid-tier offered gpt-5.4-mini (17x cheaper, two generations back) as its
// one-step-down alternative.
func TestCyberIsTopTier(t *testing.T) {
	if got := Tier("gpt-5.6-cyber"); got != "gpt-pro" {
		t.Errorf("Tier(gpt-5.6-cyber) = %q, want %q", got, "gpt-pro")
	}
	if got := CheaperAlternative("gpt-5.6-cyber"); got != "" {
		t.Errorf("CheaperAlternative(gpt-5.6-cyber) = %q, want \"\" (same as astra)", got)
	}
	tab := Default()
	cyber, _ := tab.Lookup("gpt-5.6-cyber")
	astra, _ := tab.Lookup("gpt-6-astra")
	if cyber.Input <= astra.Input {
		t.Fatalf("premise changed: cyber %v is no longer dearer than astra %v", cyber.Input, astra.Input)
	}
}

// gpt-5.4-mini and gpt-5.4-nano publish no long-context rate, unlike every
// other model from gpt-5.4 onward. A reviewer read the table's own comment
// and proposed inventing one; this pins the vendor's actual shape.
func TestSmallModelsHaveNoLongTier(t *testing.T) {
	tab := Default()
	for _, id := range []string{"gpt-5.4-mini", "gpt-5.4-nano"} {
		r, ok := tab.Lookup(id)
		if !ok {
			t.Fatalf("%s missing", id)
		}
		if r.Long != nil {
			t.Errorf("%s has a long tier; the vendor publishes none for it", id)
		}
	}
	for _, id := range []string{"gpt-5.4", "gpt-5.4-pro", "gpt-5.5", "gpt-6-astra"} {
		r, _ := tab.Lookup(id)
		if r.Long == nil {
			t.Errorf("%s lost its long tier", id)
		}
	}
}
