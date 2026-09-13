// Command pricedrift checks the built-in price table against a public
// reference list and reports every model whose rate has moved since the
// table was last verified. It is a tripwire for maintainers, not a source
// of prices: it never writes to prices.go, because a rate in this repo has
// to come from the vendor's own pricing page. All it does is tell you which
// model to go and look at.
//
// Usage: go run ./tools/pricedrift
//
// Exit codes: 0 no drift, 1 drift found, 2 could not complete the check.
// The distinction matters to CI — 1 opens an issue, 2 is a broken job.
//
// It runs in CI only. The shipped tallybook binary makes no network calls.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/magna-nz/tallybook/internal/pricing"
)

// defaultSource is LiteLLM's price list: a plain JSON file, updated often,
// covering both vendors. It is a reference, not an authority — see the
// package comment.
const defaultSource = "https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json"

// refEntry is the part of a reference entry we compare. Every field is a
// pointer so that absent stays distinct from zero: a rate the source does
// not publish is never compared. That is what keeps this check quiet
// instead of noisy.
type refEntry struct {
	Input      *float64 `json:"input_cost_per_token"`
	Output     *float64 `json:"output_cost_per_token"`
	CacheRead  *float64 `json:"cache_read_input_token_cost"`
	CacheWrite *float64 `json:"cache_creation_input_token_cost"`
	// The long-TTL write tier. Anthropic publishes it; OpenAI does not,
	// and an absent field is simply not compared.
	CacheWrite1h *float64 `json:"cache_creation_input_token_cost_above_1hr"`

	// raw holds every numeric field by name, so the long-context tier can be
	// looked up: its keys carry the model's own threshold
	// ("input_cost_per_token_above_272k_tokens"), which no fixed struct tag
	// can name.
	raw map[string]float64
}

// at returns a raw numeric field, or nil when the source does not publish it.
func (e refEntry) at(key string) *float64 {
	v, ok := e.raw[key]
	if !ok {
		return nil
	}
	return &v
}

// reTrailingDate matches a snapshot suffix such as "-20251101".
var reTrailingDate = regexp.MustCompile(`-\d{8}$`)

// normalize turns a reference key into the id form prices.go uses, or
// returns ok=false if the key is not a direct-API rate we can compare.
//
// Keys carrying a provider route ("bedrock/...", "anthropic.claude-...")
// are rejected rather than normalized. Bedrock and Vertex publish their own
// prices for the same model, and tallybook prices direct API usage, so
// folding those in would report differences that are not drift at all.
//
// A provider prefix is recognised by a dot appearing before the first dash
// ("anthropic.claude-opus-4-5"), which no model id does: the dots in ids
// like "gpt-5.4-mini" always come after a dash.
func normalize(key string) (string, bool) {
	k := strings.ToLower(strings.TrimSpace(key))
	if k == "" || strings.Contains(k, "/") {
		return "", false
	}
	if dot, dash := strings.Index(k, "."), strings.Index(k, "-"); dot >= 0 && (dash < 0 || dot < dash) {
		return "", false
	}
	k = strings.TrimSuffix(k, "-v1:0")
	k = reTrailingDate.ReplaceAllString(k, "")
	return k, k != ""
}

// drift is one field of one model whose reference rate differs from ours.
type drift struct {
	model  string
	field  string
	ours   float64
	theirs float64
}

// moved reports whether two per-million rates differ by more than rounding.
// Both sides are dollar figures, so a relative tolerance well below a cent
// separates a real repricing from float noise.
func moved(ours, theirs float64) bool {
	if ours == theirs {
		return false
	}
	scale := math.Max(math.Abs(ours), math.Abs(theirs))
	return math.Abs(ours-theirs) > 1e-6*math.Max(scale, 1)
}

// compare checks one model's rate against a reference entry, appending a
// drift row per field that moved. Reference costs are per token; ours are
// per million, so every reference figure is scaled up before comparing.
func compare(id string, ours pricing.Rate, ref refEntry) []drift {
	var out []drift
	check := func(field string, mine float64, theirs *float64) {
		if theirs == nil {
			return // not published by the source; nothing to compare
		}
		scaled := *theirs * 1e6
		if moved(mine, scaled) {
			out = append(out, drift{model: id, field: field, ours: mine, theirs: scaled})
		}
	}
	check("input", ours.Input, ref.Input)
	check("output", ours.Output, ref.Output)
	check("cache read", ours.CacheRead, ref.CacheRead)
	check("5m cache write", ours.CacheWrite5m, ref.CacheWrite)
	check("1h cache write", ours.CacheWrite1h, ref.CacheWrite1h)

	// The long-context tier, whose keys are suffixed with the threshold the
	// model itself sets. A model with no Long tier has nothing to compare,
	// and neither does one whose threshold the source does not publish.
	if ours.Long != nil && ours.LongContextFrom > 0 {
		sfx := fmt.Sprintf("_above_%dk_tokens", ours.LongContextFrom/1000)
		check("long input", ours.Long.Input, ref.at("input_cost_per_token"+sfx))
		check("long output", ours.Long.Output, ref.at("output_cost_per_token"+sfx))
		check("long cache read", ours.Long.CacheRead, ref.at("cache_read_input_token_cost"+sfx))
		check("long 5m write", ours.Long.CacheWrite5m, ref.at("cache_creation_input_token_cost"+sfx))
		check("long 1h write", ours.Long.CacheWrite1h, ref.at("cache_creation_input_token_cost_above_1hr"+sfx))
	}
	return out
}

// fetch retrieves and decodes the reference list. Entries are decoded one
// at a time so that a single malformed or oddly-typed entry cannot fail the
// whole run — the list carries non-model rows and changes shape over time.
func fetch(url string, timeout time.Duration) (map[string]refEntry, error) {
	client := &http.Client{Timeout: timeout}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("decoding %s: %w", url, err)
	}
	out := make(map[string]refEntry, len(raw))
	for key, msg := range raw {
		id, ok := normalize(key)
		if !ok {
			continue
		}
		var e refEntry
		if err := json.Unmarshal(msg, &e); err != nil {
			continue // not a rate row, or a shape we do not understand
		}
		var anyFields map[string]any
		if err := json.Unmarshal(msg, &anyFields); err == nil {
			e.raw = make(map[string]float64, len(anyFields))
			for k, v := range anyFields {
				if f, ok := v.(float64); ok {
					e.raw[k] = f
				}
			}
		}
		if _, seen := out[id]; !seen {
			out[id] = e
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s: no usable entries; the file's shape may have changed", url)
	}
	return out, nil
}

func main() {
	source := flag.String("source", defaultSource, "reference price list URL")
	timeout := flag.Duration("timeout", 30*time.Second, "HTTP timeout")
	flag.Parse()

	ref, err := fetch(*source, *timeout)
	if err != nil {
		fmt.Fprintln(os.Stderr, "pricedrift:", err)
		os.Exit(2)
	}

	table := pricing.Default()
	var drifts []drift
	var unchecked []string
	for _, id := range table.Models() {
		ours, ok := table.Lookup(id)
		if !ok {
			continue
		}
		entry, found := ref[id]
		if !found {
			unchecked = append(unchecked, id)
			continue
		}
		drifts = append(drifts, compare(id, ours, entry)...)
	}
	sort.Slice(drifts, func(i, j int) bool {
		if drifts[i].model != drifts[j].model {
			return drifts[i].model < drifts[j].model
		}
		return drifts[i].field < drifts[j].field
	})

	checked := len(table.Models()) - len(unchecked)
	fmt.Printf("Checked %d of %d models against %s\n", checked, len(table.Models()), *source)
	fmt.Printf("Table last verified %s\n\n", table.Dated())

	if len(unchecked) > 0 {
		fmt.Printf("Not in the reference list (not drift, just unverifiable here): %s\n\n", strings.Join(unchecked, ", "))
	}

	// The reference list models no fast-mode tier, so those rates are
	// checked by hand against the vendor page or not at all. Saying so beats
	// a silent pass that looks like verification.
	var fastOnly []string
	for _, id := range table.Models() {
		if r, ok := table.Lookup(id); ok && r.Fast != nil {
			fastOnly = append(fastOnly, id)
		}
	}
	if len(fastOnly) > 0 {
		fmt.Printf("Fast-mode rates not checked (the reference list has no fast tier): %s\n\n", strings.Join(fastOnly, ", "))
	}

	if len(drifts) == 0 {
		fmt.Println("No drift: every rate the reference list publishes matches prices.go.")
		return
	}

	fmt.Printf("%d rate(s) differ. Check each against the vendor's own pricing page,\n", len(drifts))
	fmt.Printf("then decide which of two things happened:\n\n")
	fmt.Printf("  the vendor repriced  -> update basePrices AND add a priceHistory row,\n")
	fmt.Printf("                          so sessions from before the change keep their price\n")
	fmt.Printf("  our number was wrong -> update basePrices only. Do NOT add a history row:\n")
	fmt.Printf("                          it would assert a price change that never happened\n\n")
	for _, d := range drifts {
		fmt.Printf("  %-22s %-16s prices.go $%.4f   reference $%.4f\n", d.model, d.field, d.ours, d.theirs)
	}
	os.Exit(1)
}
