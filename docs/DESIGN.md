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

## What is stored

Token counts, model ids, effort levels, tool names, tool-call classes
(read/write), result sizes, timestamps, session and project paths, and a
flag on each turn that followed a context compaction. Never prompt text,
tool-result text, command lines, or sub-agent task briefs: the parser reads
the Agent call's description to build the launch record, but the store writes
it as an empty string.

One more column needs explaining. `tool_calls.input_hash` is a 16-character
hash of each call's input, so a rule can see the same tool called with the
identical input again and again without the store ever holding the input. The
parser digests the input in memory; the store hashes that digest again with a
random salt it generated on first open and keeps in its `meta` table, and
writes the first 16 hex characters. Without the salt, the hash of a common
command line could be looked up from a list; with it, two databases hash the
same input differently and neither can be reversed. The salt is never printed
or returned by any query.

Tool results that are images are sized at a fixed 1,600 tokens each rather
than the length of their base64, which is roughly what the API charges and
about a hundred times less than the text would suggest. Before that, a session
of screenshots was priced as if it were carrying a small library.

## Pricing

A dated table per vendor. Cost per turn is
`input*base + cache_read*read + cache_write_5m*w5 + cache_write_1h*w1 + output*out`.
Codex has no TTL split; cache writes are billed at the base input rate.

## Measured versus estimated

Two kinds of number appear in this tool and they are not equally strong.

The ledger and `tallybook changes` are **measured**: every figure is a token
count the API charged, priced at the rate in force that day. A change compares
real runs on one model against real runs on another.

The findings' cheaper-model numbers are **estimated**: they price one model's
recorded token counts at another model's rates, which is a claim about a run
that never happened. The tokenizer section below is why that claim has a bound
on it, and why a finding says so when it crosses one.

## Tokenizers

The token counts in a transcript are the ones the API charged for, so the
ledger is exact. A counterfactual is not: pricing one model's recorded counts
at another model's rates assumes both models turn the same text into the same
number of tokens.

That holds inside a tokenizer family and not across one. Claude models from
Opus 4.7 onward (Opus 4.7, 4.8, Opus 5, Sonnet 5, Fable, Mythos) use a newer
tokenizer that produces roughly 30% more tokens for the same text than the one
Sonnet 4.6 and earlier use, Haiku 4.5 included. The exact figure depends on the
content.

`pricing.Tokenizer` names the family and `pricing.TokenizerDrift` says which
way a comparison errs. When a suggested change crosses families, the finding
keeps the arithmetic the transcript supports, adds a sentence naming the
direction of the error, and drops one step of confidence. It never scales a
number by a ratio the transcript never held.

## Advice

A finding ends in something to change, which means naming a real file and a
real setting. One invented setting name shipped: `CLAUDE_CODE_CACHE_TTL`, which
does not exist. Advice that names a setting nobody has is worse than no advice,
because the reader follows it, nothing happens, and they stop believing the
rest of the report.

Two things guard against a repeat:

* `internal/findings/settings.go` records every configuration name the advice
  may use, each with the documentation URL and the date it was checked. A test
  runs every registered rule, scans the advice they generate for anything shaped like a
  setting, and fails on any name not in that list. It recognises quoted and
  backticked keys, environment variables, indented `key: value` lines in pasted
  snippets, and backticked slash commands. The detector is tested directly
  against the string that shipped and against the shapes an earlier, narrower
  version missed.
* `internal/agentfile` reads the frontmatter of a project's own
  `.claude/agents/*.md` files, so advice is checked against the config rather
  than assumed. That is what lets a finding tell apart a file pinning the wrong
  model, one pinning nothing, one saying `inherit`, one already saying the
  right thing, and one that does not exist. Only frontmatter is read; the body
  of an agent file is a system prompt and is never touched.

Claude Code resolves a sub-agent's model at the call site first and the agent
file second. An earlier version of the advice had that backwards.

## Findings

Each finding is a rule over the store. It produces: title, estimated saving,
confidence, plain-English body (what happened, why it costs, what to change,
what to expect), and an evidence table. Every rule has a floor below which it
says nothing, and every saving is either measured from the transcript or
labelled as an estimate with the assumption stated in the prose.

The rules, by the direction of the change they ask for:

| Rule | Direction | What it looks for |
|---|---|---|
| `readonly-agent-on-strong-model` | downgrade | Sub-agents that only read, on Opus or Fable |
| `requested-model-not-honoured` | config | A launch asked for one model and the run used another |
| `main-session-on-strong-model` | downgrade | Short, read-only main sessions on the top tier |
| `effort-on-read-only-agents` | effort | Read-only sub-agents at high or xhigh effort |
| `tool-output-bloat` | context | Command output and file reads filling the context |
| `long-context-tax` | context | Turns past 100k tokens paying to re-send history |
| `repeated-tool-calls` | context | The same read-only call with the identical input, three or more times |
| `repeated-compaction` | context | Main sessions compacted two or more times |
| `cache-rebuilt-mid-session` | cache | Pauses that let the prompt cache expire |
| `cache-1h-without-pauses` | cache | One-hour cache writes in sessions that never paused |
| `thinking-on-relay` | effort | Thinking tokens on turns that only handed off to a tool |
| `retry-loops` | upgrade | Sessions that failed their way through a task; not a saving |
| `subscription-break-even` | config | List-price usage against what the plan costs; needs `plan_price` |

The default report lists the five biggest savings and up to two notes, and
counts the rest; `tallybook findings` lists everything grouped by direction.
Both number findings by their position in the full list, so `tallybook
finding <n>` means the same thing from either.
