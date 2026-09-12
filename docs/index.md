---
title: Tallybook
---

# Tallybook

Tallybook prices every Claude Code and Codex session on your disk, shows what each agent cost over
any period, and says in plain English what would have been cheaper. It is a command-line tool, a
local web page (`tallybook --serve`) and an MCP server. Nothing leaves your machine.

[Repository](https://github.com/magna-nz/tallybook) ·
[Releases](https://github.com/magna-nz/tallybook/releases/latest) ·
[Design notes](https://github.com/magna-nz/tallybook/blob/main/docs/DESIGN.md)

## Contents

* [How it works](#how-it-works)
* [Installation](#installation)
* [Quick start](#quick-start)
* [CLI reference](#cli-reference)
  * [Global options](#global-options)
  * [Windows](#windows)
  * [Commands](#commands)
  * [Exit codes](#exit-codes)
* [Reading a report](#reading-a-report)
* [Web UI](#web-ui)
* [Findings reference](#findings-reference)
* [Configuration](#configuration)
  * [Config file](#config-file)
  * [Environment variables](#environment-variables)
  * [Plan detection](#plan-detection)
* [MCP server reference](#mcp-server-reference)
  * [Registering the server](#registering-the-server)
  * [Behaviour](#behaviour)
  * [Common inputs](#common-inputs)
  * [Tools](#tools)
  * [Calling the tools](#calling-the-tools)
* [Hooks](#hooks)
* [Privacy and data](#privacy-and-data)

## How it works

1. **Ingest.** Tallybook reads the transcript files Claude Code and Codex already write to disk
   and records token counts, model ids, tool names and timestamps in a SQLite ledger under your
   config directory. Files that have not changed since the last run are skipped, so repeat runs are
   fast.
2. **Price.** Every model response is priced at the vendor's list rate in force on that day, split
   across fresh input, cache reads, cache writes and output.
3. **Report.** The ledger is rolled up by window, session, sub-agent type and model.
4. **Find.** Thirteen rules run over the window and produce findings: what happened, why it costs
   money, what to change, and what to expect. Each names a real file, setting or habit.

Two kinds of number appear and they are not equally strong. A **measured** figure is a token count
the API charged, priced at the rate for that day: the ledger, `changes`, `--compare` and the cache
hit rate are all measured. An **estimated** figure prices one model's recorded tokens at another
model's rates, or assumes a fraction; every estimated finding states the assumption in its text.

Tallybook is read-only. It never edits your configuration and never makes a network call. See
[Privacy and data](#privacy-and-data) for exactly what the ledger holds.

## Installation

### Homebrew (macOS and Linux)

```sh
brew install --cask magna-nz/tap/tallybook
```

The tap is added on the way, so there is no separate `brew tap` step. Upgrade with
`brew upgrade --cask tallybook` and remove with `brew uninstall --cask tallybook`. The build is
unsigned; the cask clears the quarantine flag so macOS runs it without a trip to System Settings.

### Go

```sh
go install github.com/magna-nz/tallybook/cmd/tallybook@latest
go install github.com/magna-nz/tallybook/cmd/tallybook-mcp@latest
```

Binaries land in `$(go env GOPATH)/bin`, which must be on your `PATH`.

### Release archive

Download the archive for your platform from the
[latest release](https://github.com/magna-nz/tallybook/releases/latest), unpack it, and put
`tallybook` and `tallybook-mcp` on your `PATH`. macOS and Linux builds are provided for Intel and
ARM, Windows for amd64 and arm64. A downloaded archive is quarantined on macOS; clear it before the
first run:

```sh
xattr -dr com.apple.quarantine ./tallybook ./tallybook-mcp
```

### From source

Go 1.27 or newer.

```sh
git clone https://github.com/magna-nz/tallybook.git
cd tallybook
go build ./cmd/tallybook ./cmd/tallybook-mcp
```

### Requirements

Claude Code or Codex CLI, with sessions where they put them by default: `~/.claude/projects` (or
under `CLAUDE_CONFIG_DIR`) and `~/.codex/sessions` (or under `CODEX_HOME`). No API key and no
account.

## Quick start

```sh
tallybook --version
tallybook                       # the last 30 days, with the top five findings
tallybook findings              # every finding, grouped by the change it asks for
tallybook finding 1 --evidence  # the first finding in full, with the sessions behind it
```

## CLI reference

```
tallybook [global options] [command] [command options] [arguments]
```

With no command, `tallybook` runs `report`. Every command honours the global options.

### Global options

| Option | Value | Default | Effect |
|---|---|---|---|
| `--since` | `7d`, `30d`, `90d`, `all`, or `YYYY-MM-DD` | `default_since` from config, `30d` | The reporting window. See [Windows](#windows). |
| `--project` | path | every project | Restrict to one project directory. An exact match, or a prefix match when the path ends with `/`. |
| `--claude` | | | Only Claude Code sessions. |
| `--codex` | | | Only Codex sessions. Passing both flags, or neither, includes every source. |
| `--currency` | `usd` or `share` | detected plan | Force the currency: `usd` reports dollars as a bill, `share` reports share of usage with dollars as a list-price equivalent. See [Plan detection](#plan-detection). |
| `--json` | | | Write a JSON document instead of text. Supported by every reporting command; see the [commands table](#commands). |
| `--no-ingest` | | | Do not scan for new or changed transcripts; report from the ledger as it stands. |
| `--db` | path | `db_path` from config | The ledger database to use. |
| `--compare` | | | Report only: also show the window of the same length before this one, and how each figure moved. Refused with `--since all`. |
| `--serve` | | | Instead of printing the report, serve it as a web page on localhost and keep running until Ctrl-C. See [Web UI](#web-ui). Honours `--db` and `--no-ingest`; the page's own controls replace the other options. |
| `--port` | number | `7477` | With `--serve`: the port to listen on. `0` picks a free one. |
| `--open` | | | With `--serve`: open the page in your default browser once it is listening. |
| `-v`, `--version` | | | Print the version and exit. |
| `-h`, `--help` | | | Help for the command. |

### Windows

`--since` sets the window every reporting command reads.

| Value | Window |
|---|---|
| `7d`, `30d`, `90d` | The last N days, ending now. |
| `YYYY-MM-DD` | From that date, in local time, until now. |
| `all` | Every session in the ledger. The window's length is taken from the earliest session. |

Findings normalise their savings to 30 days from whatever window is in use, so a `7d` window
reports "per month" figures scaled from one week. `--compare` needs a bounded window; the prior
window is the same length and ends the moment this one starts.

### Commands

| Command | Arguments | Options | Prints | `--json` |
|---|---|---|---|---|
| `report` (default) | | `--compare` | Scan summary, window totals, cache hit rate, the top findings and how many more there are. | yes |
| `findings` | | | Every finding in the window, grouped by direction, numbered as in `report`. | yes |
| `finding` | `<n>` | `--evidence`, `--patch` | One finding in full: the four sections, and with `--evidence` the table of sessions or runs behind it. `--patch` prints only the unified diff of the file change, for piping to `patch` or `git apply`. | yes |
| `agents` | | | Spend per sub-agent type: runs, the model and effort it mostly ran at, average cost per run, the share of runs that only read, errored tool results, and requested-versus-actual model mismatches. | yes |
| `sessions` | | `--sort cost\|time`, `--limit N` | Sessions in the window with what each cost. `time` is most recent first (default); `cost` is most expensive first. `--limit` defaults to 20; `0` shows every row. Sub-agent runs are their own rows. | yes |
| `session` | `<id-or-prefix>` | | One session turn by turn: model, effort, token counts and cost per response, with notes for cache rebuilds and compactions, and the sub-agents it launched. The id may be any unique prefix. | yes |
| `changes` | | `--all`, `--min-runs N` | Every point where a sub-agent type's model changed, with the runs before and after compared and a verdict: keep, watch, revert, or too early. `--min-runs` (default 3) is how many runs each side needs before a verdict; `--all` also prints the changes that fall short. | yes |
| `status` | | | Where the database and transcript roots are, how many sessions each holds, what the last scan did, the detected plan and why, and when prices were verified. | yes |
| `prices` | | | The price table every figure is computed from, in USD per million tokens, including overrides from your config. | yes |
| `config init` | | `--force` | Write an annotated `config.toml` with every key and its default. Refuses to overwrite unless `--force`. | no |
| `config path` | | | Print the config file location. | no |
| `setup hook` | | `--write` | Print the Claude Code `SessionEnd` hook that records each session as it ends; `--write` merges it into `~/.claude/settings.json`. | no |
| `hook session-end` | | | The hook entry point. Reads Claude Code's JSON on stdin, records that one transcript, appends one line to `sessions.log`. Never exits non-zero. | no |
| `completion` | `bash\|zsh\|fish\|powershell` | | Shell completion script. | no |

#### Examples

```sh
# The default report for the last 7 days, dollars as a bill even on a subscription.
tallybook --since 7d --currency usd

# This week against last week.
tallybook --since 7d --compare

# Only Codex sessions since a date, as JSON, for a script.
tallybook --since 2026-08-01 --codex --json

# One repository only. The trailing slash makes it a prefix, so worktrees under it count.
tallybook --project ~/code/myapp/

# Every finding, then the third one with its evidence, then its fix as a diff.
tallybook findings
tallybook finding 3 --evidence
tallybook finding 3 --patch | git apply

# The ten most expensive sessions, then one of them by prefix.
tallybook sessions --sort cost --limit 10
tallybook session 81fd6a4a

# Did switching a sub-agent's model help? Include changes too new to judge.
tallybook changes --all --min-runs 2

# Report from the ledger without rescanning, against a different database.
tallybook --no-ingest --db /tmp/other.db status

# Write the config file, then find it.
tallybook config init
tallybook config path
```

### Exit codes

| Code | Meaning |
|---|---|
| `0` | Success. |
| `1` | A runtime error: the database could not be opened, a transcript root could not be read, and so on. The message is on stderr. |
| `2` | A usage error: an unknown `--since` value, `--currency` other than `usd` or `share`, `--compare` with `--since all`, `config init` over an existing file without `--force`. |

`hook session-end` always exits `0`, because a hook that fails would fail the session that
called it.

## Reading a report

A report has four parts, in order.

**Scan summary.** How many main sessions and sub-agent runs were found, and the span of dates they
cover.

**Totals.** The window's cost, split into main-session turns and sub-agents, and by source when
both Claude Code and Codex are present. On API billing the dollar column leads and is a bill; on a
subscription the share column leads and the dollars are labelled as a list-price equivalent. The
**cache hit rate** line is the share of everything sent to the model that came back from the prompt
cache rather than being processed afresh: cache reads over fresh input, cache reads and both kinds
of cache write. Claude Code usually sits in the high nineties; a fall means something is
invalidating the cache.

**Comparison** (with `--compare`). The window of the same length that ended where this one started,
and how each figure moved: total, main-session and sub-agent spend, session and run counts, and the
cache hit rate. A move under one percent reads as "about the same"; equal figures read "no change";
a figure that was zero reads "new". The hit rate moves in points. An empty prior window says so in
one line.

**Findings.** The five biggest savings, each with its estimated saving per 30 days, then up to two
notes under "Also worth knowing", then one line counting what was left out. A saving under
`min_saving_usd` a month is counted rather than listed. `tallybook findings` lists everything,
grouped by the kind of change each asks for. Both number findings by their position in the full
list, so `tallybook finding <n>` means the same thing from either, and the printed numbers may
skip. Savings are estimated one finding at a time; two that touch the same runs overlap and do not
add up.

## Web UI

```sh
tallybook --serve          # http://127.0.0.1:7477
tallybook --serve --open   # and open it in your browser
```

`--serve` serves the report as a single web page from the `tallybook` binary itself. It is the same
ledger, the same rules and the same numbers as the CLI and the MCP server; the page is a different
way to read them, with a scope bar in place of the global options. Every screen names the CLI
command it mirrors and has a JSON view of the response behind it.

| Screen | Mirrors | Shows |
|---|---|---|
| Overview | `tallybook`, `--compare` | The totals, cache hit rate, spend by day (from the sessions list), by model and by source, and the top findings. Tick *Compare* for the window before. |
| Findings | `findings` | Every finding, grouped by the change it asks for, numbered as in the report. |
| Finding | `finding <n> --evidence` | The four sections, the evidence table with links into sessions, and the patch with a copy button. Tallybook never applies the patch. |
| Sub-agents | `agents` | The per-agent table, with read-only-on-a-strong-model and high error counts highlighted. |
| Sessions | `sessions --sort --limit` | The session table with sub-agent runs as their own rows, linked to their parent. |
| Session | `session <id>` | Turn by turn, plus a chart of the context carried per turn marking compactions, cache rebuilds and sub-agent launches, and the runs the session launched. |
| Model changes | `changes --min-runs --all` | One card per change with the before and after runs and the verdict. |
| Status | `status` | Database, transcript roots, the last scan and its errors, plan detection, and a Rescan button. |
| Prices | `prices` | The price table, with config overrides marked. |
| Config & hook | `config path`, `setup hook --write` | The resolved config and where each value came from, which environment variables are set (never their values), and a button that installs the `SessionEnd` hook. |

The scope bar carries the window, project, source and currency, exactly like `--since`,
`--project`, `--claude`/`--codex` and `--currency`. The page rescans the transcript roots the same
way the MCP server does: on the first request, and again when a request arrives more than 60
seconds after the last scan. *Rescan* forces one. With `--no-ingest` nothing scans until you press
it, so the page reports the ledger as it stands.

The page ships three palettes, picked in the scope bar and remembered by the browser: **Embigo**,
the default, a warm-neutral dark surface with an indigo-to-violet accent shared with ShipPromptly;
and the page's own **Ledger light** and **Ledger dark**.

### What it exposes

The server binds to `127.0.0.1` only and has no authentication: anyone with a shell on the machine
can read it, which is the same trust the ledger file already has. It rejects requests whose `Host`
header is not `localhost`, `127.0.0.1` or `[::1]`, so a web page you visit cannot reach it through
DNS rebinding, and the two requests that write (`POST /api/refresh`, `POST /api/hook`) require a
request header only the page sets, so a form on another site cannot trigger them.

The JSON routes under `/api/` return the MCP tools' structured values with the same field names,
plus `/api/status` and `/api/config` for the two setup screens. They are for the page, not a
stable API; the MCP server is the interface for other programs.

Like every other command it is read-only apart from its own database, and the hook button writes
only the block `tallybook setup hook` prints, to `~/.claude/settings.json`, refusing to add it
twice.

## Findings reference

Every finding has four sections: what happened, why it costs money, what to change, what to expect.
Each rule has a floor below which it says nothing, so a quiet report means nothing crossed a floor,
not that nothing was checked. Rules can be disabled by id with the `disabled` config key.

| Id | Direction | Looks for | Number | Threshold keys | Asks you to |
|---|---|---|---|---|---|
| `readonly-agent-on-strong-model` | downgrade | Sub-agent runs whose every tool call only read, on the Opus or Fable tier | estimated at the cheaper model's rates | `min_runs` | Add `model: sonnet` (or the named alternative) to the agent file; `--patch` prints the diff |
| `main-session-on-strong-model` | downgrade | Main sessions of at most `main_session_turns` turns that only read, or called no tools, on the Opus or Fable tier | estimated | `main_session_turns` | Start look-ups with `/model sonnet`; make it the default when more than half of sessions qualify |
| `requested-model-not-honoured` | config | An Agent call asked for one model and the run used another | estimated at the requested model's rates | `min_runs` | Fix the setting that overrode the request; the advice names which one |
| `subscription-break-even` | config | List-price usage, scaled to 30 days, against `plan_price` | measured | `plan_price`; window of at least 14 days | Nothing while the plan pays for itself; consider API billing if it stops |
| `tool-output-bloat` | context | Sessions over 200k context tokens where carried tool output is at least `tool_output_share` of it | estimated: assumes trimming halves the re-read cost | `tool_output_share` | Three habits for CLAUDE.md |
| `long-context-tax` | context | Turns whose context exceeds `long_context_tokens`; the tax is the input-side cost scaled by the share above the threshold | measured tax; the saving is half of it | `long_context_tokens` | `/context`, `/clear` between tasks, `/compact` at a break, `autoCompactWindow` |
| `repeated-tool-calls` | context | The same read-only call with byte-identical input at least `repeat_threshold` times in a session | carried cost, capped at the session's measured cache-read spend | `repeat_threshold` | Habits for CLAUDE.md or AGENTS.md |
| `repeated-compaction` | context | Claude Code main sessions compacted two or more times | none; informational | | `/clear`, `/compact` at a break, sub-agents for big reads, `autoCompactWindow` |
| `cache-rebuilt-mid-session` | cache | A pause over `cache_gap_minutes` followed by a turn that re-wrote at least half the context | measured: write price less read price | `min_cache_rebuilds`, `cache_gap_minutes` | `promptCacheTtl` set to `1h`, unless the pauses already ran past an hour |
| `cache-1h-without-pauses` | cache | Sessions where one-hour cache writes dominate and no pause exceeded five minutes | measured: the one-hour premium | | `promptCacheTtl` set to `5m` on API billing; nothing on a subscription, where the hour is free within plan usage |
| `thinking-on-relay` | effort | Turns with one tool call, under 200 characters of text, and thinking tokens | measured thinking cost | `thinking_relay_turns` | Lower the effort level |
| `effort-on-read-only-agents` | effort | Sub-agent runs that only read, with every turn at high, xhigh or max effort | estimated: assumes medium halves thinking | `min_runs` | `effort: medium` in the agent file; `--patch` prints the diff |
| `retry-loops` | upgrade | One tool failing `retry_threshold` or more times inside six calls, or a quarter of at least eight calls erroring | none; informational | `retry_threshold` | A fuller brief or a stronger model; never a downgrade |

Where an estimate crosses a tokenizer family (Opus 4.7 and later against Sonnet 4.6 and earlier),
the finding adds a sentence naming the direction of the error and drops one step of confidence.
Advice only ever names settings that exist: every name is checked against the vendor's
documentation and recorded with the date, and a test fails the build on any name outside that list.

## Configuration

### Config file

Location, in order of precedence: `$TALLYBOOK_DIR/config.toml`, `$XDG_CONFIG_HOME/tallybook/config.toml`,
`~/.config/tallybook/config.toml` (on Windows, the user config directory). `tallybook config path`
prints the resolved location and `tallybook config init` writes an annotated file. Every key is
optional.

| Key | Type | Default | Effect |
|---|---|---|---|
| `plan` | `auto`, `api`, `subscription` | `auto` | How you pay. `api` reports dollars as a bill; `subscription` leads with share. `auto` detects it; see [Plan detection](#plan-detection). |
| `default_since` | window | `30d` | The window when `--since` is not given. |
| `db_path` | path | `<config dir>/tallybook.db` | Where the ledger lives. `~` is expanded. |
| `claude_roots` | list of paths | `~/.claude/projects` | Claude Code transcript directories to scan. |
| `codex_roots` | list of paths | `~/.codex/sessions`, `~/.codex/archived_sessions` | Codex transcript directories to scan. |
| `[prices."<model id>"]` | table | built-in | Override or pin a price, with `input`, `cache_read`, `cache_write_5m`, `cache_write_1h`, `output`, each USD per million tokens. |

Under `[findings]`:

| Key | Type | Default | Used by |
|---|---|---|---|
| `disabled` | list of ids | `[]` | Rules to skip entirely. |
| `min_runs` | integer | `3` | Per-agent findings: runs before an agent is reported. |
| `tool_output_share` | fraction | `0.5` | `tool-output-bloat`: tool output as a share of context. |
| `min_cache_rebuilds` | integer | `3` | `cache-rebuilt-mid-session`: rebuilds in one session. |
| `cache_gap_minutes` | integer | `5` | `cache-rebuilt-mid-session`: a pause this long counts as an expiry. |
| `retry_threshold` | integer | `3` | `retry-loops`: errors from one tool inside six calls. |
| `thinking_relay_turns` | integer | `20` | `thinking-on-relay`: relay turns with thinking, per agent. |
| `main_session_turns` | integer | `10` | `main-session-on-strong-model`: a session this short that only read is a look-up. |
| `long_context_tokens` | integer | `100000` | `long-context-tax`: past this, a turn is mostly carrying history. |
| `repeat_threshold` | integer | `3` | `repeated-tool-calls`: identical read-only calls before it is a repeat. |
| `min_saving_usd` | dollars | `1.0` | The smallest monthly saving the default report lists. |
| `report_limit` | integer | `5` | How many priced findings the default report lists. |
| `plan_price` | dollars | unset | Your subscription's monthly price; turns on `subscription-break-even`. |

A minimal file:

```toml
plan = "subscription"
default_since = "7d"

[findings]
plan_price = 200
disabled = ["thinking-on-relay"]
```

### Environment variables

Environment variables override the config file, and command-line options override both.

| Variable | Effect |
|---|---|
| `TALLYBOOK_DIR` | The config directory: config file, database and `sessions.log`. |
| `TALLYBOOK_DB` | The database path; overrides `db_path`. |
| `TALLYBOOK_PLAN` | `api` or `subscription`; overrides `plan`. |
| `TALLYBOOK_CLAUDE_ROOTS` | Claude Code transcript roots, separated by the OS path-list separator; overrides `claude_roots`. |
| `TALLYBOOK_CODEX_ROOTS` | Codex transcript roots, likewise; overrides `codex_roots`. |
| `XDG_CONFIG_HOME` | Moves the default config directory to `$XDG_CONFIG_HOME/tallybook`. |
| `CLAUDE_CONFIG_DIR` | Honoured when computing the default Claude Code root, which becomes `$CLAUDE_CONFIG_DIR/projects`. |
| `CODEX_HOME` | Honoured when computing the default Codex roots, which become `$CODEX_HOME/sessions` and `archived_sessions`. |
| `ANTHROPIC_API_KEY` | Its presence makes plan detection choose `api`. The value is never read. |

### Plan detection

With `plan = "auto"`, tallybook chooses `api` when `ANTHROPIC_API_KEY` is set, and otherwise reads
the non-secret account fields in `~/.claude.json` to see whether Claude Code is logged in with a
Max or Pro subscription. `tallybook status` shows the decision and the reason. On a subscription,
dollars are what the usage would have cost on the API, not a bill, and the share column leads;
`--currency` forces either view for one run.

## MCP server reference

`tallybook-mcp` serves the same ledger over the [Model Context Protocol](https://modelcontextprotocol.io/)
on stdio, so an agent can ask about its own spend and about what to change. It ships next to
`tallybook` in every release and takes no arguments; `tallybook-mcp --version` prints the version.

### Registering the server

Claude Code:

```sh
claude mcp add tallybook -- tallybook-mcp
```

Codex, in `~/.codex/config.toml`:

```toml
[mcp_servers.tallybook]
command = "tallybook-mcp"
```

Any other MCP client: run `tallybook-mcp` as a stdio server with no arguments. Configuration comes
from the same config file and environment variables as the CLI.

### Behaviour

* Scans the transcript roots once at startup, and again when a tool is called more than 60 seconds
  after the last scan. The config file and plan are re-read on each scan. `refresh` forces one.
* Every tool returns both prose, for the model to read, and a structured value, for a client to
  compute on. Every structured value carries `ingested_at`, `age_seconds` and, when the last scan
  failed, `scan_error`; the priced ones (all but `prices` and `refresh`) also carry `plan`,
  `plan_reason` and `currency`.
* On a subscription the structured `currency` is `list_price_equivalent`, not `usd`.
* Read-only apart from its own database. It makes no network calls and never returns prompt text,
  tool output or command lines, because the database never holds them.
* Tool annotations: every tool is marked read-only and idempotent except `refresh`, which is marked
  non-destructive and idempotent.

### Common inputs

The window tools (`report`, `finding`, `changes`, `agents`, `sessions`) share these optional
inputs. They mirror the CLI's global options.

| Input | Type | Default | Effect |
|---|---|---|---|
| `since` | string | `default_since`, `30d` | `7d`, `30d`, `90d`, `all`, or a `YYYY-MM-DD` start date. |
| `project` | string | every project | Exact match on a project directory, or a prefix when it ends with a path separator. |
| `source` | string | both | `claude-code` or `codex`. |
| `currency` | string | detected plan | `usd` or `share`. |

### Tools

| Tool | Inputs beyond the common ones | Returns |
|---|---|---|
| `report` | `compare` (bool) | `window`, `sessions`, `subagents`, `turns`, `usd`, `main_usd`, `subagent_usd`, `by_source`, `by_model`, `unknown_models`, `cache_hit_rate`, `findings` (id, title, saving_usd, share, confidence, direction), `skipped_files`; with `compare`, a `prior` object with the previous window's `window`, `sessions`, `subagents`, `usd`, `main_usd`, `subagent_usd` and `cache_hit_rate`. `compare` with `since: "all"` is an error. |
| `finding` | `id` (string, required) | One finding by its stable id: `title`, `saving_usd`, `share`, `confidence`, `direction`, `what_happened`, `why_it_costs`, `what_to_change`, `what_to_expect`, `patch` when there is a file to change, and `evidence` as `columns` and `rows`. |
| `changes` | `min_runs` (int, default 3), `include_undecided` (bool) | `changes`, each with `agent`, `from`, `to`, `at`, `before` and `after` (`runs`, `avg_usd`, `error_rate`, `avg_turns`), `verdict` and `min_runs`; plus `undecided`, the count held back. |
| `agents` | | `agents`, each with `agent`, `runs`, `model`, `effort`, `avg_usd`, `read_only_share`, `errors`, `mismatched`. |
| `sessions` | `sort` (`cost` or `time`), `limit` (int, default 20, `0` for all) | `total` and `sessions`, each with `id`, `source`, `project`, `started_at`, `ended_at`, `turns`, `tool_calls`, `tool_errors`, `usd`, `known`, and for sub-agent runs `agent_type` and `parent_session_id`. |
| `session` | `id` (string, required; a unique prefix is accepted), `currency` | `id`, `source`, `project`, `agent_type` for a sub-agent run, `started_at`, `ended_at`, `usd`, and `turns`, each with `index`, `time`, `model`, `effort`, `input`, `cache_read`, `cache_write_5m`, `cache_write_1h`, `output`, `thinking`, `text_chars`, `tool_calls`, `usd` and `known`. |
| `prices` | none | The price table and the date it was verified, including overrides from the config. |
| `refresh` | none | Rescans now: `scanned`, `ingested`, `unchanged`, `failed`, and `errors`, one message per failed file. |

Finding ids are the same as in the [findings reference](#findings-reference) and are stable across
versions.

### Calling the tools

Once the server is registered, the agent calls the tools itself. Prompts that map cleanly onto
them:

* "What have my sub-agents cost this week?" → `agents` with `since: "7d"`.
* "Is there anything cheaper I should be doing?" → `report`, then `finding` for each id.
* "Is this week up or down on last week?" → `report` with `since: "7d"` and `compare: true`.
* "Did moving the implementer to Sonnet actually save money?" → `changes`.
* "Rescan and show me the most expensive session today." → `refresh`, then `sessions` with
  `sort: "cost"`.

The same calls as JSON-RPC `tools/call` parameters:

```json
{ "name": "report",   "arguments": { "since": "7d", "compare": true } }
{ "name": "finding",  "arguments": { "since": "30d", "id": "long-context-tax" } }
{ "name": "changes",  "arguments": { "min_runs": 2, "include_undecided": true } }
{ "name": "sessions", "arguments": { "sort": "cost", "limit": 10, "project": "/home/me/code/myapp/" } }
{ "name": "session",  "arguments": { "id": "81fd6a4a" } }
{ "name": "agents",   "arguments": { "source": "claude-code", "currency": "usd" } }
{ "name": "prices",   "arguments": {} }
{ "name": "refresh",  "arguments": {} }
```

## Hooks

`tallybook setup hook` prints a Claude Code `SessionEnd` hook that records each session the moment
it ends, so the next report is instant, and appends one line per session to
`<config dir>/sessions.log` with the session's cost, the share of its context that was tool output,
and its turn count. Claude Code does not show a hook's output, which is why the line goes to a file.

```sh
tallybook setup hook          # print the JSON block to add to ~/.claude/settings.json
tallybook setup hook --write  # merge it into the file; refuses to add it twice
```

The block it adds:

```json
{
  "hooks": {
    "SessionEnd": [
      { "hooks": [ { "type": "command", "command": "tallybook hook session-end" } ] }
    ]
  }
}
```

`tallybook hook session-end` is the entry point the hook runs. It reads Claude Code's JSON from
stdin (`session_id`, `transcript_path`, `cwd`, `hook_event_name`, `reason`), records only the
transcript named, and finishes well inside the 1.5 second budget a `SessionEnd` hook gets. It never
exits non-zero and never prints on error. Run by hand with nothing on stdin, it prints the summary
line for the most recent session already in the ledger. The log is capped at 256 KB; past that, the oldest
half is dropped.

Remove the hook by deleting that entry from the `SessionEnd` list in `settings.json`.

## Privacy and data

The ledger is a SQLite file, `tallybook.db` in the config directory, holding token counts, model
ids, effort levels, tool names, tool-call classes (read or write), result sizes, timestamps,
session and project paths, and a flag on each turn that followed a compaction. It never holds
prompt text, tool output, command lines or sub-agent task briefs.

One column needs explaining. `tool_calls.input_hash` is a 16-character hash of each tool call's
input, so a rule can see the same call repeated with the same input without the input ever being
stored. The parser digests the input in memory; the store hashes that digest again with a random
salt generated when the database was created, and writes the first 16 hex characters. The salt is
never printed or returned by any query, and two databases hash the same input differently.

Tool results that are images are sized at a fixed 1,600 tokens each, which is roughly what the API
charges, rather than the length of their base64.

The only files tallybook reads outside its own directory are the transcripts under the configured
roots, the frontmatter of `.claude/agents/*.md` files (never the body, which is a prompt), the
non-secret account fields of `~/.claude.json` for plan detection, and, with `setup hook --write` or
the hook button in the web UI, `~/.claude/settings.json`. It makes no network calls; `--serve`
listens on the loopback interface only and never connects outward.

Parsing rules, the pricing formula and the tokenizer caveat are in
[`DESIGN.md`](https://github.com/magna-nz/tallybook/blob/main/docs/DESIGN.md). Prices were
verified 2026-09-10; `tallybook prices` shows the table.
