package pricing

import "strings"

// Tokenizer families. Two models in the same family turn the same text into
// the same number of tokens, so a cost comparison between them is sound. Two
// models in different families do not, and a comparison between them carries
// an error the size of the tokenizer difference.
//
// Verified 2026-09-10 against platform.claude.com/docs/en/about-claude/pricing:
// "Claude 4.7 and later models ... use a newer tokenizer ... approximately 30%
// more tokens for the same text ... Claude Sonnet 4.6 and earlier models use
// the previous tokenizer."
const (
	TokenizerClaudeNew = "claude-4.7" // Opus 4.7 and later, Sonnet 5, Fable, Mythos
	TokenizerClaudeOld = "claude-4.6" // Sonnet 4.6 and earlier, Opus 4.6 and earlier, Haiku
	TokenizerOpenAI    = "openai"     // assumed one family: OpenAI documents no split
	TokenizerUnknown   = ""
)

// NewTokenizerRatio is how many tokens the newer Claude tokenizer produces for
// text the older one measured, as published. The real figure runs from about
// 1.0 to 1.35 depending on the content, so this is only ever used to describe
// the size of an error in words, never to adjust a number.
const NewTokenizerRatio = 1.3

// claudeNewTokenizer lists the exact ids on the tokenizer introduced with
// Opus 4.7. Everything else in the Claude family is on the older one.
var claudeNewTokenizer = map[string]bool{
	"claude-opus-4-7": true, "claude-opus-4-8": true, "claude-opus-5": true,
	"claude-sonnet-5": true,
	"claude-fable-5":  true, "claude-fable-5-1": true,
	"claude-mythos-5": true, "claude-mythos-5-1": true,
	"claude-mythos-preview": true,
}

// Tokenizer returns the tokenizer family of a model id, resolving aliases and
// date suffixes first. An unrecognised id returns TokenizerUnknown.
func Tokenizer(modelID string) string {
	id, ok := Default().Canonical(modelID)
	if !ok {
		id = strings.ToLower(strings.TrimSpace(modelID))
	}
	switch {
	case id == "":
		return TokenizerUnknown
	case strings.HasPrefix(id, "gpt"):
		// OpenAI publishes no per-model tokenizer split inside the gpt-5
		// family, so they are treated as one family until it does.
		return TokenizerOpenAI
	case strings.HasPrefix(id, "claude") || strings.Contains(id, "fable") || strings.Contains(id, "mythos"):
		if claudeNewTokenizer[id] {
			return TokenizerClaudeNew
		}
		return TokenizerClaudeOld
	}
	return TokenizerUnknown
}

// SameTokenizer reports whether two models count tokens the same way, so that
// pricing one model's recorded token counts at the other's rates is sound.
// Two unknown ids are not assumed to match.
func SameTokenizer(a, b string) bool {
	ta, tb := Tokenizer(a), Tokenizer(b)
	return ta != TokenizerUnknown && ta == tb
}

// TokenizerDrift describes what happens to a cost comparison that prices the
// token counts recorded for `from` at the rates of `to`. It returns 0 when the
// two share a tokenizer or either is unknown, +1 when `to` would need more
// tokens for the same text (so the comparison flatters `to` and overstates any
// saving), and -1 when `to` would need fewer (so any saving is understated).
func TokenizerDrift(from, to string) int {
	tf, tt := Tokenizer(from), Tokenizer(to)
	if tf == TokenizerUnknown || tt == TokenizerUnknown || tf == tt {
		return 0
	}
	if tf == TokenizerClaudeOld && tt == TokenizerClaudeNew {
		return 1
	}
	if tf == TokenizerClaudeNew && tt == TokenizerClaudeOld {
		return -1
	}
	return 0 // across vendors there is no published ratio to reason with
}
