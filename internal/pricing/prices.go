package pricing

// Verified 2026-09-10 against platform.claude.com/docs/en/about-claude/pricing
// and developers.openai.com/api/docs/pricing
//
// All rates are USD per million tokens. Columns for Claude models are
// (input, cache read, 5m cache write, 1h cache write, output). OpenAI does
// not publish a TTL split for cache writes, so CacheWrite5m and
// CacheWrite1h are both set to the base input rate.
var basePrices = map[string]Rate{
	// Claude
	"claude-fable-5-1":  {Input: 10, CacheRead: 0.25, CacheWrite5m: 12.5, CacheWrite1h: 20, Output: 50},
	"claude-mythos-5-1": {Input: 10, CacheRead: 0.25, CacheWrite5m: 12.5, CacheWrite1h: 20, Output: 50},
	"claude-fable-5":    {Input: 10, CacheRead: 1, CacheWrite5m: 12.5, CacheWrite1h: 20, Output: 50},
	"claude-mythos-5":   {Input: 10, CacheRead: 1, CacheWrite5m: 12.5, CacheWrite1h: 20, Output: 50},

	"claude-opus-5":   {Input: 5, CacheRead: 0.5, CacheWrite5m: 6.25, CacheWrite1h: 10, Output: 25},
	"claude-opus-4-8": {Input: 5, CacheRead: 0.5, CacheWrite5m: 6.25, CacheWrite1h: 10, Output: 25},
	"claude-opus-4-7": {Input: 5, CacheRead: 0.5, CacheWrite5m: 6.25, CacheWrite1h: 10, Output: 25},
	"claude-opus-4-6": {Input: 5, CacheRead: 0.5, CacheWrite5m: 6.25, CacheWrite1h: 10, Output: 25},
	"claude-opus-4-5": {Input: 5, CacheRead: 0.5, CacheWrite5m: 6.25, CacheWrite1h: 10, Output: 25},

	"claude-opus-4-1": {Input: 15, CacheRead: 1.5, CacheWrite5m: 18.75, CacheWrite1h: 30, Output: 75},
	"claude-opus-4":   {Input: 15, CacheRead: 1.5, CacheWrite5m: 18.75, CacheWrite1h: 30, Output: 75},

	"claude-sonnet-5": {Input: 2, CacheRead: 0.2, CacheWrite5m: 2.5, CacheWrite1h: 4, Output: 10},

	"claude-sonnet-4-6": {Input: 3, CacheRead: 0.3, CacheWrite5m: 3.75, CacheWrite1h: 6, Output: 15},
	"claude-sonnet-4-5": {Input: 3, CacheRead: 0.3, CacheWrite5m: 3.75, CacheWrite1h: 6, Output: 15},
	"claude-sonnet-4":   {Input: 3, CacheRead: 0.3, CacheWrite5m: 3.75, CacheWrite1h: 6, Output: 15},

	"claude-haiku-4-5": {Input: 1, CacheRead: 0.1, CacheWrite5m: 1.25, CacheWrite1h: 2, Output: 5},
	"claude-haiku-3-5": {Input: 0.8, CacheRead: 0.08, CacheWrite5m: 1, CacheWrite1h: 1.6, Output: 4},

	// OpenAI (no TTL split; CacheWrite5m/CacheWrite1h == Input)
	"gpt-6-astra":   {Input: 10, CacheRead: 1, CacheWrite5m: 10, CacheWrite1h: 10, Output: 50},
	"gpt-5.6-sol":   {Input: 4, CacheRead: 0.4, CacheWrite5m: 4, CacheWrite1h: 4, Output: 20},
	"gpt-5.6-terra": {Input: 2, CacheRead: 0.2, CacheWrite5m: 2, CacheWrite1h: 2, Output: 12},
	"gpt-5.6-luna":  {Input: 0.2, CacheRead: 0.02, CacheWrite5m: 0.2, CacheWrite1h: 0.2, Output: 1.2},

	"gpt-5.5": {Input: 5, CacheRead: 0.5, CacheWrite5m: 5, CacheWrite1h: 5, Output: 30},
	// No cached rate published for gpt-5.5-pro; using the input rate.
	"gpt-5.5-pro": {Input: 30, CacheRead: 30, CacheWrite5m: 30, CacheWrite1h: 30, Output: 180},

	"gpt-5.4":      {Input: 2.5, CacheRead: 0.25, CacheWrite5m: 2.5, CacheWrite1h: 2.5, Output: 15},
	"gpt-5.4-mini": {Input: 0.75, CacheRead: 0.075, CacheWrite5m: 0.75, CacheWrite1h: 0.75, Output: 4.5},
	"gpt-5.4-nano": {Input: 0.2, CacheRead: 0.02, CacheWrite5m: 0.2, CacheWrite1h: 0.2, Output: 1.25},
	"gpt-5.4-pro":  {Input: 30, CacheRead: 30, CacheWrite5m: 30, CacheWrite1h: 30, Output: 180},

	"gpt-5.3-codex": {Input: 1.75, CacheRead: 0.175, CacheWrite5m: 1.75, CacheWrite1h: 1.75, Output: 14},

	"gpt-5.2":       {Input: 1.75, CacheRead: 0.175, CacheWrite5m: 1.75, CacheWrite1h: 1.75, Output: 14},
	"gpt-5.2-codex": {Input: 1.75, CacheRead: 0.175, CacheWrite5m: 1.75, CacheWrite1h: 1.75, Output: 14},
	"gpt-5.2-pro":   {Input: 21, CacheRead: 21, CacheWrite5m: 21, CacheWrite1h: 21, Output: 168},

	"gpt-5.1": {Input: 1.25, CacheRead: 0.125, CacheWrite5m: 1.25, CacheWrite1h: 1.25, Output: 10},
	// assumed equal to gpt-5.1; no separate rate published for the codex variant.
	"gpt-5.1-codex":      {Input: 1.25, CacheRead: 0.125, CacheWrite5m: 1.25, CacheWrite1h: 1.25, Output: 10},
	"gpt-5.1-codex-mini": {Input: 0.25, CacheRead: 0.025, CacheWrite5m: 0.25, CacheWrite1h: 0.25, Output: 2},

	"gpt-5": {Input: 1.25, CacheRead: 0.125, CacheWrite5m: 1.25, CacheWrite1h: 1.25, Output: 10},
	// assumed equal to gpt-5; no separate rate published for the codex variant.
	"gpt-5-codex": {Input: 1.25, CacheRead: 0.125, CacheWrite5m: 1.25, CacheWrite1h: 1.25, Output: 10},
	"gpt-5-mini":  {Input: 0.25, CacheRead: 0.025, CacheWrite5m: 0.25, CacheWrite1h: 0.25, Output: 2},
	"gpt-5-nano":  {Input: 0.05, CacheRead: 0.005, CacheWrite5m: 0.05, CacheWrite1h: 0.05, Output: 0.4},
	"gpt-5-pro":   {Input: 15, CacheRead: 15, CacheWrite5m: 15, CacheWrite1h: 15, Output: 120},
}

// historicalRate says what Model cost at any moment strictly before Until.
// Add a row here when a vendor changes a price, so sessions from before the
// change are valued at what they actually cost. Keep rows sourced: cite the
// announcement in a comment.
type historicalRate struct {
	Model string
	Until string // YYYY-MM-DD, the day the new price took effect
	Rate  Rate
}

// priceHistory is empty as of the verification date: none of the models in
// basePrices changed price between their launch and 2026-09-10. Claude
// Sonnet 5's introductory $2/$10 became its standard price on 2026-09-01
// instead of rising to $3/$15, so no row is needed for it.
var priceHistory = []historicalRate{}
