package ledger_test

import (
	"math"
	"testing"
	"time"

	"github.com/magna-nz/tallybook/internal/ledger"
	"github.com/magna-nz/tallybook/internal/model"
)

func TestCacheHitRate(t *testing.T) {
	cases := []struct {
		name  string
		usage model.Usage
		want  float64
	}{
		{"nothing sent", model.Usage{}, 0},
		{"output only is not input", model.Usage{Output: 500, Thinking: 100}, 0},
		{"all fresh input", model.Usage{Input: 1000}, 0},
		{"all cache reads", model.Usage{CacheRead: 1000}, 1},
		// 800 read out of 800 + 100 + 50 + 50 sent.
		{"mixed", model.Usage{Input: 100, CacheRead: 800, CacheWrite5m: 50, CacheWrite1h: 50}, 0.8},
		// Both write lifetimes count as sent, and so does fresh input.
		{"writes only", model.Usage{Input: 200, CacheWrite5m: 300, CacheWrite1h: 500}, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ledger.Totals{Usage: c.usage}.CacheHitRate()
			if math.Abs(got-c.want) > 1e-9 {
				t.Errorf("CacheHitRate() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestPriorOfARelativeWindow(t *testing.T) {
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	w, err := ledger.ParseSince("30d", now)
	if err != nil {
		t.Fatal(err)
	}
	prior, ok := ledger.Prior(w)
	if !ok {
		t.Fatal("Prior of a 30d window should exist")
	}
	if !prior.Until.Equal(w.Since) {
		t.Errorf("prior.Until = %v, want this window's Since %v", prior.Until, w.Since)
	}
	if want := w.Since.AddDate(0, 0, -30); !prior.Since.Equal(want) {
		t.Errorf("prior.Since = %v, want %v", prior.Since, want)
	}
	if math.Abs(prior.Days-30) > 1e-9 {
		t.Errorf("prior.Days = %v, want 30", prior.Days)
	}
	if prior.Label != "Jul 12 – Aug 11" {
		t.Errorf("prior.Label = %q, want %q", prior.Label, "Jul 12 – Aug 11")
	}
	// The two windows tile: nothing falls between them and they do not overlap.
	if prior.Until.After(w.Since) || prior.Until.Before(w.Since) {
		t.Errorf("windows do not tile: prior ends %v, this starts %v", prior.Until, w.Since)
	}
}

func TestPriorOfADateWindowHasTheSameLength(t *testing.T) {
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	w, err := ledger.ParseSince("2026-09-01", now)
	if err != nil {
		t.Fatal(err)
	}
	prior, ok := ledger.Prior(w)
	if !ok {
		t.Fatal("Prior of a dated window should exist")
	}
	if got, want := prior.Until.Sub(prior.Since), w.Until.Sub(w.Since); got != want {
		t.Errorf("prior spans %v, this window spans %v", got, want)
	}
	if math.Abs(prior.Days-w.Days) > 1e-9 {
		t.Errorf("prior.Days = %v, want %v", prior.Days, w.Days)
	}
}

func TestPriorOfAllTimeDoesNotExist(t *testing.T) {
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	w, err := ledger.ParseSince("all", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := ledger.Prior(w); ok {
		t.Error("an all-time window has nothing before it; Prior should say so")
	}
	// A window whose start is not before its end is equally empty.
	if _, ok := ledger.Prior(ledger.Window{Since: now, Until: now}); ok {
		t.Error("a zero-length window should have no prior")
	}
}
