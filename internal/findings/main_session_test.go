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

// lookupSession builds a main session of n turns that used the given tools, one
// per turn. Turns beyond the tool list call nothing, which is what a question
// and an answer looks like.
func lookupSession(id, modelID string, turns int, tools []string) *model.Transcript {
	tr := &model.Transcript{Session: session(id, "/work/app", start)}
	for i := 0; i < turns; i++ {
		t := model.Turn{
			SessionID: id,
			ID:        fmt.Sprintf("%s-t%d", id, i),
			Timestamp: start.Add(time.Duration(i) * time.Minute),
			Model:     modelID,
			TextChars: 600,
			Usage:     usage(1_000, 5_000, 0, 1_000, 0),
		}
		if i < len(tools) {
			t.ToolCalls = []model.ToolCall{{
				ID: fmt.Sprintf("%s-tc%d", id, i), Name: tools[i], InputChars: 100,
			}}
		}
		tr.Turns = append(tr.Turns, t)
	}
	return tr
}

// lookupFixture ingests n short read-only main sessions on one model.
func lookupFixture(t *testing.T, st *store.Store, prefix, modelID string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		ingest(t, st, lookupSession(fmt.Sprintf("%s%d", prefix, i), modelID, 3, []string{"Read", "Grep"}))
	}
}

func init() {
	// So TestAdviceOnlyNamesKnownSettings scans this rule's prose once the rule
	// is registered in All().
	adviceFixtures = append(adviceFixtures, func(t *testing.T, st *store.Store) {
		lookupFixture(t, st, "mainlookup", "claude-opus-5", mainSessionMinSessions)
	})
}

// assertKnownSettings is the registry guard applied to one finding, so a rule's
// own tests fail on an invented setting name without waiting for the rule to be
// registered in All().
func assertKnownSettings(t *testing.T, f Finding) {
	t.Helper()
	for field, text := range map[string]string{
		"WhatHappened": f.WhatHappened,
		"WhyItCosts":   f.WhyItCosts,
		"WhatToChange": f.WhatToChange,
		"WhatToExpect": f.WhatToExpect,
		"Patch":        f.Patch,
	} {
		for _, m := range settingLike.FindAllStringSubmatch(text, -1) {
			name := firstNonEmpty(m[1:])
			if !settingsRegistry[name] {
				t.Errorf("%s: %s names %q, which is not in settingsRegistry:\n%s",
					f.Title, field, name, text)
			}
		}
	}
}

func TestMainSessionFires(t *testing.T) {
	st := newStore(t)
	lookupFixture(t, st, "m", "claude-opus-5", 3)

	f, err := mainSessionRule{}.Run(input(t, st))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if f == nil {
		t.Fatal("expected a finding for three short read-only opus sessions")
	}
	if f.Direction != Downgrade {
		t.Errorf("Direction = %q, want %q", f.Direction, Downgrade)
	}
	if f.Confidence != Medium {
		t.Errorf("Confidence = %q, want %q", f.Confidence, Medium)
	}
	if f.SavingUSD <= 0 {
		t.Errorf("SavingUSD = %v, want > 0", f.SavingUSD)
	}
	if f.SavingShare <= 0 || f.SavingShare > 1 {
		t.Errorf("SavingShare = %v, want a fraction of the window total", f.SavingShare)
	}
	if !strings.Contains(f.Title, "Opus") {
		t.Errorf("Title = %q, want it to name Opus", f.Title)
	}
	for _, want := range []string{"10 turns or fewer", "only read"} {
		if !strings.Contains(f.WhatHappened, want) {
			t.Errorf("WhatHappened missing %q:\n%s", want, f.WhatHappened)
		}
	}
	if !strings.Contains(f.WhyItCosts, "the rate of Sonnet") {
		t.Errorf("WhyItCosts should price the premium against Sonnet:\n%s", f.WhyItCosts)
	}
	for _, want := range []string{"`/model sonnet`", "press s"} {
		if !strings.Contains(f.WhatToChange, want) {
			t.Errorf("WhatToChange missing %q:\n%s", want, f.WhatToChange)
		}
	}
	if f.Patch != "" {
		t.Errorf("Patch = %q, want none: there is no file behind this finding", f.Patch)
	}
	if got, want := f.Evidence.Columns, []string{"session", "project", "turns", "tools", "cost", "as sonnet"}; len(got) != len(want) {
		t.Errorf("evidence columns = %v, want %v", got, want)
	}
	if got := len(f.Evidence.Rows); got != 3 {
		t.Fatalf("evidence rows = %d, want 3", got)
	}
	if f.Evidence.Rows[0][2] != "3" {
		t.Errorf("evidence turns = %q, want 3", f.Evidence.Rows[0][2])
	}
	if f.Evidence.Rows[0][3] != "Grep and Read" {
		t.Errorf("evidence tools = %q, want the read-only tools", f.Evidence.Rows[0][3])
	}
	assertPlainEnglish(t, *f)
	assertKnownSettings(t, *f)

	t.Logf("main-session-on-strong-model\n\nTITLE: %s\n\nWHAT HAPPENED: %s\n\nWHY IT COSTS: %s\n\n"+
		"WHAT TO CHANGE: %s\n\nWHAT TO EXPECT: %s\n\nEVIDENCE: %v\n%v",
		f.Title, f.WhatHappened, f.WhyItCosts, f.WhatToChange, f.WhatToExpect,
		f.Evidence.Columns, f.Evidence.Rows)
}

func TestMainSessionBelowSessionFloor(t *testing.T) {
	st := newStore(t)
	lookupFixture(t, st, "m", "claude-opus-5", mainSessionMinSessions-1)

	f, err := mainSessionRule{}.Run(input(t, st))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if f != nil {
		t.Fatalf("two qualifying sessions is below the floor of %d, got %q", mainSessionMinSessions, f.Title)
	}
}

func TestMainSessionIgnoresLongerSessions(t *testing.T) {
	st := newStore(t)
	for i := 0; i < 3; i++ {
		ingest(t, st, lookupSession(fmt.Sprintf("long%d", i), "claude-opus-5",
			mainSessionMaxTurns+1, []string{"Read", "Grep"}))
	}

	f, err := mainSessionRule{}.Run(input(t, st))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if f != nil {
		t.Fatalf("expected no finding above %d turns, got %q", mainSessionMaxTurns, f.Title)
	}
}

func TestMainSessionIgnoresSessionsThatEdited(t *testing.T) {
	st := newStore(t)
	// Four short sessions on opus, two of which changed the project. Only two
	// were read-only, which is below the floor.
	lookupFixture(t, st, "ro", "claude-opus-5", 2)
	for i := 0; i < 2; i++ {
		ingest(t, st, lookupSession(fmt.Sprintf("rw%d", i), "claude-opus-5", 3, []string{"Read", "Edit"}))
	}

	f, err := mainSessionRule{}.Run(input(t, st))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if f != nil {
		t.Fatalf("sessions that edited must not count, got %q", f.Title)
	}
}

func TestMainSessionIgnoresSubAgentRuns(t *testing.T) {
	st := newStore(t)
	// Five short read-only opus sub-agent runs: the other rule's territory, and
	// none of them a main session.
	readOnlyFixture(t, st, "p1", "researcher", "claude-opus-5", 5, nil)

	f, err := mainSessionRule{}.Run(input(t, st))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if f != nil {
		t.Fatalf("sub-agent runs are not main sessions, got %q", f.Title)
	}
}

func TestMainSessionSkipsAlreadyCheapModel(t *testing.T) {
	st := newStore(t)
	lookupFixture(t, st, "m", "claude-sonnet-5", 4)

	f, err := mainSessionRule{}.Run(input(t, st))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if f != nil {
		t.Fatalf("expected no finding on sonnet, got %q", f.Title)
	}
}

// The saving is the difference between what these sessions cost and what the
// same token counts would have cost on the cheaper model, and nothing else.
func TestMainSessionSavingMatchesHandCalculation(t *testing.T) {
	st := newStore(t)
	// Three sessions of one turn each: 1,000 uncached input and 1,000 output.
	// Opus: 1,000 x $5 + 1,000 x $25 per million = $0.030 a session.
	// Sonnet: 1,000 x $2 + 1,000 x $10 per million = $0.012 a session.
	for i := 0; i < 3; i++ {
		tr := lookupSession(fmt.Sprintf("calc%d", i), "claude-opus-5", 1, []string{"Read"})
		tr.Turns[0].Usage = usage(1_000, 0, 0, 1_000, 0)
		ingest(t, st, tr)
	}

	f, err := mainSessionRule{}.Run(input(t, st))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if f == nil {
		t.Fatal("expected a finding")
	}
	const want = 3 * (0.030 - 0.012) // the window is 30 days, so this is also the monthly figure
	if math.Abs(f.SavingUSD-want) > 1e-9 {
		t.Errorf("SavingUSD = %v, want %v", f.SavingUSD, want)
	}
	if got := f.Evidence.Rows[0][4]; got != "$0.03" {
		t.Errorf("evidence cost = %q, want $0.03", got)
	}
	if got := f.Evidence.Rows[0][5]; got != "$0.01" {
		t.Errorf("evidence cost on sonnet = %q, want $0.01", got)
	}
	// 40% of what it costs now: $0.012 of every $0.030.
	if !strings.Contains(f.WhatToExpect, "40%") {
		t.Errorf("WhatToExpect = %q, want the 40%% figure", f.WhatToExpect)
	}
}

// Changing the saved default is only the right advice when most of the window's
// sessions are look-ups. Below that mark the per-session switch is the whole of
// the advice, because a default that is wrong half the time is not wrong.
func TestMainSessionDefaultAdviceFlipsAtTheHalfMark(t *testing.T) {
	const settingsAdvice = "~/.claude/settings.json"

	cases := []struct {
		name        string
		qualifying  int
		longer      int
		wantDefault bool
	}{
		{name: "a minority of sessions: session-only", qualifying: 3, longer: 5, wantDefault: false},
		{name: "exactly half: still session-only", qualifying: 4, longer: 4, wantDefault: false},
		{name: "more than half: change the default", qualifying: 5, longer: 3, wantDefault: true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newStore(t)
			lookupFixture(t, st, "short", "claude-opus-5", c.qualifying)
			for i := 0; i < c.longer; i++ {
				ingest(t, st, lookupSession(fmt.Sprintf("work%d", i), "claude-opus-5",
					mainSessionMaxTurns+2, []string{"Read", "Grep"}))
			}

			f, err := mainSessionRule{}.Run(input(t, st))
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			if f == nil {
				t.Fatal("expected a finding")
			}
			if !strings.Contains(f.WhatToChange, "press s") {
				t.Errorf("the per-session switch should always be offered:\n%s", f.WhatToChange)
			}
			if got := strings.Contains(f.WhatToChange, settingsAdvice); got != c.wantDefault {
				t.Errorf("default-branch advice present = %v, want %v (%d of %d sessions qualify):\n%s",
					got, c.wantDefault, c.qualifying, c.qualifying+c.longer, f.WhatToChange)
			}
			assertPlainEnglish(t, *f)
			assertKnownSettings(t, *f)
		})
	}
}

// Two strong models that both move to the same cheaper one share a single
// paragraph of advice; the report once printed it twice, word for word.
func TestMainSessionAdviceIsNotRepeatedPerModel(t *testing.T) {
	groups := []*mainGroup{
		{modelID: "claude-opus-5", alt: "claude-sonnet-5"},
		{modelID: "claude-opus-4-8", alt: "claude-sonnet-5"},
	}
	got := mainSessionWhatToChange(groups, 2, 10)
	if n := strings.Count(got, "This is a habit rather than a file"); n != 1 {
		t.Errorf("advice paragraph appears %d times, want 1:\n%s", n, got)
	}
}
