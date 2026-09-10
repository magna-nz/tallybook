package findings

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/magna-nz/tallybook/internal/model"
	"github.com/magna-nz/tallybook/internal/pricing"
)

// costOf prices turns at the model they actually ran on, using the rate in
// force when each turn happened. A turn whose model the table does not know
// contributes nothing: a missing price is never guessed at.
func costOf(in Input, turns []model.Turn) float64 {
	var total float64
	for _, t := range turns {
		if usd, ok := in.Prices.CostAt(t.Model, t.Usage, t.Timestamp); ok {
			total += usd
		}
	}
	return total
}

// costAs prices the same usage as if every turn had run on altModel, at the
// alternative's rate on the day of the turn. If altModel is unknown the
// result is 0, which callers must treat as "no comparison available".
func costAs(in Input, turns []model.Turn, altModel string) float64 {
	var total float64
	for _, t := range turns {
		r, ok := in.Prices.LookupAt(altModel, t.Timestamp)
		if !ok {
			continue
		}
		total += r.Apply(t.Usage)
	}
	return total
}

// sameModel reports whether two model ids name the same model. Ids are
// compared through the price table's canonical form, so the "sonnet" a
// caller writes and the "claude-sonnet-5" the harness records compare equal.
// When either side is unknown to the table there is nothing better than a
// case-insensitive string compare.
func sameModel(in Input, a, b string) bool {
	if in.Prices == nil {
		return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
	}
	return in.Prices.SameModel(a, b)
}

// canonical resolves a model id for grouping, falling back to the lowercased
// id when the table does not know it.
func canonical(in Input, id string) string {
	if c, ok := in.Prices.Canonical(id); ok {
		return c
	}
	return strings.ToLower(strings.TrimSpace(id))
}

// readOnlyTools are the tools that only look at the project. A run that used
// nothing else changed nothing, so it can move to a cheaper model without
// risking a bad edit.
var readOnlyTools = map[string]bool{
	"read":         true,
	"grep":         true,
	"glob":         true,
	"ls":           true,
	"webfetch":     true,
	"websearch":    true,
	"notebookread": true,
	"todoread":     true,
	"toolsearch":   true,
	"skill":        true,
}

// mutatingTools change the project or run arbitrary commands. Bare "bash"
// (no class) and "bash(write)" are mutating; "bash(read)" is accepted above
// because the parser classified every command in it as read-only without
// keeping the command text.
var mutatingTools = map[string]bool{
	"edit":         true,
	"write":        true,
	"multiedit":    true,
	"notebookedit": true,
	"apply_patch":  true,
	"exec_command": true,
	"local_shell":  true,
	"shell":        true,
	"bash":         true,
}

// mcpReadVerbs mark an MCP tool as a reader rather than a writer.
var mcpReadVerbs = []string{"read", "get", "list", "search", "find", "fetch"}

// isReadOnlyTool reports whether a single tool call could not have changed
// anything. Unknown tools are treated as mutating.
func isReadOnlyTool(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" {
		return false
	}
	if strings.HasSuffix(n, "(read)") {
		return true // a shell tool whose every command the parser classified as read-only
	}
	if mutatingTools[n] {
		return false
	}
	if readOnlyTools[n] {
		return true
	}
	if strings.HasPrefix(n, "mcp__") {
		for _, verb := range mcpReadVerbs {
			if strings.Contains(n, verb) {
				return true
			}
		}
	}
	return false
}

// allReadOnly reports whether every tool a run used was read-only. A run that
// called no tools at all counts as read-only: it changed nothing.
func allReadOnly(counts map[string]int) bool {
	for name, n := range counts {
		if n <= 0 {
			continue
		}
		if !isReadOnlyTool(name) {
			return false
		}
	}
	return true
}

// builtinAgents ship with the harness and have no file on disk to edit.
var builtinAgents = map[string]bool{
	"explore":            true,
	"plan":               true,
	"general-purpose":    true,
	"claude-code-guide":  true,
	"statusline-setup":   true,
	"claude":             true,
	"output-style-setup": true,
}

// agentFile is the file that defines a sub-agent type, or "" when the type is
// built into the harness and the only place to set a model is the call site.
func agentFile(subagentType string) string {
	t := strings.ToLower(strings.TrimSpace(subagentType))
	if t == "" || t == "unnamed" || builtinAgents[t] {
		return ""
	}
	return ".claude/agents/" + t + ".md"
}

// shortNames maps an exact model id to the name a user would type in an
// agent file or an Agent call.
var shortNames = map[string]string{
	"claude-opus-5":     "opus",
	"claude-sonnet-5":   "sonnet",
	"claude-haiku-4-5":  "haiku",
	"claude-fable-5-1":  "fable",
	"claude-mythos-5-1": "fable",
}

// shortModelName is what to write in a config file: "sonnet", "haiku", or the
// exact id for everything else.
func shortModelName(id string) string {
	if s, ok := shortNames[strings.ToLower(id)]; ok {
		return s
	}
	return strings.ToLower(id)
}

// modelDisplay is the name to use in a sentence: "Opus", "Sonnet", or the
// exact id for OpenAI models, which have no friendly family name.
func modelDisplay(id string) string {
	switch pricing.Tier(id) {
	case "fable":
		return "Fable"
	case "opus":
		return "Opus"
	case "sonnet":
		return "Sonnet"
	case "haiku":
		return "Haiku"
	}
	return strings.ToLower(id)
}

// mostCommonModel is the model a session mostly ran on. Ties break on the id
// so the answer is stable between runs.
func mostCommonModel(turns []model.Turn) string {
	counts := map[string]int{}
	for _, t := range turns {
		if t.Model != "" {
			counts[t.Model]++
		}
	}
	best, bestN := "", 0
	for m, n := range counts {
		if n > bestN || (n == bestN && m < best) {
			best, bestN = m, n
		}
	}
	return best
}

// outputRatio is how many times more the output tokens of a cost as much as
// b's, at the given moment. It returns 0 when either price is unknown.
func outputRatio(in Input, a, b string, at time.Time) float64 {
	ra, oka := in.Prices.LookupAt(a, at)
	rb, okb := in.Prices.LookupAt(b, at)
	if !oka || !okb || rb.Output <= 0 {
		return 0
	}
	return ra.Output / rb.Output
}

// shortID is the first 8 characters of a session id, which is what the rest
// of the tool prints.
func shortID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

// joinList renders a list the way a person would say it: "a", "a and b",
// "a, b and c".
func joinList(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	case 2:
		return items[0] + " and " + items[1]
	}
	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}

// sortedKeys returns a map's keys in a stable order.
func sortedKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// windowUntil is the end of the reporting window: the filter's Until, or now.
func windowUntil(in Input) time.Time {
	if !in.Filter.Until.IsZero() {
		return in.Filter.Until
	}
	if !in.Now.IsZero() {
		return in.Now
	}
	return time.Now()
}

// windowPhrase describes the reporting window in a sentence, e.g. "in the
// last 30 days" or "between 11 Aug and 10 Sep 2026".
func windowPhrase(in Input) string {
	if !in.Filter.Since.IsZero() {
		return fmt.Sprintf("between %s and %s",
			in.Filter.Since.Format("2 Jan"), windowUntil(in).Format("2 Jan 2006"))
	}
	if in.WindowDays > 0 {
		return fmt.Sprintf("in the last %s days", fmtInt(int64(in.WindowDays+0.5)))
	}
	return "in this window"
}

// fmtUSD renders a dollar figure the way a bill does: "$1,234.56", "$0.40".
func fmtUSD(v float64) string {
	sign := ""
	if v < 0 {
		sign, v = "-", -v
	}
	s := strconv.FormatFloat(v, 'f', 2, 64)
	dot := strings.IndexByte(s, '.')
	return sign + "$" + groupThousands(s[:dot]) + s[dot:]
}

// fmtInt renders a count with thousands separators: "30,000".
func fmtInt(v int64) string {
	sign := ""
	if v < 0 {
		sign, v = "-", -v
	}
	return sign + groupThousands(strconv.FormatInt(v, 10))
}

func groupThousands(digits string) string {
	if len(digits) <= 3 {
		return digits
	}
	var b strings.Builder
	lead := len(digits) % 3
	if lead > 0 {
		b.WriteString(digits[:lead])
	}
	for i := lead; i < len(digits); i += 3 {
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(digits[i : i+3])
	}
	return b.String()
}

// fmtPct renders a 0..1 share as a whole-number percentage, without the sign.
func fmtPct(share float64) string {
	return strconv.FormatInt(int64(share*100+0.5), 10)
}

// fmtOneDP renders a number to one decimal place, e.g. a price ratio.
func fmtOneDP(v float64) string {
	return strconv.FormatFloat(v, 'f', 1, 64)
}

// fmtMinutes renders a duration in whole minutes.
func fmtMinutes(d time.Duration) string {
	return fmtInt(int64(d.Minutes() + 0.5))
}

// frontmatterPatch is a unified diff that adds one line to the settings block
// at the top of an agent file. It is printed for the user to apply; tallybook
// never writes to a file itself.
func frontmatterPatch(file, line string) string {
	return "--- a/" + file + "\n" +
		"+++ b/" + file + "\n" +
		"@@ -1,2 +1,3 @@\n" +
		" ---\n" +
		"+" + line + "\n"
}

// plural returns "" for one and "s" for anything else.
func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// pick chooses between a singular and a plural form of a word.
func pick(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// capitalise upper-cases the first letter of a word so it can start a
// sentence. It leaves an empty string alone.
func capitalise(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
