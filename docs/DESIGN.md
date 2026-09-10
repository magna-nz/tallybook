# tallybook design notes

## What it is

A CLI that ingests coding-agent transcripts from disk into SQLite, prices every
model response, and produces plain-English findings about where the spend went
and what a cheaper choice would have cost. Nothing is sent anywhere.

## Sources

| Source | Where | Shape |
|---|---|---|
| Claude Code | `~/.claude/projects/<slug>/<session>.jsonl` and `<session>/subagents/agent-<id>.jsonl` | one JSON record per line; assistant records carry `message.usage` |
| Codex CLI | `~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl` (also `archived_sessions/`) | `{timestamp, type, payload}` records |

### Claude Code parsing rules

* Each content block of one API response is its own `assistant` record. They
  share `message.id` and `requestId` and **repeat the same `usage`**. Usage is
  counted once per `message.id`.
* `usage.cache_creation.ephemeral_5m_input_tokens` / `ephemeral_1h_input_tokens`
  split the cache write; `cache_creation_input_tokens` is their sum.
* `usage.output_tokens_details.thinking_tokens` is already inside `output_tokens`.
* A `tool_use` block named `Agent` is a sub-agent launch. Its `input.model` is
  the requested model. The matching `tool_result` record's `toolUseResult`
  carries `agentId` and `resolvedModel`. The child transcript is
  `<session>/subagents/agent-<agentId>.jsonl` with `isSidechain: true`.
* Records with types other than `user` / `assistant` (`custom-title`,
  `last-prompt`, `queue-operation`, ...) are skipped. Malformed lines are skipped.

### Codex parsing rules

* `session_meta.payload` gives id, cwd, cli_version, originator, optional git.
* `turn_context.payload.model` is the model for everything that follows until
  the next `turn_context`. If none has been seen, assume `gpt-5`.
* Usage comes from `token_usage_record` records (one per response id) when the
  file has any. Otherwise it is derived from `event_msg` records of type
  `token_count`: the delta between successive `info.total_token_usage` values.
  Repeated `token_count` events with an unchanged total contribute nothing.
* `cached_input_tokens` is inside `input_tokens` (subtract to get uncached).
  `reasoning_output_tokens` is inside `output_tokens`.
* Tool calls are `response_item` payloads of type `function_call`,
  `local_shell_call`, `custom_tool_call`; results are `function_call_output` /
  `custom_tool_call_output`, linked by `call_id`. `output` may be a string or
  an object with `body` and `success`.

## Pricing

A dated table per vendor. Cost per turn is
`input*base + cache_read*read + cache_write_5m*w5 + cache_write_1h*w1 + output*out`.
Codex has no TTL split; cache writes are billed at the base input rate.

## Findings

Each finding is a rule over the store. It produces: title, estimated saving,
confidence, plain-English body (what happened, why it costs, what to change,
what to expect), and an evidence table.
