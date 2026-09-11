package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/magna-nz/tallybook/internal/config"
	"github.com/magna-nz/tallybook/internal/model"
	"github.com/magna-nz/tallybook/internal/store"
	"github.com/magna-nz/tallybook/internal/transcript/claude"
	"github.com/spf13/cobra"
)

// hookStdinDeadline is how long the hook waits for JSON on stdin before
// carrying on as if nothing arrived. A person running the command by hand
// gives it no stdin at all, and this must never hang on that.
//
// Claude Code gives a SessionEnd hook 1.5 seconds by default, so everything
// here has to finish well inside that.
const hookStdinDeadline = 200 * time.Millisecond

// hookInput is the JSON Claude Code writes to a hook's stdin. Unknown fields
// are ignored; a malformed or empty payload yields the zero value rather than
// an error.
type hookInput struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	CWD            string `json:"cwd"`
	HookEventName  string `json:"hook_event_name"`
	// Reason is why the session ended: clear, resume, logout,
	// prompt_input_exit or other.
	Reason string `json:"reason"`
}

// newHookCmd groups the hooks tallybook can be wired into a coding agent's
// lifecycle as.
func newHookCmd(flags *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hook",
		Short: "Hooks a coding agent CLI can call directly",
	}
	cmd.AddCommand(newHookSessionEndCmd(flags))
	return cmd
}

// newHookSessionEndCmd runs from a Claude Code SessionEnd hook.
//
// SessionEnd, not Stop: Stop fires every time the agent finishes a reply, so
// it would run dozens of times a session, and a Stop hook's stdout goes to the
// debug log rather than to the person at the keyboard. SessionEnd fires once,
// when the session actually ends.
//
// Because no hook's stdout reaches the user, this does not try to announce
// anything. It records the session in the ledger while the transcript is
// fresh, so `tallybook` is instant when it is next run, and appends one line
// to a log the user can read whenever they like. Run by hand, it also prints
// that line.
//
// It must never fail a user's session: no non-zero exit, no panic, no hang.
//
// Event choice and timeout verified 2026-09-11 against code.claude.com/docs/en/hooks.
func newHookSessionEndCmd(flags *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "session-end",
		Short: "Record the session that just ended (for a Claude Code SessionEnd hook)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			runHookSessionEnd(cmd, flags)
			return nil
		},
	}
}

// runHookSessionEnd does the real work. Every failure path returns silently
// rather than propagating an error: a hook that makes noise or fails a session
// is worse than one that does nothing.
func runHookSessionEnd(cmd *cobra.Command, flags *globalFlags) {
	defer func() {
		// Belt and braces: a hook must never take down the caller's
		// session, however this command misbehaves.
		_ = recover()
	}()

	in := readHookInput(cmd.InOrStdin())

	// A full scan would blow the 1.5 second budget on a large history, so the
	// hook never does one. When Claude Code names the transcript that changed,
	// that single file is read; when it does not, nothing is ingested and the
	// summary comes from whatever the ledger already holds.
	scoped := *flags
	scoped.noIngest = true

	ctx, err := openApp(&scoped)
	if err != nil {
		return
	}
	defer ctx.close()

	// Parsed once: the transcript is the largest thing the hook touches, and
	// reading it twice was doubling the work inside the tightest budget here.
	var parsed *model.Transcript
	if in.TranscriptPath != "" {
		parsed = ingestOne(ctx, in.TranscriptPath)
	}

	sessionID, ok := resolveHookSession(ctx, in, parsed)
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
	appendSessionLog(line)
	fmt.Fprintln(cmd.OutOrStdout(), line)
}

// ingestOne reads a single transcript into the store and returns it, so the
// caller can take the session id from it rather than parsing the file again.
// A nil return means the file could not be used.
func ingestOne(ctx *appContext, path string) *model.Transcript {
	t, err := claude.Parse(path)
	if err != nil {
		return nil
	}
	fi, err := os.Stat(path)
	if err != nil {
		return t
	}
	_ = ctx.st.ReplaceTranscript(t, fi.Size(), fi.ModTime())
	return t
}

// sessionLogName is the file the hook appends to. No hook's stdout reaches
// the person at the keyboard, so this is where the line actually goes.
const sessionLogName = "sessions.log"

// sessionLogMaxBytes caps the log. One line per session forever is a file the
// tool creates and never cleans up; past this size the oldest half is dropped.
const sessionLogMaxBytes = 256 * 1024

// appendSessionLog adds one dated line to the log, and says nothing if it
// cannot: a log that fails to write must not disturb the session that ended.
func appendSessionLog(line string) {
	path := filepath.Join(config.Dir(), sessionLogName)
	trimSessionLog(path)

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s  %s\n", time.Now().Format(time.RFC3339), line)
}

// trimSessionLog drops the older half of the log once it passes the cap. It
// keeps whole lines, and gives up silently on any error.
func trimSessionLog(path string) {
	fi, err := os.Stat(path)
	if err != nil || fi.Size() <= sessionLogMaxBytes {
		return
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return
	}
	keep := body[len(body)/2:]
	if i := bytes.IndexByte(keep, '\n'); i >= 0 {
		keep = keep[i+1:] // start at a line boundary
	}
	tmp := path + ".tmp"
	if os.WriteFile(tmp, keep, 0o644) != nil {
		return
	}
	_ = os.Rename(tmp, path)
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
func resolveHookSession(ctx *appContext, in hookInput, parsed *model.Transcript) (string, bool) {
	if parsed != nil && parsed.Session.ID != "" {
		return parsed.Session.ID, true
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

// hookSummaryLine composes the one line the session-end hook records: the
// session's cost and the share of its context that was tool output.
//
// Everything here is scoped to the one session that just ended. An earlier
// version also counted open findings, which walked every session in the window
// and read every project's agent files, so the hook's cost grew with the
// user's whole history inside a budget measured in milliseconds.
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

	costPhrase := hookFmtUSD(usd)
	if ctx.plan == config.PlanSubscription {
		costPhrase += " list-price equiv."
	}

	return fmt.Sprintf(
		"tallybook: this session %s, %s of context was tool output, %d turns",
		costPhrase, hookFmtShare(toolShare), len(turns),
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
