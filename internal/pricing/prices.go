package pricing

// Verified 2026-09-13 against platform.claude.com/docs/en/about-claude/pricing
// and developers.openai.com/api/docs/pricing
//
// All rates are USD per million tokens. Columns for Claude models are
// (input, cache read, 5m cache write, 1h cache write, output).
//
// OpenAI publishes no TTL split for cache writes, so CacheWrite5m and
// CacheWrite1h always carry the same figure. Which figure changed with the
// generation: through gpt-5.5 no cache-write cost is published at all and
// both fields hold the base input rate, while from gpt-5.6 onward a
// cache-write rate is published at 1.25x input and both fields hold that.
// Codex never reports a 1h write, so the 1h column is inert for OpenAI.
//
// Some models bill a premium tier that the flat columns cannot express, so
// they carry a nested rate instead:
//
//	Fast  - Claude Opus 5 and Opus 4.8 under fast mode, at 2x input and
//	        output. Caching multipliers apply on top of fast pricing, so
//	        the nested rate carries its own cache columns (1.25x and 2x the
//	        fast input rate, and 0.1x for reads). Fast mode is not offered
//	        on Opus 4.7, and Opus 4.6 accepts the flag but bills standard.
//	Long  - the rate once the prompt passes LongContextFrom tokens. Claude
//	        4.6 and later price the whole 1M window at one rate and have
//	        none; Sonnet 4 and 4.5 charge 2x input above 200k. On the
//	        OpenAI side this is per model, not per generation: gpt-5.4,
//	        gpt-5.4-pro, gpt-5.5, gpt-5.5-pro and every 5.6 and 6 model
//	        charge 2x input above 272k, while gpt-5.4-mini and
//	        gpt-5.4-nano publish no long-context rate at all and bill one
//	        tier throughout. Do not infer a tier for a model from its
//	        siblings; check the vendor page.
var basePrices = map[string]Rate{
	// Claude
	"claude-fable-5-1":  {Input: 10, CacheRead: 0.25, CacheWrite5m: 12.5, CacheWrite1h: 20, Output: 50},
	"claude-mythos-5-1": {Input: 10, CacheRead: 0.25, CacheWrite5m: 12.5, CacheWrite1h: 20, Output: 50},
	"claude-fable-5":    {Input: 10, CacheRead: 1, CacheWrite5m: 12.5, CacheWrite1h: 20, Output: 50},
	"claude-mythos-5":   {Input: 10, CacheRead: 1, CacheWrite5m: 12.5, CacheWrite1h: 20, Output: 50},

	"claude-opus-5": {Input: 5, CacheRead: 0.5, CacheWrite5m: 6.25, CacheWrite1h: 10, Output: 25,
		Fast: &Rate{Input: 10, CacheRead: 1, CacheWrite5m: 12.5, CacheWrite1h: 20, Output: 50}},
	"claude-opus-4-8": {Input: 5, CacheRead: 0.5, CacheWrite5m: 6.25, CacheWrite1h: 10, Output: 25,
		Fast: &Rate{Input: 10, CacheRead: 1, CacheWrite5m: 12.5, CacheWrite1h: 20, Output: 50}},
	"claude-opus-4-7": {Input: 5, CacheRead: 0.5, CacheWrite5m: 6.25, CacheWrite1h: 10, Output: 25},
	"claude-opus-4-6": {Input: 5, CacheRead: 0.5, CacheWrite5m: 6.25, CacheWrite1h: 10, Output: 25},
	"claude-opus-4-5": {Input: 5, CacheRead: 0.5, CacheWrite5m: 6.25, CacheWrite1h: 10, Output: 25},

	"claude-opus-4-1": {Input: 15, CacheRead: 1.5, CacheWrite5m: 18.75, CacheWrite1h: 30, Output: 75},
	"claude-opus-4":   {Input: 15, CacheRead: 1.5, CacheWrite5m: 18.75, CacheWrite1h: 30, Output: 75},

	"claude-sonnet-5": {Input: 2, CacheRead: 0.2, CacheWrite5m: 2.5, CacheWrite1h: 4, Output: 10},

	"claude-sonnet-4-6": {Input: 3, CacheRead: 0.3, CacheWrite5m: 3.75, CacheWrite1h: 6, Output: 15},
	"claude-sonnet-4-5": {Input: 3, CacheRead: 0.3, CacheWrite5m: 3.75, CacheWrite1h: 6, Output: 15,
		Long: &Rate{Input: 6, CacheRead: 0.6, CacheWrite5m: 7.5, CacheWrite1h: 12, Output: 22.5}, LongContextFrom: 200_000},
	// The long tier's 1h write is the one derived figure in this file: no
	// 1h-above-200k rate is published for Sonnet 4, so it follows the
	// documented 2x-input multiplier, which is what Sonnet 4.5 publishes.
	"claude-sonnet-4": {Input: 3, CacheRead: 0.3, CacheWrite5m: 3.75, CacheWrite1h: 6, Output: 15,
		Long: &Rate{Input: 6, CacheRead: 0.6, CacheWrite5m: 7.5, CacheWrite1h: 12, Output: 22.5}, LongContextFrom: 200_000},

	"claude-haiku-4-5": {Input: 1, CacheRead: 0.1, CacheWrite5m: 1.25, CacheWrite1h: 2, Output: 5},
	"claude-haiku-3-5": {Input: 0.8, CacheRead: 0.08, CacheWrite5m: 1, CacheWrite1h: 1.6, Output: 4},

	// OpenAI (no TTL split; CacheWrite5m == CacheWrite1h, see the note above)
	"gpt-6-astra": {Input: 10, CacheRead: 1, CacheWrite5m: 12.5, CacheWrite1h: 12.5, Output: 50,
		Long: &Rate{Input: 20, CacheRead: 2, CacheWrite5m: 25, CacheWrite1h: 25, Output: 75}, LongContextFrom: 272_000},
	"gpt-5.6-cyber": {Input: 12.5, CacheRead: 1.25, CacheWrite5m: 15.625, CacheWrite1h: 15.625, Output: 75,
		Long: &Rate{Input: 25, CacheRead: 2.5, CacheWrite5m: 31.25, CacheWrite1h: 31.25, Output: 112.5}, LongContextFrom: 272_000},
	"gpt-5.6-sol": {Input: 4, CacheRead: 0.4, CacheWrite5m: 5, CacheWrite1h: 5, Output: 20,
		Long: &Rate{Input: 8, CacheRead: 0.8, CacheWrite5m: 10, CacheWrite1h: 10, Output: 30}, LongContextFrom: 272_000},
	// The bare id is not listed separately on the pricing page; it carries
	// gpt-5.6-sol's rates. Without a row here it longest-prefix matches
	// gpt-5, which prices it at a third of what it costs.
	"gpt-5.6": {Input: 4, CacheRead: 0.4, CacheWrite5m: 5, CacheWrite1h: 5, Output: 20,
		Long: &Rate{Input: 8, CacheRead: 0.8, CacheWrite5m: 10, CacheWrite1h: 10, Output: 30}, LongContextFrom: 272_000},
	"gpt-5.6-terra": {Input: 2, CacheRead: 0.2, CacheWrite5m: 2.5, CacheWrite1h: 2.5, Output: 12,
		Long: &Rate{Input: 4, CacheRead: 0.4, CacheWrite5m: 5, CacheWrite1h: 5, Output: 18}, LongContextFrom: 272_000},
	"gpt-5.6-luna": {Input: 0.2, CacheRead: 0.02, CacheWrite5m: 0.25, CacheWrite1h: 0.25, Output: 1.2,
		Long: &Rate{Input: 0.4, CacheRead: 0.04, CacheWrite5m: 0.5, CacheWrite1h: 0.5, Output: 1.8}, LongContextFrom: 272_000},

	"gpt-5.5": {Input: 5, CacheRead: 0.5, CacheWrite5m: 5, CacheWrite1h: 5, Output: 30,
		Long: &Rate{Input: 10, CacheRead: 1, CacheWrite5m: 10, CacheWrite1h: 10, Output: 45}, LongContextFrom: 272_000},
	// No cached rate published for gpt-5.5-pro; using the input rate.
	"gpt-5.5-pro": {Input: 30, CacheRead: 30, CacheWrite5m: 30, CacheWrite1h: 30, Output: 180,
		Long: &Rate{Input: 60, CacheRead: 60, CacheWrite5m: 60, CacheWrite1h: 60, Output: 270}, LongContextFrom: 272_000},

	"gpt-5.4": {Input: 2.5, CacheRead: 0.25, CacheWrite5m: 2.5, CacheWrite1h: 2.5, Output: 15,
		Long: &Rate{Input: 5, CacheRead: 0.5, CacheWrite5m: 5, CacheWrite1h: 5, Output: 22.5}, LongContextFrom: 272_000},
	"gpt-5.4-mini": {Input: 0.75, CacheRead: 0.075, CacheWrite5m: 0.75, CacheWrite1h: 0.75, Output: 4.5},
	"gpt-5.4-nano": {Input: 0.2, CacheRead: 0.02, CacheWrite5m: 0.2, CacheWrite1h: 0.2, Output: 1.25},
	"gpt-5.4-pro": {Input: 30, CacheRead: 30, CacheWrite5m: 30, CacheWrite1h: 30, Output: 180,
		Long: &Rate{Input: 60, CacheRead: 60, CacheWrite5m: 60, CacheWrite1h: 60, Output: 270}, LongContextFrom: 272_000},

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
// basePrices changed price between their launch and 2026-09-13. Claude
// Sonnet 5's introductory $2/$10 became its standard price on 2026-09-01
// instead of rising to $3/$15, so no row is needed for it.
var priceHistory = []historicalRate{}
