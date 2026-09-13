// Package model defines the vendor-neutral shapes that every transcript parser
// produces and that the store, pricing and findings packages consume.
//
// A Transcript is one session file on disk. It yields a Session, the Turns
// (one per model response), the ToolResults the harness fed back, and the
// AgentLaunches (sub-agent spawns) the session made. Parsers never compute
// cost; they only record counts and names.
package model

import (
	"bytes"
	"crypto/sha256"
	"time"
)

// Source identifies which tool wrote the transcript.
type Source string

const (
	SourceClaudeCode Source = "claude-code"
	SourceCodex      Source = "codex"
)

// Session is one conversation file. Sub-agent transcripts are their own
// Session with ParentSessionID and AgentID set.
type Session struct {
	ID              string    // vendor session id (Claude: sessionId; Codex: session_meta.id)
	Source          Source    //
	Path            string    // absolute path of the transcript file
	Project         string    // working directory at session start
	GitBranch       string    // may be empty
	StartedAt       time.Time //
	EndedAt         time.Time // timestamp of the last record
	CLIVersion      string    // harness version string as written in the file
	ParentSessionID string    // set for sub-agent transcripts
	AgentID         string    // set for sub-agent transcripts (Claude: agentId)
}

// Usage is the token accounting for one model response. Every field is a
// count of tokens; zero means "not reported", never "free".
type Usage struct {
	Input        int64 // uncached input tokens billed at the base rate
	CacheRead    int64 // input served from the prompt cache
	CacheWrite5m int64 // input written to cache with the short TTL
	CacheWrite1h int64 // input written to cache with the long TTL
	Output       int64 // output tokens, including any thinking/reasoning tokens
	Thinking     int64 // thinking/reasoning tokens (already included in Output)
}

// Total returns every token the request touched, for context-size heuristics.
func (u Usage) Total() int64 {
	return u.Input + u.CacheRead + u.CacheWrite5m + u.CacheWrite1h + u.Output
}

// ContextTokens is the prompt size the model saw on this turn.
func (u Usage) ContextTokens() int64 {
	return u.Input + u.CacheRead + u.CacheWrite5m + u.CacheWrite1h
}

// Turn is one model response. Claude Code writes one per assistant message
// (a tool-using reply may span several records sharing a requestId; the
// parser merges those into one Turn). Codex writes one per response id.
type Turn struct {
	SessionID string
	ID        string    // vendor id: Claude requestId or message uuid; Codex response_id
	Timestamp time.Time //
	Model     string    // model id as written in the transcript
	Effort    string    // may be empty
	// Speed is the harness's speed setting for the turn ("fast", "standard",
	// or empty when the transcript does not say). Fast mode bills at a
	// premium, so a turn priced without it is priced too low.
	Speed     string
	Usage     Usage
	TextChars int        // characters of visible assistant text
	ToolCalls []ToolCall // tool invocations made in this response
	// CompactionBefore is true when a context compaction happened between
	// the previous turn and this one, so a rule can count how often, and how
	// big the context was, without the store ever holding what got compacted.
	CompactionBefore bool
}

// ToolCall is one tool invocation the model made.
type ToolCall struct {
	ID         string // vendor tool_use id / call_id
	Name       string // tool name as the harness names it (Read, Bash, exec_command, ...)
	InputChars int    // size of the serialized input
	// Class is ClassRead or ClassWrite for shell-style tools whose command
	// could be classified, else "". See ClassifyCommand.
	Class string
	// InputDigest is the plain (unsalted) digest of this call's input, from
	// DigestInput. It exists only so the store can turn it into a salted
	// hash without ever being handed the input itself; a parser sets it and
	// nothing downstream of the store ever reads it back. It never touches
	// disk.
	InputDigest [32]byte
	// InputHash is the salted, truncated hash the store computed from
	// InputDigest at ingest time, read back by Store.Turns so a rule can
	// compare it across calls. It is empty for a ToolCall that never had a
	// digest (for example one built by a test that leaves InputDigest zero),
	// and it is always the zero value on a ToolCall a parser just produced,
	// since nothing has hashed it yet.
	InputHash string
	// Agent is non-nil when this call launched a sub-agent.
	Agent *AgentLaunch
}

// DigestInput is the plain SHA-256 of a tool name, a NUL byte, and the
// trimmed serialized input that followed it. Parsers call this for every
// tool call so a finding rule can later tell whether the same tool was
// called twice with identical input, without the store ever holding the
// input that produced the match.
func DigestInput(name string, input []byte) [32]byte {
	h := sha256.New()
	h.Write([]byte(name))
	h.Write([]byte{0})
	h.Write(bytes.TrimSpace(input))
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// AgentLaunch describes a sub-agent spawn. RequestedModel is what the caller
// asked for; ResolvedModel is what the harness reported it actually used.
// Either may be empty. AgentID links to the child Session.
type AgentLaunch struct {
	SubagentType   string
	RequestedModel string
	ResolvedModel  string
	AgentID        string
	Description    string
}

// ToolResult is what the harness sent back for a tool call.
type ToolResult struct {
	SessionID  string
	ToolCallID string
	Timestamp  time.Time
	Chars      int  // size of the result content; the proxy for context bloat
	IsError    bool //
}

// Transcript is everything a parser extracts from one file.
type Transcript struct {
	Session     Session
	Turns       []Turn
	ToolResults []ToolResult
}
