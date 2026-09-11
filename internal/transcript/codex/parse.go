package codex

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/magna-nz/tallybook/internal/model"
)

// maxLineSize bounds a single rollout line at 16 MiB, well above anything a
// real Codex session should produce.
const maxLineSize = 16 * 1024 * 1024

// rawRecord is the outer shape of every rollout line:
// {"timestamp": RFC3339, "type": "...", "payload": {...}}.
type rawRecord struct {
	Timestamp time.Time       `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

// usageFields is the shape shared by a token_usage_record's "usage" object
// and the "total_token_usage" / "last_token_usage" snapshots inside an
// event_msg token_count record.
type usageFields struct {
	InputTokens           int64 `json:"input_tokens"`
	CachedInputTokens     int64 `json:"cached_input_tokens"`
	CacheWriteInputTokens int64 `json:"cache_write_input_tokens"`
	OutputTokens          int64 `json:"output_tokens"`
	ReasoningOutputTokens int64 `json:"reasoning_output_tokens"`
	TotalTokens           int64 `json:"total_tokens"`
}

type sessionMetaPayload struct {
	ID         string    `json:"id"`
	SessionID  string    `json:"session_id"`
	Timestamp  time.Time `json:"timestamp"`
	Cwd        string    `json:"cwd"`
	Originator string    `json:"originator"`
	CLIVersion string    `json:"cli_version"`
	Git        *struct {
		Branch string `json:"branch"`
	} `json:"git"`
}

type turnContextPayload struct {
	Model  string `json:"model"`
	Effort string `json:"effort"`
	Cwd    string `json:"cwd"`
	TurnID string `json:"turn_id"`
}

type tokenUsageRecordPayload struct {
	ThreadID   string      `json:"thread_id"`
	TurnID     string      `json:"turn_id"`
	SessionID  string      `json:"session_id"`
	RootTurnID string      `json:"root_turn_id"`
	ResponseID string      `json:"response_id"`
	Usage      usageFields `json:"usage"`
}

type eventMsgEnvelope struct {
	Type string `json:"type"`
}

type tokenCountPayload struct {
	Info *struct {
		TotalTokenUsage usageFields `json:"total_token_usage"`
		LastTokenUsage  usageFields `json:"last_token_usage"`
	} `json:"info"`
}

type responseItemEnvelope struct {
	Type string `json:"type"`
}

type messagePayload struct {
	Role    string `json:"role"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

type functionCallPayload struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
	CallID    string `json:"call_id"`
}

type localShellCallPayload struct {
	CallID *string         `json:"call_id"`
	Action json.RawMessage `json:"action"`
}

type customToolCallPayload struct {
	CallID string `json:"call_id"`
	Name   string `json:"name"`
	Input  string `json:"input"`
}

type callOutputPayload struct {
	CallID string          `json:"call_id"`
	Output json.RawMessage `json:"output"`
}

// Parse reads one rollout file. Malformed lines are skipped. Parse returns
// an error only if the file cannot be opened or has no session_meta record.
func Parse(path string) (*model.Transcript, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("codex: open %s: %w", path, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), maxLineSize)

	var records []rawRecord
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var r rawRecord
		if err := json.Unmarshal(line, &r); err != nil {
			continue
		}
		records = append(records, r)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("codex: read %s: %w", path, err)
	}

	hasTokenUsageRecord := false
	for _, r := range records {
		if r.Type == "token_usage_record" {
			hasTokenUsageRecord = true
			break
		}
	}

	var (
		session       model.Session
		sessionFound  bool
		currentModel  = "gpt-5"
		currentEffort string
		turns         []model.Turn
		toolResults   []model.ToolResult
		pendingCalls  []model.ToolCall
		pendingText   int
		tcCount       int
		haveTotal     bool
		prevTotal     usageFields
	)

	finalize := func(id string, ts time.Time, u model.Usage) {
		turns = append(turns, model.Turn{
			ID:        id,
			Timestamp: ts,
			Model:     currentModel,
			Effort:    currentEffort,
			Usage:     u,
			TextChars: pendingText,
			ToolCalls: pendingCalls,
		})
		pendingCalls = nil
		pendingText = 0
	}

	for _, r := range records {
		switch r.Type {
		case "session_meta":
			var p sessionMetaPayload
			if err := json.Unmarshal(r.Payload, &p); err != nil {
				continue
			}
			session = model.Session{
				ID:         p.ID,
				Source:     model.SourceCodex,
				Path:       path,
				Project:    p.Cwd,
				StartedAt:  p.Timestamp,
				CLIVersion: p.CLIVersion,
			}
			if p.Git != nil {
				session.GitBranch = p.Git.Branch
			}
			sessionFound = true

		case "turn_context":
			var p turnContextPayload
			if err := json.Unmarshal(r.Payload, &p); err != nil {
				continue
			}
			currentModel = p.Model
			currentEffort = p.Effort

		case "token_usage_record":
			var p tokenUsageRecordPayload
			if err := json.Unmarshal(r.Payload, &p); err != nil {
				continue
			}
			u := deriveUsage(p.Usage.InputTokens, p.Usage.CachedInputTokens, p.Usage.CacheWriteInputTokens, p.Usage.OutputTokens, p.Usage.ReasoningOutputTokens)
			finalize(p.ResponseID, r.Timestamp, u)

		case "event_msg":
			if hasTokenUsageRecord {
				continue
			}
			var env eventMsgEnvelope
			if err := json.Unmarshal(r.Payload, &env); err != nil {
				continue
			}
			if env.Type != "token_count" {
				continue
			}
			var p tokenCountPayload
			if err := json.Unmarshal(r.Payload, &p); err != nil {
				continue
			}
			if p.Info == nil {
				continue
			}
			cur := p.Info.TotalTokenUsage
			var prev usageFields
			if haveTotal {
				prev = prevTotal
			}
			if cur.TotalTokens <= prev.TotalTokens {
				// Duplicate or non-advancing event_msg; contributes nothing.
				continue
			}
			u := deriveUsage(
				cur.InputTokens-prev.InputTokens,
				cur.CachedInputTokens-prev.CachedInputTokens,
				cur.CacheWriteInputTokens-prev.CacheWriteInputTokens,
				cur.OutputTokens-prev.OutputTokens,
				cur.ReasoningOutputTokens-prev.ReasoningOutputTokens,
			)
			tcCount++
			finalize(fmt.Sprintf("tc-%d", tcCount), r.Timestamp, u)
			prevTotal = cur
			haveTotal = true

		case "response_item":
			var env responseItemEnvelope
			if err := json.Unmarshal(r.Payload, &env); err != nil {
				continue
			}
			switch env.Type {
			case "message":
				var p messagePayload
				if err := json.Unmarshal(r.Payload, &p); err != nil {
					continue
				}
				if p.Role == "assistant" {
					for _, c := range p.Content {
						pendingText += len(c.Text)
					}
				}

			case "reasoning":
				// Ignored: not billable content on its own.

			case "function_call":
				var p functionCallPayload
				if err := json.Unmarshal(r.Payload, &p); err != nil {
					continue
				}
				pendingCalls = append(pendingCalls, model.ToolCall{
					ID:          p.CallID,
					Name:        p.Name,
					InputChars:  len(p.Arguments),
					Class:       classifyShellArgs(p.Name, []byte(p.Arguments)),
					InputDigest: model.DigestInput(p.Name, []byte(p.Arguments)),
				})

			case "local_shell_call":
				var p localShellCallPayload
				if err := json.Unmarshal(r.Payload, &p); err != nil {
					continue
				}
				id := ""
				if p.CallID != nil {
					id = *p.CallID
				}
				if id == "" {
					id = fmt.Sprintf("local_shell-%d", len(pendingCalls)+1)
				}
				pendingCalls = append(pendingCalls, model.ToolCall{
					ID:          id,
					Name:        "local_shell",
					InputChars:  len(p.Action),
					Class:       classifyShellArgs("local_shell", p.Action),
					InputDigest: model.DigestInput("local_shell", p.Action),
				})

			case "custom_tool_call":
				var p customToolCallPayload
				if err := json.Unmarshal(r.Payload, &p); err != nil {
					continue
				}
				pendingCalls = append(pendingCalls, model.ToolCall{
					ID:          p.CallID,
					Name:        p.Name,
					InputChars:  len(p.Input),
					InputDigest: model.DigestInput(p.Name, []byte(p.Input)),
				})

			case "function_call_output", "custom_tool_call_output":
				var p callOutputPayload
				if err := json.Unmarshal(r.Payload, &p); err != nil {
					continue
				}
				chars, isErr := parseCallOutput(p.Output)
				toolResults = append(toolResults, model.ToolResult{
					ToolCallID: p.CallID,
					Timestamp:  r.Timestamp,
					Chars:      chars,
					IsError:    isErr,
				})

			default:
				// Other response_item types (e.g. unrecognised) are ignored.
			}

		default:
			// "compacted" and any other unknown record types are skipped.
		}
	}

	if !sessionFound {
		return nil, fmt.Errorf("codex: %s: no session_meta record", path)
	}

	if len(pendingCalls) > 0 {
		ts := session.StartedAt
		if len(records) > 0 {
			ts = records[len(records)-1].Timestamp
		}
		finalize("", ts, model.Usage{})
	}

	if len(records) > 0 {
		session.EndedAt = records[len(records)-1].Timestamp
	} else {
		session.EndedAt = session.StartedAt
	}

	for i := range turns {
		turns[i].SessionID = session.ID
	}
	for i := range toolResults {
		toolResults[i].SessionID = session.ID
	}

	return &model.Transcript{Session: session, Turns: turns, ToolResults: toolResults}, nil
}

// deriveUsage maps the raw Codex usage counters (or a delta thereof) onto
// model.Usage. cached_input_tokens is a subset of input_tokens, so the
// uncached amount is input_tokens - cached_input_tokens. Every field is
// clamped at 0 so a malformed delta can never produce a negative count.
func deriveUsage(inputTokens, cachedInputTokens, cacheWriteInputTokens, outputTokens, reasoningOutputTokens int64) model.Usage {
	input := inputTokens - cachedInputTokens
	if input < 0 {
		input = 0
	}

	cacheRead := cachedInputTokens
	if cacheRead < 0 {
		cacheRead = 0
	}

	cacheWrite := cacheWriteInputTokens
	if cacheWrite < 0 {
		cacheWrite = 0
	}

	output := outputTokens
	if output < 0 {
		output = 0
	}

	thinking := reasoningOutputTokens
	if thinking < 0 {
		thinking = 0
	}

	return model.Usage{
		Input:        input,
		CacheRead:    cacheRead,
		CacheWrite5m: cacheWrite,
		Output:       output,
		Thinking:     thinking,
	}
}

// parseCallOutput decodes a function_call_output / custom_tool_call_output
// "output" field, which is either a plain string or an object with "body"
// and "success".
func parseCallOutput(raw json.RawMessage) (chars int, isError bool) {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return len(s), false
	}

	var obj struct {
		Body    string `json:"body"`
		Success *bool  `json:"success"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil {
		isError = obj.Success != nil && !*obj.Success
		return len(obj.Body), isError
	}

	return 0, false
}

// shellTools are the Codex tool names that run a command line.
var shellTools = map[string]bool{"exec_command": true, "shell": true, "local_shell": true, "bash": true, "container.exec": true, "shell_command": true}

// classifyShellArgs pulls the command out of a shell tool's arguments,
// which Codex has written as {"cmd": "..."}, {"command": "..."} or
// {"command": ["bash", "-lc", "..."]} over time, and classifies it. The
// command text is not kept.
func classifyShellArgs(name string, args []byte) string {
	if !shellTools[name] || len(args) == 0 {
		return ""
	}
	var raw struct {
		Cmd     json.RawMessage `json:"cmd"`
		Command json.RawMessage `json:"command"`
	}
	if err := json.Unmarshal(args, &raw); err != nil {
		return ""
	}
	for _, v := range [][]byte{raw.Cmd, raw.Command} {
		if len(v) == 0 {
			continue
		}
		var s string
		if json.Unmarshal(v, &s) == nil {
			return model.ClassifyCommand(s)
		}
		var parts []string
		if json.Unmarshal(v, &parts) == nil && len(parts) > 0 {
			// ["bash","-lc","<script>"]: classify the script, not the shell.
			if len(parts) >= 3 && (parts[1] == "-lc" || parts[1] == "-c") {
				return model.ClassifyCommand(parts[len(parts)-1])
			}
			return model.ClassifyCommand(strings.Join(parts, " "))
		}
	}
	return ""
}
