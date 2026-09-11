package pricing

import "testing"

func TestTokenizerFamilies(t *testing.T) {
	cases := map[string]string{
		"claude-opus-5":             TokenizerClaudeNew,
		"claude-opus-4-8":           TokenizerClaudeNew,
		"claude-opus-4-7":           TokenizerClaudeNew,
		"claude-sonnet-5":           TokenizerClaudeNew,
		"claude-fable-5-1":          TokenizerClaudeNew,
		"opus":                      TokenizerClaudeNew, // alias resolves to opus-5
		"claude-opus-5-20260401":    TokenizerClaudeNew,
		"claude-opus-4-6":           TokenizerClaudeOld,
		"claude-sonnet-4-6":         TokenizerClaudeOld,
		"claude-haiku-4-5":          TokenizerClaudeOld,
		"claude-haiku-4-5-20251001": TokenizerClaudeOld,
		"gpt-5.5":                   TokenizerOpenAI,
		"gpt-5.4-mini":              TokenizerOpenAI,
		"nonsense":                  TokenizerUnknown,
		"":                          TokenizerUnknown,
	}
	for id, want := range cases {
		if got := Tokenizer(id); got != want {
			t.Errorf("Tokenizer(%q) = %q, want %q", id, got, want)
		}
	}
}

func TestSameTokenizer(t *testing.T) {
	// The pairs CheaperAlternative actually produces.
	if !SameTokenizer("claude-opus-5", CheaperAlternative("claude-opus-5")) {
		t.Error("opus 5 -> sonnet 5 should share a tokenizer")
	}
	if !SameTokenizer("claude-fable-5-1", CheaperAlternative("claude-fable-5-1")) {
		t.Error("fable 5.1 -> opus 5 should share a tokenizer")
	}
	if SameTokenizer("claude-sonnet-5", CheaperAlternative("claude-sonnet-5")) {
		t.Error("sonnet 5 -> haiku 4.5 crosses tokenizers and must not report as the same")
	}
	if SameTokenizer("nonsense", "nonsense") {
		t.Error("two unknown ids must not be assumed to match")
	}
}

func TestTokenizerDrift(t *testing.T) {
	cases := []struct {
		from, to string
		want     int
	}{
		{"claude-opus-5", "claude-sonnet-5", 0},      // both new
		{"claude-sonnet-5", "claude-haiku-4-5", -1},  // new -> old: fewer tokens, saving understated
		{"claude-opus-4-6", "claude-sonnet-5", 1},    // old -> new: more tokens, saving overstated
		{"claude-haiku-4-5", "claude-sonnet-4-6", 0}, // both old
		{"gpt-5.5", "gpt-5.4-mini", 0},               // one assumed family
		{"claude-opus-5", "gpt-5.5", 0},              // across vendors: no published ratio
		{"nonsense", "claude-opus-5", 0},             // unknown
	}
	for _, c := range cases {
		if got := TokenizerDrift(c.from, c.to); got != c.want {
			t.Errorf("TokenizerDrift(%q,%q) = %d, want %d", c.from, c.to, got, c.want)
		}
	}
}
