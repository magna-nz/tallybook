package report

import "testing"

// The phrases are what the reader acts on, so each boundary is pinned.
func TestChangePhrase(t *testing.T) {
	cases := []struct {
		before, after float64
		want          string
	}{
		{0, 0, "no change"},
		{0, 12.5, "new"},
		{12.5, 0, "down 100%"},
		{100, 100, "no change"},
		{100, 100.4, "about the same"},
		{100, 99.6, "about the same"},
		{100, 100.9, "about the same"},
		{100, 101, "up 1%"},
		{100, 101.4, "up 1%"},
		{100, 130, "up 30%"},
		{100, 70, "down 30%"},
		{100, 250, "up 150%"},
		{10, 1000, "up 9,900%"},
		{200, 199, "about the same"},
		{200, 198, "down 1%"},
		{200, 197, "down 2%"},
	}
	for _, c := range cases {
		if got := changePhrase(c.before, c.after); got != c.want {
			t.Errorf("changePhrase(%v, %v) = %q, want %q", c.before, c.after, got, c.want)
		}
	}
}

// A share moves in points, never in percent of a percent.
func TestPointsPhrase(t *testing.T) {
	cases := []struct {
		before, after float64
		want          string
	}{
		{0.84, 0.87, "up 3 points"},
		{0.87, 0.84, "down 3 points"},
		{0.5, 0.504, "about the same"},
		{0, 0, "about the same"},
		{0, 1, "up 100 points"},
		{0.301, 0.296, "about the same"},
		{0.30, 0.31, "up 1 point"},
		{0.31, 0.30, "down 1 point"},
		{0.30, 0.40, "up 10 points"},
	}
	for _, c := range cases {
		if got := pointsPhrase(c.before, c.after); got != c.want {
			t.Errorf("pointsPhrase(%v, %v) = %q, want %q", c.before, c.after, got, c.want)
		}
	}
}

func TestFmtDays(t *testing.T) {
	for days, want := range map[float64]string{1: "1 day", 7: "7 days", 30: "30 days", 29.6: "30 days", 0.4: "0 days"} {
		if got := fmtDays(days); got != want {
			t.Errorf("fmtDays(%v) = %q, want %q", days, got, want)
		}
	}
}
