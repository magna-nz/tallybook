package claude

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/magna-nz/tallybook/internal/model"
)

const maxLineSize = 16 * 1024 * 1024 // 16 MB, lines can exceed 1 MB

// rawRecord is the top-level shape of every line in a Claude Code
// transcript. Fields not relevant to a given record type are simply left
// zero.
type rawRecord struct {
	Type          string          `json:"type"`
	UUID          string          `json:"uuid"`
	ParentUUID    string          `json:"parentUuid"`
	SessionID     string          `json:"sessionId"`
	Timestamp     string          `json:"timestamp"`
	CWD           string          `json:"cwd"`
	GitBranch     string          `json:"gitBranch"`
	Version       string          `json:"version"`
	IsSidechain   bool            `json:"isSidechain"`
	AgentID       string          `json:"agentId"`
	RequestID     string          `json:"requestId"`
	Effort        string          `json:"effort"`
	Message       json.RawMessage `json:"message"`
	ToolUseResult json.RawMessage `json:"toolUseResult"`
	// Subtype distinguishes system records; the only one a rule needs is
	// "compact_boundary", which marks that the harness just compacted
	// context. compactMetadata (trigger, preTokens) rides along on that
	// record but is never read: a rule only needs to know a compaction
	// happened, not why or how big.
	Subtype string `json:"subtype"`
	// IsCompactSummary marks Claude Code's other compaction shape: a
	// synthetic user record whose content is the summary prompt text. The
	// text is never recorded; only the fact that a compaction happened is.
	IsCompactSummary bool `json:"isCompactSummary"`
}

// assistantMessage is rawRecord.Message when Type == "assistant".
type assistantMessage struct {
	ID      string         `json:"id"`
	Model   string         `json:"model"`
	Role    string         `json:"role"`
	Type    string         `json:"type"`
	Content []contentBlock `json:"content"`
	Usage   *usageJSON     `json:"usage"`
}

type contentBlock struct {
	Type     string          `json:"type"`
	Text     string          `json:"text"`
	Thinking string          `json:"thinking"`
	ID       string          `json:"id"`
	Name     string          `json:"name"`
	Input    json.RawMessage `json:"input"`
}

type usageJSON struct {
	InputTokens              int64 `json:"input_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	OutputTokensDetails      *struct {
		ThinkingTokens int64 `json:"thinking_tokens"`
	} `json:"output_tokens_details"`
	CacheCreation *struct {
		Ephemeral5m int64 `json:"ephemeral_5m_input_tokens"`
		Ephemeral1h int64 `json:"ephemeral_1h_input_tokens"`
	} `json:"cache_creation"`
	// Speed is "fast" when the turn ran in fast mode, "standard" otherwise.
	// Older transcripts omit it, and some records carry null; both read as
	// empty, which prices at the standard rate.
	Speed speedField `json:"speed"`
}

// speedField is a string that tolerates not being one.
//
// A type error anywhere in the usage object makes the whole assistant record
// fail to unmarshal, and the parser drops such a record - so a turn whose
// speed arrived as a number or an object would lose its tokens entirely,
// which is far worse than pricing it at the standard rate. Until this field
// was read, any shape was harmlessly ignored; that stays true.
type speedField string

func (s *speedField) UnmarshalJSON(b []byte) error {
	var v string
	if err := json.Unmarshal(b, &v); err == nil {
		*s = speedField(v)
	}
	return nil
}

// userMessage is rawRecord.Message when Type == "user".
type userMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"` // string (prompt) or []toolResultBlock
}

type toolResultBlock struct {
	Type      string          `json:"type"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"` // string or []textBlock
	IsError   bool            `json:"is_error"`
}

type textBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// imageResultChars is what one image block counts for when sizing a tool
// result: about 1,600 tokens at the usual four characters per token.
const imageResultChars = 1600 * 4

type agentToolInput struct {
	SubagentType string `json:"subagent_type"`
	Model        string `json:"model"`
	Description  string `json:"description"`
}

type toolUseResultAgent struct {
	AgentID       string `json:"agentId"`
	ResolvedModel string `json:"resolvedModel"`
}

// turnBuilder accumulates the several assistant records that share one
// message.id into a single model.Turn.
type turnBuilder struct {
	id        string
	timestamp time.Time
	model     string
	effort    string
	speed     string
	usage     model.Usage
	usageSet  bool
	textChars int
	toolCalls []model.ToolCall
	// compactionBefore is set when a compaction marker was seen before this
	// message.id was first assigned a turnBuilder.
	compactionBefore bool
}

// toolCallRef locates one ToolCall inside the turns being built, so that a
// later tool_result record can fill in its Agent details.
type toolCallRef struct {
	msgID string
	index int
}

// Parse reads one Claude Code transcript file and returns the Transcript it
// describes. Malformed lines and unrecognised record types are skipped.
// Parse only fails if the file cannot be opened or yields no usable
// records.
func Parse(path string) (*model.Transcript, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("claude: open %s: %w", path, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), maxLineSize)

	var sess model.Session
	sess.Path = path
	isSubAgent := strings.Contains(filepath.ToSlash(path), "/subagents/")

	var (
		order      []string // message.id insertion order
		byMsgID    = map[string]*turnBuilder{}
		toolIndex  = map[string]toolCallRef{} // tool_use_id -> location
		toolResult []model.ToolResult
		sessionID  string // sessionId field as written in the file
		sawRecord  bool   // a structurally valid user or assistant record
		// pendingCompaction is set by a compaction marker and cleared onto
		// the first turnBuilder created after it, so the flag lands on the
		// next assistant turn and only that one.
		pendingCompaction bool
	)

	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}

		var rec rawRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			continue // malformed line: skip
		}
		if rec.Type == "system" {
			// A compact_boundary is the only system record a rule needs: it
			// marks that the harness just compacted context, and the flag
			// must land on the very next assistant turn. Every other system
			// record (custom-title, last-prompt, queue-operation, ...) is
			// still skipped exactly as before.
			if rec.Subtype == "compact_boundary" {
				pendingCompaction = true
			}
			continue
		}
		if rec.Type != "user" && rec.Type != "assistant" {
			continue
		}
		sawRecord = true

		ts, tsErr := time.Parse(time.RFC3339Nano, rec.Timestamp)

		if rec.SessionID != "" {
			sessionID = rec.SessionID
		}
		if rec.CWD != "" && sess.Project == "" {
			sess.Project = rec.CWD
		}
		if rec.GitBranch != "" && sess.GitBranch == "" {
			sess.GitBranch = rec.GitBranch
		}
		if rec.Version != "" && sess.CLIVersion == "" {
			sess.CLIVersion = rec.Version
		}
		if rec.AgentID != "" && sess.AgentID == "" {
			sess.AgentID = rec.AgentID
		}
		if rec.IsSidechain && rec.AgentID != "" {
			isSubAgent = true
		}
		if tsErr == nil {
			if sess.StartedAt.IsZero() || ts.Before(sess.StartedAt) {
				sess.StartedAt = ts
			}
			if ts.After(sess.EndedAt) {
				sess.EndedAt = ts
			}
		}

		switch rec.Type {
		case "assistant":
			var am assistantMessage
			if err := json.Unmarshal(rec.Message, &am); err != nil || am.ID == "" {
				continue
			}
			if am.Model == "<synthetic>" {
				continue // a locally generated notice (API error, sleep, etc.), no tokens involved
			}
			b, ok := byMsgID[am.ID]
			if !ok {
				// message.id is unique per API response; requestId is not
				// guaranteed to be (a retried request can reuse it), so the
				// message id is the turn key.
				b = &turnBuilder{id: am.ID, model: am.Model, effort: rec.Effort}
				byMsgID[am.ID] = b
				order = append(order, am.ID)
				if pendingCompaction {
					b.compactionBefore = true
					pendingCompaction = false
				}
			}
			if tsErr == nil && (b.timestamp.IsZero() || ts.Before(b.timestamp)) {
				b.timestamp = ts
			}
			if am.Usage != nil && !b.usageSet {
				b.usage = usageFromJSON(am.Usage)
				b.speed = string(am.Usage.Speed)
				b.usageSet = true
			} else if am.Usage != nil && b.speed == "" {
				// The records sharing one message id normally repeat the
				// same usage block, speed included. If the first one to
				// carry usage did not name a speed, take it from a later
				// one rather than pricing a fast turn as standard.
				b.speed = string(am.Usage.Speed)
			}
			for _, block := range am.Content {
				switch block.Type {
				case "text":
					b.textChars += len(block.Text)
				case "tool_use":
					tc := model.ToolCall{
						ID:          block.ID,
						Name:        block.Name,
						InputChars:  len(bytes.TrimSpace(block.Input)),
						InputDigest: model.DigestInput(block.Name, block.Input),
					}
					if block.Name == "Bash" {
						var in struct {
							Command string `json:"command"`
						}
						if err := json.Unmarshal(block.Input, &in); err == nil {
							tc.Class = model.ClassifyCommand(in.Command)
						}
					}
					if block.Name == "Agent" {
						var in agentToolInput
						if err := json.Unmarshal(block.Input, &in); err == nil {
							tc.Agent = &model.AgentLaunch{
								SubagentType:   in.SubagentType,
								RequestedModel: in.Model,
								Description:    in.Description,
							}
						} else {
							tc.Agent = &model.AgentLaunch{}
						}
					}
					if block.ID != "" {
						if _, dup := toolIndex[block.ID]; dup {
							tc.ID = fmt.Sprintf("%s#%d", block.ID, len(toolIndex))
						}
					}
					b.toolCalls = append(b.toolCalls, tc)
					if block.ID != "" {
						toolIndex[block.ID] = toolCallRef{msgID: am.ID, index: len(b.toolCalls) - 1}
					}
				}
			}

		case "user":
			if rec.IsCompactSummary {
				// The summary text lives in this record's message content,
				// but that is prompt text and is never recorded; only the
				// fact that a compaction happened carries forward.
				pendingCompaction = true
			}
			var um userMessage
			if err := json.Unmarshal(rec.Message, &um); err != nil {
				continue
			}
			blocks, ok := parseToolResultBlocks(um.Content)
			if !ok {
				continue // plain string prompt: nothing to record
			}
			for _, tb := range blocks {
				if tb.Type != "tool_result" {
					continue
				}
				chars := toolResultChars(tb.Content)
				var resultTS time.Time
				if tsErr == nil {
					resultTS = ts
				}
				toolResult = append(toolResult, model.ToolResult{
					ToolCallID: tb.ToolUseID,
					Timestamp:  resultTS,
					Chars:      chars,
					IsError:    tb.IsError,
				})

				if ref, found := toolIndex[tb.ToolUseID]; found {
					if b, ok := byMsgID[ref.msgID]; ok && ref.index < len(b.toolCalls) {
						if tc := &b.toolCalls[ref.index]; tc.Agent != nil {
							fillAgentFromToolUseResult(tc.Agent, rec.ToolUseResult)
						}
					}
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("claude: read %s: %w", path, err)
	}

	// A session that only ever produced local notices (an API error, the
	// machine going to sleep) is a real session with nothing billable in it.
	// That is not a file we failed to read, and reporting it as one would be a
	// false alarm on a corpus with plenty of them. Only a file with no
	// recognisable records at all is an error.
	if !sawRecord {
		return nil, fmt.Errorf("claude: %s: not a transcript", path)
	}

	if isSubAgent && sess.AgentID == "" {
		// Older sub-agent files carry no agentId field; the file name does.
		base := strings.TrimSuffix(filepath.Base(path), ".jsonl")
		sess.AgentID = strings.TrimPrefix(base, "agent-")
	}
	if isSubAgent {
		sess.ParentSessionID = sessionID
		sess.ID = sessionID + "/agent-" + sess.AgentID
	} else {
		sess.ID = sessionID
	}
	sess.Source = model.SourceClaudeCode

	turns := make([]model.Turn, 0, len(order))
	for _, msgID := range order {
		b := byMsgID[msgID]
		turns = append(turns, model.Turn{
			SessionID:        sess.ID,
			ID:               b.id,
			Timestamp:        b.timestamp,
			Model:            b.model,
			Effort:           b.effort,
			Speed:            b.speed,
			Usage:            b.usage,
			TextChars:        b.textChars,
			ToolCalls:        b.toolCalls,
			CompactionBefore: b.compactionBefore,
		})
	}
	sort.SliceStable(turns, func(i, j int) bool {
		return turns[i].Timestamp.Before(turns[j].Timestamp)
	})

	for i := range toolResult {
		toolResult[i].SessionID = sess.ID
	}

	return &model.Transcript{
		Session:     sess,
		Turns:       turns,
		ToolResults: toolResult,
	}, nil
}

func usageFromJSON(u *usageJSON) model.Usage {
	var thinking int64
	if u.OutputTokensDetails != nil {
		thinking = u.OutputTokensDetails.ThinkingTokens
	}

	var write5m, write1h int64
	if u.CacheCreation != nil {
		write5m = u.CacheCreation.Ephemeral5m
		write1h = u.CacheCreation.Ephemeral1h
	} else {
		write5m = u.CacheCreationInputTokens
	}

	return model.Usage{
		Input:        u.InputTokens,
		CacheRead:    u.CacheReadInputTokens,
		CacheWrite5m: write5m,
		CacheWrite1h: write1h,
		Output:       u.OutputTokens,
		Thinking:     thinking,
	}
}

// parseToolResultBlocks interprets a user message's content field, which is
// either a bare string (a human prompt, ignored) or an array of
// tool_result blocks.
func parseToolResultBlocks(raw json.RawMessage) ([]toolResultBlock, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return nil, false
	}
	var blocks []toolResultBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil, false
	}
	return blocks, true
}

// toolResultChars implements the Chars sizing rule for a tool_result's
// content field: length of the string, or the sum of text-block lengths plus
// a fixed allowance per image block, or (if there are no text or image
// blocks) the length of the serialised array.
//
// An image block carries its pixels as base64, which is far longer than what
// the API charges for it: an image is billed by area, and one scaled to the
// largest size the API accepts costs about 1,600 tokens. Counting the base64
// made one screenshot look like 150,000 tokens of context, so a session full
// of screenshots was priced as if it were carrying a small library.
func toolResultChars(raw json.RawMessage) int {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return 0
	}
	if trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			return len(s)
		}
		return len(trimmed)
	}
	if trimmed[0] == '[' {
		var blocks []textBlock
		if err := json.Unmarshal(raw, &blocks); err == nil {
			total := 0
			found := false
			for _, b := range blocks {
				switch b.Type {
				case "text":
					total += len(b.Text)
					found = true
				case "image":
					total += imageResultChars
					found = true
				}
			}
			if found {
				return total
			}
		}
		return len(trimmed)
	}
	return len(trimmed)
}

// fillAgentFromToolUseResult reads the agentId/resolvedModel out of a
// record's top-level toolUseResult field, which may be an object, a bare
// string, or absent.
func fillAgentFromToolUseResult(agent *model.AgentLaunch, raw json.RawMessage) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return
	}
	var tur toolUseResultAgent
	if err := json.Unmarshal(raw, &tur); err != nil {
		return
	}
	if tur.AgentID != "" {
		agent.AgentID = tur.AgentID
	}
	if tur.ResolvedModel != "" {
		agent.ResolvedModel = tur.ResolvedModel
	}
}
