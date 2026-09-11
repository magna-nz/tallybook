package main

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/magna-nz/tallybook/internal/config"
	"github.com/magna-nz/tallybook/internal/model"
	"github.com/magna-nz/tallybook/internal/store"
	"github.com/magna-nz/tallybook/internal/transcript/claude"
	"github.com/spf13/cobra"
)

// hookStdinDeadline is how long "hook stop" waits for JSON on stdin before
// carrying on as if nothing arrived. A human running the command by hand
// gives it no stdin at all, and this command must never hang on that.
const hookStdinDeadline = 200 * time.Millisecond

// hookInput is the JSON a Claude Code Stop hook writes to this command's
// stdin. Unknown fields are ignored; a malformed or empty payload yields the
// zero value rather than an error.
type hookInput struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	CWD            string `json:"cwd"`
	HookEventName  string `json:"hook_event_name"`
}

// newHookCmd groups the hooks tallybook can be wired into a coding agent's
// lifecycle as.
func newHookCmd(flags *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hook",
		Short: "Hooks a coding agent CLI can call directly",
	}
	cmd.AddCommand(newHookStopCmd(flags))
	return cmd
}

// newHookStopCmd is meant to run from a Claude Code Stop hook: a one-line
// summary of the session that just ended, on stdout. It must never fail a
// user's session, so it never returns a non-zero exit code (short of Cobra
// rejecting a flag before RunE even runs) and never panics or hangs.
func newHookStopCmd(flags *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Print a one-line cost summary for the session that just ended",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			runHookStop(cmd, flags)
			return nil
		},
	}
}

// runHookStop does the real work of "hook stop". Every failure path returns
// silently rather than propagating an error: a hook that makes noise or
// fails a session is worse than one that prints nothing.
func runHookStop(cmd *cobra.Command, flags *globalFlags) {
	defer func() {
		// Belt and braces: a hook must never take down the caller's
		// session, however this command misbehaves.
		_ = recover()
	}()

	in := readHookInput(cmd.InOrStdin())

	ctx, err := openApp(flags)
	if err != nil {
		return
	}
	defer ctx.close()

	sessionID, ok := resolveHookSession(ctx, in)
	if !ok {
		return
	}
	row, err := ctx.st.Session(sessionID)
	if err != nil || row == nil {
		return
	}

	line, ok := hookSummaryLine(ctx, *row)
	if !ok {
		return
	}
	fmt.Fprintln(cmd.OutOrStdout(), line)
}

// readHookInput reads and parses whatever JSON is waiting on r, giving up
// after hookStdinDeadline rather than blocking forever when nothing is
// piped in. A read error or malformed JSON yields the zero value.
func readHookInput(r io.Reader) hookInput {
	type result struct {
		data []byte
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		data, err := io.ReadAll(r)
		ch <- result{data, err}
	}()

	select {
	case res := <-ch:
		var in hookInput
		if res.err == nil && len(res.data) > 0 {
			_ = json.Unmarshal(res.data, &in) // malformed input: keep the zero value
		}
		return in
	case <-time.After(hookStdinDeadline):
		return hookInput{}
	}
}

// resolveHookSession works out which session the summary is about: the one
// named by the transcript path, else by the session id, else the most
// recently started Claude Code session in the store.
func resolveHookSession(ctx *appContext, in hookInput) (string, bool) {
	if in.TranscriptPath != "" {
		t, err := claude.Parse(in.TranscriptPath)
		if err == nil && t.Session.ID != "" {
			return t.Session.ID, true
		}
	}
	if in.SessionID != "" {
		return in.SessionID, true
	}
	return mostRecentClaudeSessionID(ctx.st)
}

// mostRecentClaudeSessionID returns the id of the most recently started main
// (non-sub-agent) Claude Code session in the store.
func mostRecentClaudeSessionID(st *store.Store) (string, bool) {
	rows, err := st.Sessions(store.Filter{Source: model.SourceClaudeCode})
	if err != nil {
		return "", false
	}
	var best *store.SessionRow
	for i := range rows {
		r := &rows[i]
		if r.AgentID != "" {
			continue // sub-agent run, not the main session
		}
		if best == nil || r.StartedAt.After(best.StartedAt) {
			best = r
		}
	}
	if best == nil {
		return "", false
	}
	return best.ID, true
}

// hookSummaryLine composes the one line "hook stop" prints: the session's
// cost, the share of its context that was tool output, and how many
// findings are open in the default window.
func hookSummaryLine(ctx *appContext, row store.SessionRow) (string, bool) {
	turns, err := ctx.st.Turns(row.ID)
	if err != nil {
		return "", false
	}
	var usd float64
	var contextTokens int64
	for _, t := range turns {
		amt, _ := ctx.prices.CostAt(t.Model, t.Usage, t.Timestamp)
		usd += amt
		contextTokens += t.Usage.ContextTokens()
	}

	results, err := ctx.st.ToolResults(row.ID)
	if err != nil {
		return "", false
	}
	var toolChars int
	for _, r := range results {
		toolChars += r.Chars
	}

	toolShare := 0.0
	if contextTokens > 0 {
		toolTokens := float64(toolChars) / 4
		toolShare = toolTokens / float64(contextTokens)
		if toolShare > 1 {
			toolShare = 1
		}
	}

	_, totals, err := sessionsAndTotals(ctx)
	if err != nil {
		return "", false
	}
	fs, err := findingsFor(ctx, totals)
	if err != nil {
		return "", false
	}

	costPhrase := hookFmtUSD(usd)
	if ctx.plan == config.PlanSubscription {
		costPhrase += " list-price equiv."
	}

	findingWord := "findings"
	if len(fs) == 1 {
		findingWord = "finding"
	}

	return fmt.Sprintf(
		"tallybook: this session %s, %s of context was tool output, %d %s across your %s",
		costPhrase, hookFmtShare(toolShare), len(fs), findingWord, hookWindowPhrase(ctx.sinceFlag, ctx.window.Label),
	), true
}

// hookFmtUSD formats a dollar amount for the hook's one-line summary.
func hookFmtUSD(amount float64) string {
	return fmt.Sprintf("$%.2f", amount)
}

// hookFmtShare formats a 0..1 fraction as a whole-number percentage.
func hookFmtShare(fraction float64) string {
	return fmt.Sprintf("%.0f%%", fraction*100)
}

// hookWindowPhrase renders the reporting window the way the hook's summary
// line reads it: "last 30 days" rather than report's "Last 30 days".
func hookWindowPhrase(sinceFlag, label string) string {
	switch sinceFlag {
	case "7d":
		return "last 7 days"
	case "30d":
		return "last 30 days"
	case "90d":
		return "last 90 days"
	case "all":
		return "all time"
	default:
		if label != "" {
			return label
		}
		return sinceFlag
	}
}
