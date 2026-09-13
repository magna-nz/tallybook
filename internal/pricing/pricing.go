// Package pricing turns raw token counts into USD using a dated price
// table. It never invents a price: every rate lives in prices.go, sourced
// from the vendor pricing pages.
//
// Rates are effective-dated. The base table holds today's prices; the
// history table records what a model cost before a given date. CostAt
// prices a turn at the moment it happened, so old sessions are valued at
// the price in force then, not the price today.
package pricing

import (
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/magna-nz/tallybook/internal/model"
)

// datedVersion is the date the built-in table was last checked against the
// vendor pricing pages.
const datedVersion = "2026-09-13"

// Rate is USD per million tokens.
//
// A model may bill more than one tier. Fast and Long hold those tiers as
// whole rates rather than multipliers, because vendors publish them as
// figures and a multiplier would invent precision the pricing page does not
// have. Both are nil for a model that bills one tier across the board.
type Rate struct {
	Input, CacheRead, CacheWrite5m, CacheWrite1h, Output float64

	// Fast is what the model bills when the harness ran the turn in fast
	// mode. Caching multipliers apply on top of fast pricing, so this is a
	// full rate, not just input and output.
	Fast *Rate
	// Long is what the model bills once the prompt is larger than
	// LongContextFrom tokens.
	Long *Rate
	// LongContextFrom is the prompt size above which Long applies. It is
	// zero exactly when Long is nil.
	LongContextFrom int64
}

// For returns the flat rate that actually applies to a turn, given the
// harness's speed setting and the size of the prompt.
//
// The tiers do not stack, and today they cannot collide: Anthropic
// publishes a fast tier and prices its full context window at one rate,
// OpenAI publishes a long-context tier and no fast mode. Anthropic is
// explicit that fast pricing covers the whole window, so fast wins if a
// model ever carries both.
func (r Rate) For(speed string, contextTokens int64) Rate {
	switch {
	case r.Fast != nil && strings.EqualFold(strings.TrimSpace(speed), "fast"):
		return (*r.Fast).flat()
	case r.Long != nil && r.LongContextFrom > 0 && contextTokens > r.LongContextFrom:
		return (*r.Long).flat()
	}
	return r.flat()
}

// flat strips the tier fields, so the result is the single rate it claims to
// be: it cannot be tiered a second time, and it shares no pointer with the
// table it came from.
func (r Rate) flat() Rate {
	r.Fast, r.Long, r.LongContextFrom = nil, nil, 0
	return r
}

// clone returns a Rate whose tiers are its own. Default() uses it so that a
// Table never shares tier pointers with the package-level basePrices, where
// one stray write would reprice every model for the rest of the process.
func (r Rate) clone() Rate {
	if r.Fast != nil {
		f := *r.Fast
		r.Fast = &f
	}
	if r.Long != nil {
		l := *r.Long
		r.Long = &l
	}
	return r
}

// pastRate is a rate that applied strictly before Until.
type pastRate struct {
	until time.Time
	rate  Rate
}

// Table is a dated set of rates keyed by exact model id, plus a handful of
// short aliases for the current flagship models.
type Table struct {
	dated   string
	rates   map[string]Rate       // current rate per exact id
	history map[string][]pastRate // earlier rates, sorted by until ascending
	aliases map[string]string
}

// defaultAliases maps a short, human-friendly name to the exact model id it
// currently resolves to.
var defaultAliases = map[string]string{
	"opus":   "claude-opus-5",
	"sonnet": "claude-sonnet-5",
	"haiku":  "claude-haiku-4-5",
	"fable":  "claude-fable-5-1",
}

// Default returns the built-in table.
func Default() *Table {
	t := &Table{
		dated:   datedVersion,
		rates:   make(map[string]Rate, len(basePrices)),
		history: make(map[string][]pastRate),
		aliases: make(map[string]string, len(defaultAliases)),
	}
	for id, r := range basePrices {
		t.rates[strings.ToLower(id)] = r.clone()
	}
	for _, h := range priceHistory {
		until, err := time.Parse("2006-01-02", h.Until)
		if err != nil {
			panic("pricing: bad history date " + h.Until)
		}
		t.AddHistory(h.Model, until, h.Rate)
	}
	for alias, id := range defaultAliases {
		t.aliases[alias] = id
	}
	return t
}

// Dated returns the date this table's rates were last verified.
func (t *Table) Dated() string {
	return t.dated
}

// Set adds or overrides the current rate for an exact model id (used by
// config overrides). It also discards any history for that id, because an
// override is a statement about what the user pays, not about the past.
//
// The same reasoning drops the model's Fast and Long tiers unless the
// caller supplies its own: a flat override says the user pays one rate.
// Config can only express a flat rate today, so overriding a model that
// has a tier (Opus 5's fast mode, say) prices every turn at the override,
// premium turns included.
//
// model is matched by exact id, lower-cased, not through Canonical: an
// override keyed on an alias such as "opus" adds a row nothing resolves to
// and silently leaves claude-opus-5 at its built-in rate.
func (t *Table) Set(model string, r Rate) {
	id := strings.ToLower(model)
	t.rates[id] = r
	delete(t.history, id)
}

// AddHistory records that model cost r at any moment before until.
func (t *Table) AddHistory(model string, until time.Time, r Rate) {
	id := strings.ToLower(model)
	t.history[id] = append(t.history[id], pastRate{until: until, rate: r})
	sort.Slice(t.history[id], func(i, j int) bool { return t.history[id][i].until.Before(t.history[id][j].until) })
}

// rateAt returns the rate for an exact id at a moment in time.
func (t *Table) rateAt(id string, at time.Time) (Rate, bool) {
	cur, ok := t.rates[id]
	if !ok {
		return Rate{}, false
	}
	if !at.IsZero() {
		for _, h := range t.history[id] {
			if at.Before(h.until) {
				return h.rate, true
			}
		}
	}
	return cur, true
}

// has reports whether model is an exact, case-insensitive id in the table
// and returns its canonical form.
func (t *Table) has(model string) (string, bool) {
	id := strings.ToLower(model)
	_, ok := t.rates[id]
	return id, ok
}

var (
	// A trailing snapshot date such as "-20260401" or "@20260401".
	reDateSuffix = regexp.MustCompile(`(?i)^(.+)[-@]\d{8}$`)
	// A trailing "-codex" suffix, e.g. "gpt-5.5-codex".
	reCodexSuffix = regexp.MustCompile(`(?i)^(.+)-codex$`)
	// A trailing OpenAI-style dashed date such as "-2026-03-05".
	reOpenAIDateSuffix = regexp.MustCompile(`(?i)^(.+)-\d{4}-\d{2}-\d{2}$`)
)

func stripSuffix(re *regexp.Regexp, model string) (string, bool) {
	m := re.FindStringSubmatch(model)
	if m == nil {
		return "", false
	}
	return m[1], true
}

func isOpenAIID(model string) bool {
	return strings.HasPrefix(strings.ToLower(model), "gpt")
}

// longestPrefix returns the known id that is the longest prefix of model.
func (t *Table) longestPrefix(model string) (string, bool) {
	m := strings.ToLower(model)
	best := ""
	for id := range t.rates {
		if strings.HasPrefix(m, id) && len(id) > len(best) {
			best = id
		}
	}
	return best, best != ""
}

// Canonical resolves a model id as written in a transcript to the exact id
// the table knows, using, in order: exact match (case-insensitive); alias;
// strip a trailing date suffix like -20260401 or @20260401 and retry; strip
// a trailing "-codex" and retry; for OpenAI ids strip a trailing "-<date>"
// and retry; longest-prefix match against known ids. Returns ok=false if
// nothing matches. Two ids that resolve to the same canonical id are the
// same model, which is how "sonnet" and "claude-sonnet-5" compare equal.
func (t *Table) Canonical(model string) (string, bool) {
	if id, ok := t.has(model); ok {
		return id, true
	}
	if canon, ok := t.aliases[strings.ToLower(model)]; ok {
		if id, ok := t.has(canon); ok {
			return id, true
		}
	}
	if stripped, ok := stripSuffix(reDateSuffix, model); ok {
		if id, ok := t.has(stripped); ok {
			return id, true
		}
	}
	if stripped, ok := stripSuffix(reCodexSuffix, model); ok {
		if id, ok := t.has(stripped); ok {
			return id, true
		}
	}
	if isOpenAIID(model) {
		if stripped, ok := stripSuffix(reOpenAIDateSuffix, model); ok {
			if id, ok := t.has(stripped); ok {
				return id, true
			}
		}
	}
	return t.longestPrefix(model)
}

// SameModel reports whether two model ids name the same model. Exact ids
// compare by canonical form. A bare alias such as "opus" compares by family:
// it matches any Opus, because what "opus" resolved to depends on when the
// call was made, and the harness records the resolved id separately.
func (t *Table) SameModel(a, b string) bool {
	la, lb := strings.ToLower(strings.TrimSpace(a)), strings.ToLower(strings.TrimSpace(b))
	if la == "" || lb == "" {
		return false
	}
	if la == lb {
		return true
	}
	if _, ok := t.aliases[la]; ok {
		return Tier(lb) == la
	}
	if _, ok := t.aliases[lb]; ok {
		return Tier(la) == lb
	}
	ca, oka := t.Canonical(a)
	cb, okb := t.Canonical(b)
	return oka && okb && ca == cb
}

// Lookup returns today's rate for a model id. See Canonical for resolution.
func (t *Table) Lookup(model string) (Rate, bool) {
	return t.LookupAt(model, time.Time{})
}

// LookupAt returns the rate in force for a model at a moment in time. A zero
// time means today.
func (t *Table) LookupAt(model string, at time.Time) (Rate, bool) {
	id, ok := t.Canonical(model)
	if !ok {
		return Rate{}, false
	}
	return t.rateAt(id, at)
}

// Cost prices one turn at today's rates. known=false means the model was
// not found and cost is 0.
func (t *Table) Cost(modelID string, u model.Usage) (usd float64, known bool) {
	return t.CostAt(modelID, u, time.Time{})
}

// CostAt prices a usage at the standard-tier rate in force at a moment in
// time. It knows nothing about fast mode or long context, so callers
// holding a whole Turn should use CostTurn instead.
func (t *Table) CostAt(modelID string, u model.Usage, at time.Time) (usd float64, known bool) {
	r, ok := t.LookupAt(modelID, at)
	if !ok {
		return 0, false
	}
	return r.Apply(u), true
}

// RateForTurn returns the rate in force for one turn: the right model, the
// price on the day it ran, and the tier its speed and prompt size put it
// in. modelID is passed separately so a caller can ask what the same turn
// would have cost on a different model.
func (t *Table) RateForTurn(modelID string, tu model.Turn) (Rate, bool) {
	r, ok := t.LookupAt(modelID, tu.Timestamp)
	if !ok {
		return Rate{}, false
	}
	return r.For(tu.Speed, tu.Usage.ContextTokens()), true
}

// CostTurn prices one turn on the model it actually ran on, tier included.
// Prefer it over CostAt anywhere a whole Turn is in hand: CostAt cannot see
// the speed setting or the prompt size, so it silently prices a fast-mode
// or long-context turn at the standard rate.
func (t *Table) CostTurn(tu model.Turn) (usd float64, known bool) {
	r, ok := t.RateForTurn(tu.Model, tu)
	if !ok {
		return 0, false
	}
	return r.Apply(tu.Usage), true
}

// Apply prices a usage at this rate.
func (r Rate) Apply(u model.Usage) float64 {
	return (float64(u.Input)*r.Input +
		float64(u.CacheRead)*r.CacheRead +
		float64(u.CacheWrite5m)*r.CacheWrite5m +
		float64(u.CacheWrite1h)*r.CacheWrite1h +
		float64(u.Output)*r.Output) / 1e6
}

// Models lists every exact id in the table, sorted.
func (t *Table) Models() []string {
	ids := make([]string, 0, len(t.rates))
	for id := range t.rates {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Tier classifies a model id for "cheaper alternative" reasoning: "fable",
// "opus", "sonnet", "haiku" for Claude; "gpt-pro", "gpt", "gpt-mini",
// "gpt-nano" for OpenAI; "" if unknown.
func Tier(modelID string) string {
	m := strings.ToLower(modelID)
	switch {
	case strings.Contains(m, "fable"), strings.Contains(m, "mythos"):
		return "fable"
	case strings.Contains(m, "opus"):
		return "opus"
	case strings.Contains(m, "sonnet"):
		return "sonnet"
	case strings.Contains(m, "haiku"):
		return "haiku"
	}
	if !strings.HasPrefix(m, "gpt-") {
		return ""
	}
	switch {
	case strings.Contains(m, "-pro"):
		return "gpt-pro"
	case strings.Contains(m, "-nano"):
		return "gpt-nano"
	case strings.Contains(m, "-mini"):
		return "gpt-mini"
	}
	// The 5.6 and 6 named variants have no -pro/-mini/-nano suffix: astra
	// and cyber are top-end (gpt-pro), sol/terra are mid-tier (gpt), luna is
	// the small model (gpt-mini). cyber costs more than astra, so leaving it
	// mid-tier would offer gpt-5.4-mini as its "one step down".
	switch {
	case strings.Contains(m, "astra"), strings.Contains(m, "cyber"):
		return "gpt-pro"
	case strings.Contains(m, "luna"):
		return "gpt-mini"
	}
	return "gpt"
}

// CheaperAlternative returns the model id one tier down in the same vendor
// family (fable->claude-opus-5, opus->claude-sonnet-5, sonnet->
// claude-haiku-4-5, gpt-pro->the non-pro of the same version, gpt->the
// -mini of the same version if it exists else gpt-5.4-mini, gpt-mini->the
// -nano of the same version if it exists else gpt-5.4-nano). Returns "" for
// haiku, gpt-nano, and unknown.
func CheaperAlternative(modelID string) string {
	def := Default()
	m := strings.ToLower(modelID)
	switch Tier(modelID) {
	case "fable":
		return "claude-opus-5"
	case "opus":
		return "claude-sonnet-5"
	case "sonnet":
		return "claude-haiku-4-5"
	case "haiku":
		return ""
	case "gpt-pro":
		if base, ok := stripSuffix(regexp.MustCompile(`(?i)^(.+)-pro$`), m); ok {
			if _, ok := def.has(base); ok {
				return base
			}
		}
		return ""
	case "gpt":
		candidate := m + "-mini"
		if _, ok := def.has(candidate); ok {
			return candidate
		}
		return "gpt-5.4-mini"
	case "gpt-mini":
		base := strings.TrimSuffix(m, "-mini")
		candidate := base + "-nano"
		if _, ok := def.has(candidate); ok {
			return candidate
		}
		return "gpt-5.4-nano"
	case "gpt-nano":
		return ""
	}
	return ""
}
