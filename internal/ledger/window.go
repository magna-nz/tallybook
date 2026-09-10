// Package ledger provides costed views over the store: sessions, totals and
// per-agent rollups priced with a pricing.Table. It is what report and
// findings build on; it never touches the terminal or the config file.
package ledger

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/magna-nz/tallybook/internal/store"
)

// Window is a reporting time range.
type Window struct {
	Since, Until time.Time
	Days         float64
	Label        string // e.g. "Aug 11 – Sep 10"
}

// ParseSince parses a --since value: "7d", "30d", "90d", "all", or a
// YYYY-MM-DD date. now is the reference point for relative windows and for
// Until. "all" produces a zero Since and a Days of 0; callers that want an
// accurate Days for "all" should look up the earliest session with AllSince
// and adjust the Window themselves.
func ParseSince(s string, now time.Time) (Window, error) {
	switch s {
	case "7d", "30d", "90d":
		n, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
		if err != nil {
			return Window{}, fmt.Errorf("ledger: invalid --since %q", s)
		}
		since := now.AddDate(0, 0, -n)
		return Window{Since: since, Until: now, Days: float64(n), Label: label(since, now)}, nil
	case "all":
		return Window{Since: time.Time{}, Until: now, Days: 0, Label: "all time"}, nil
	}

	t, err := time.ParseInLocation("2006-01-02", s, now.Location())
	if err != nil {
		return Window{}, fmt.Errorf("ledger: invalid --since %q: want 7d, 30d, 90d, all, or YYYY-MM-DD", s)
	}
	return Window{Since: t, Until: now, Days: now.Sub(t).Hours() / 24, Label: label(t, now)}, nil
}

// label formats a date range like "Aug 11 – Sep 10".
func label(since, until time.Time) string {
	return since.Format("Jan 2") + " – " + until.Format("Jan 2")
}

// AllSince returns the StartedAt of the earliest session in the store, or
// the zero time if the store has no sessions. It is used to turn a "all"
// window into a concrete span once the store is available.
func AllSince(st *store.Store) time.Time {
	rows, err := st.Sessions(store.Filter{})
	if err != nil {
		return time.Time{}
	}
	var earliest time.Time
	for _, r := range rows {
		if r.StartedAt.IsZero() {
			continue
		}
		if earliest.IsZero() || r.StartedAt.Before(earliest) {
			earliest = r.StartedAt
		}
	}
	return earliest
}
