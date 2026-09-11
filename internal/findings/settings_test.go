package findings

import (
	"regexp"
	"testing"
	"time"
)

// settingLike matches the shapes a configuration name takes in our advice: a
// quoted camelCase key, or a SCREAMING_SNAKE environment variable. A bare
// lower-case word is not matched, because prose is full of those.
// The shapes a configuration name takes in our advice:
//   - a quoted or backticked camelCase key            "promptCacheTtl"  `effortLevel`
//   - a SCREAMING_SNAKE environment variable          CLAUDE_CODE_CACHE_TTL
//   - a frontmatter or YAML key in a pasted snippet   model: sonnet
//   - a backticked slash command                      `/effort`
//
// Slash commands must be backticked to be recognised, which is how the advice
// writes them, and which keeps an absolute path like /var/folders from being
// mistaken for one.
//
// A guard that only knew the first shape examined two names across six rules,
// which is how an invented name would have slipped through a second time.
var settingLike = regexp.MustCompile(
	"[\"`]([a-z][a-zA-Z0-9]*[A-Z][a-zA-Z0-9]*)[\"`]" + // quoted or backticked camelCase
		"|\\b([A-Z][A-Z0-9]*_[A-Z0-9_]+)\\b" + // SCREAMING_SNAKE
		"|(?m)^\\s{2,}([a-zA-Z][a-zA-Z0-9]*):\\s" + // an indented key: value line
		"|`(/[a-z][a-z0-9-]*)[ `]") // a backticked slash command

// Advice that names a setting must name one that exists. A made-up name once
// shipped to users; this is the guard that stops the next one.
func TestAdviceOnlyNamesKnownSettings(t *testing.T) {
	st := newStore(t)

	// Enough traffic of each shape that every rule has something to say.
	readOnlyFixture(t, st, "p1", "researcher", "claude-opus-5", 6, nil)
	// requested-model-not-honoured only fires when a launch asked for a model,
	// which readOnlyFixture does not do. Without this the rule that owns the
	// precedence sentence was never scanned at all.
	ingest(t, st, parentWithLaunches("req", "reviewer", "sonnet", "claude-opus-5", 3))
	for i := 0; i < 3; i++ {
		ingest(t, st, agentRun("req", i, "claude-opus-5", []string{"Read"}))
	}
	ingest(t, st, pausedSession("cache1", 40*time.Minute))
	ingest(t, st, pausedSession("cache2", 90*time.Minute))
	ingest(t, st, pausedSession("cache3", 30*time.Minute))
	ingest(t, st, bloatedFixture("bloat1"))
	ingest(t, st, retryFixture("retry1"))
	relayFixture(t, st, 30)

	in := input(t, st)
	in.Agents = agents(t, "researcher:", "reviewer:")

	found, err := Run(in)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(found) == 0 {
		t.Fatal("no findings produced; this test cannot check anything")
	}

	for _, f := range found {
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
					t.Errorf("%s.%s names %q, which is not in settingsRegistry.\n"+
						"Either it does not exist and the advice is wrong, or it does and it belongs in "+
						"settings.go with the doc URL and the date you checked it.\n\n%s",
						f.ID, field, name, text)
				}
			}
		}
	}
}

// The name that shipped and should never come back.
func TestTheInventedSettingNameIsGone(t *testing.T) {
	if settingsRegistry["CLAUDE_CODE_CACHE_TTL"] {
		t.Fatal("CLAUDE_CODE_CACHE_TTL does not exist and must never be registered")
	}
}

// The guard above is only worth having if it fires. This checks the detector
// itself: the shapes a configuration name takes must be caught, and ordinary
// prose must not be.
func TestSettingDetectorCatchesInventions(t *testing.T) {
	caught := func(text string) []string {
		var out []string
		for _, m := range settingLike.FindAllStringSubmatch(text, -1) {
			name := firstNonEmpty(m[1:])
			if !settingsRegistry[name] {
				out = append(out, name)
			}
		}
		return out
	}

	invented := []string{
		`Add "madeUpKeyName" to your settings.`,
		`Set CLAUDE_CODE_NOT_REAL=1 in your environment.`,
		`Put "cacheTtlThing" in the file.`,
		// The exact shape that shipped:
		`"env": { "CLAUDE_CODE_CACHE_TTL": "1h" }`,
		// The shapes a narrower detector missed, which is how a second
		// invented name would have reached a release:
		"In the block at the top add:\n\n    thinkingBudget: low\n",
		"`effortLevelXYZ` in ~/.claude/settings.json sets the default.",
		"Run `/thinkybudget low` to set it for the session.",
	}
	for _, text := range invented {
		if got := caught(text); len(got) == 0 {
			t.Errorf("detector missed an invented setting in %q", text)
		}
	}

	fine := []string{
		`Open .claude/agents/researcher.md and add model: sonnet.`,
		"Add this line to the block at the top:\n\n    model: sonnet\n",
		"In that file add:\n\n    effort: low\n",
		"Written to /var/folders/m2/T/tallybook/sessions.log for later.",
		`Add "promptCacheTtl": "1h" to ~/.claude/settings.json.`,
		`For the main session, ` + "`/effort low`" + ` sets it for the rest of the session.`,
		`Opus is billed at about 2.5x the rate of Sonnet for the same tokens.`,
		`In these sessions it happened during pauses of 13 to 2,080 minutes.`,
	}
	for _, text := range fine {
		if got := caught(text); len(got) > 0 {
			t.Errorf("detector flagged %v in ordinary advice: %q", got, text)
		}
	}
}

// firstNonEmpty returns the first capture group that matched.
func firstNonEmpty(groups []string) string {
	for _, g := range groups {
		if g != "" {
			return g
		}
	}
	return ""
}
