---
title: Tallybook
---

# Tallybook

Prices every Claude Code and Codex session on your disk, shows what each agent cost over any
period, and tells you in plain English what would have been cheaper.

A CLI for you, and an MCP server so your agent can check its own spend mid-session.

[← Back to the repo](https://github.com/magna-nz/tallybook)

## Contents

* [What it looks like](#what-it-looks-like)
* [Use](#use)
* [Did it help?](#did-it-help)
* [What it finds](#what-it-finds)
* [Install](#install)
* [Recording sessions automatically](#recording-sessions-automatically)
* [Ask your agent mid-session](#ask-your-agent-mid-session)
* [Privacy](#privacy)
* [Documentation](#documentation)

## What it looks like

Real output from a working machine, trimmed to what the report itself shows: totals, counts and
session-id prefixes. No file paths, prompts or command lines appear in a report.

```
$ tallybook

Scanned 137 sessions, 94 sub-agent runs (Aug 20 – Sep 11)

Last 30 days                          list price   share
  Total                                $1,278.61    100%
  Main session turns                   $1,108.36     87%
  Sub-agents                             $170.25     13%

Top findings (estimated saving / month)

 1. $260.22   Long sessions pay to carry their own history     medium confidence
              27 sessions in the last 30 days grew past 100,000 tokens of conversation.
 2.  $29.74   Thinking was spent on turns that did no thinking low confidence
              On 2,925 turns in the last 30 days, the model spent thinking tokens and then did no…
 3.  $13.52   Your saved context was rebuilt mid-session       medium confidence
              In 3 sessions in the last 30 days, the conversation so far was re-sent and charged…
 4.  $10.17   An hour of cache lifetime went unused            high confidence
              In 46 sessions in the last 30 days, most of the cache writes were made with the hou…
 5.   $7.53   Identical tool calls were repeated               medium confidence
              In 9 sessions in the last 30 days, a read-only tool was called with the same input…

Also worth knowing

 8. 8 sessions look under-powered                              do not downgrade

2 more findings. Run `tallybook findings` to see them all.

Run `tallybook finding <n>` for evidence and the change to make.
Savings are estimated one finding at a time. Where two touch the same runs
they overlap, so they do not add up.

Prices are Anthropic and OpenAI list prices, verified 2026-09-10.
```

The report stops at five so it stays readable. Everything the checks found, grouped by the kind of
change each one asks for:

```
$ tallybook findings

Every finding in this window (estimated saving / month)

Move work to a cheaper model

 6.   $3.37   Read-only sub-agents ran on Opus                 medium confidence
              4 times in the last 30 days you launched a researcher agent and it only read files…
 7.   $1.86   Short look-ups ran on Opus                       medium confidence
              8 of your own sessions in the last 30 days ran to 10 turns or fewer and only read:…

Shrink what is sent every turn

 1. $260.22   Long sessions pay to carry their own history     medium confidence
              27 sessions in the last 30 days grew past 100,000 tokens of conversation.
 5.   $7.53   Identical tool calls were repeated               medium confidence
              In 9 sessions in the last 30 days, a read-only tool was called with the same input…

Keep the prompt cache warm

 3.  $13.52   Your saved context was rebuilt mid-session       medium confidence
              In 3 sessions in the last 30 days, the conversation so far was re-sent and charged…
 4.  $10.17   An hour of cache lifetime went unused            high confidence
              In 46 sessions in the last 30 days, most of the cache writes were made with the hou…

Lower thinking effort

 2.  $29.74   Thinking was spent on turns that did no thinking low confidence
              On 2,925 turns in the last 30 days, the model spent thinking tokens and then did no…

Needs a stronger model or a better brief

 8.      --   8 sessions look under-powered                    do not downgrade
              Sessions 81fd6a4a, bed422e3, 086db154 and 5 more tried the same kind of action 3 or…

Run `tallybook finding <n>` for evidence and the change to make.
```

Then one finding in full, with the change to make:

```
$ tallybook finding 1

Long sessions pay to carry their own history       saves about $260.22/month

  What happened
  27 sessions in the last 30 days grew past 100,000 tokens of conversation.
  Across them, 4,580 turns ran above that mark, and the largest conversation
  sent was 777,015 tokens.

  Why it costs money
  Every turn sends the whole conversation again, so once a session is this
  long, most of what is sent is history the task in hand no longer needs. Most
  of it comes back from the cache at the cheaper read price, and the rest is
  fresh input and cache writes, which is why it never looks like much on any
  one turn and still comes to about $520.44 a month across these sessions. The
  figure above is half of that, because a fresh session still has to be told
  what it needs before it can carry on, and being told costs something too.

  What to change
  Four things, in the order they pay off:

  - Run `/context` to see what is actually filling the window: it is often
    one big file read or one long command output.
  - Run `/clear` between unrelated tasks, rather than carrying the last
    task's history into the next one.
  - Run `/compact` at a natural break, while you can still say what matters,
    rather than waiting for the automatic one to fire in the middle of
    something.
  - For a change that lasts, lower "autoCompactWindow" in
    ~/.claude/settings.json so compaction starts earlier. It takes a token
    count from 100k to 1M, written like "150k", or "auto".

  Codex has no equivalent setting; there, starting a new session between tasks
  is the lever.

  What to expect
  The share of turns running above 100,000 tokens should fall, and sessions
  should stay quick and accurate for longer before they need clearing.
```

When the change is a line in a file, the finding names the file and the line, and `--patch` prints
it as a diff. A read-only sub-agent on Opus, say, gets `model: sonnet` for the block at the top of
its `.claude/agents/<name>.md`, after tallybook has read that file to check the line is not already
there.

On a Max or Pro plan the share column leads instead, and the dollars are labelled as what the
usage would have cost rather than what you paid.

## Use

| Command | Example |
|---|---|
| Report | `tallybook` |
| Last week only | `tallybook --since 7d` |
| One tool only | `tallybook --claude` or `tallybook --codex` |
| Every finding, grouped by what to change | `tallybook findings` |
| One finding, with evidence | `tallybook finding 1 --evidence` |
| The change as a diff | `tallybook finding 1 --patch` |
| Spend by sub-agent, with model and effort | `tallybook agents` |
| Spend by session | `tallybook sessions --sort cost` |
| One session in detail | `tallybook session 81fd6a4a` |
| Did a change help? | `tallybook changes` |
| Including ones too new to judge | `tallybook changes --all` |
| Machine-readable | `tallybook --json` |
| What was scanned | `tallybook status` |
| The price table | `tallybook prices` |
| Write a config file | `tallybook config init` |
| Record sessions automatically | `tallybook setup hook` |

`--since` takes `7d`, `30d`, `90d`, `all`, or a date. `--project <path>` limits to one repo.

The plan is read from Claude Code's login. Force it with `--currency usd|share` or
`plan = "api"` / `plan = "subscription"` in the config.

## Did it help?

`tallybook changes` finds every point where a sub-agent's model changed and compares the runs
either side of it. Runs that overlapped in time are not a change: dispatching a wave of
sub-agents with mixed models is a choice, not a switch. Changes with too few runs on one side to
judge are counted rather than printed; `--all` shows them.

```
$ tallybook changes

implementer: Opus 5 to Sonnet 5, 4 Sep                                  keep

  Before  4 runs   $1.07 each   9% of tool calls failed
  After   5 runs   $0.43 each   5% of tool calls failed

  About 60% cheaper per run, and nothing started failing more. Worth keeping.
```

This one is measured, not estimated. Both sides are real runs that really happened, so it is the
strongest number the tool produces.

## What it finds

Every finding has four parts: what happened, why it costs money, what to change, what to expect.
The change names the file and the line. `--patch` prints it as a diff. Tallybook never edits your
config.

Thirteen checks run over every report. Each has a floor below which it says nothing, so a quiet
report means nothing crossed it, not that nothing was checked.

| Check | Asks you to |
|---|---|
| Read-only sub-agents ran on Opus or Fable | Pin a cheaper model in the agent file |
| A launch asked for one model and got another | Fix the setting that overrode it |
| Short, read-only main sessions ran on the top tier | Start look-ups with `/model sonnet` |
| Read-only sub-agents ran at high or xhigh effort | Add `effort: medium` to the agent file |
| Command output and file reads filled the context | Trim output; read big files in a sub-agent |
| Long sessions paid to re-send history past 100k tokens | `/clear` between tasks; compact earlier |
| The same read-only call repeated with identical input | Read once and keep the part you need |
| A session was compacted again and again | Compact at a natural break; move big reads out |
| A pause let the prompt cache expire | Keep the cache for an hour |
| An hour of cache lifetime was paid for and never used | Drop back to five minutes |
| Thinking tokens on turns that only handed off to a tool | Lower the effort level |
| Sessions failed their way through a task | Give a fuller brief or a stronger model; not a saving |
| List-price usage against what the plan costs | Nothing, unless the plan stops paying for itself |

The last one needs `plan_price` in the config. Every threshold lives under `[findings]` in
`config.toml`; `tallybook config init` writes an annotated copy with the defaults.

Only the five biggest savings and two notes appear in the default report. `report_limit` and
`min_saving_usd` in the config change that; `tallybook findings` always shows everything.

## Install

### macOS and Linux

```sh
brew install --cask magna-nz/tap/tallybook
```

That pulls in [magna-nz/homebrew-tap](https://github.com/magna-nz/homebrew-tap) on the way, so
there is no separate `brew tap` step. Upgrade and remove with `brew upgrade --cask tallybook` and
`brew uninstall --cask tallybook`.

The build is unsigned, so macOS would otherwise refuse to run it. The cask strips the quarantine
flag on install, which is why it opens without a detour through System Settings.

### With Go

```sh
go install github.com/magna-nz/tallybook/cmd/tallybook@latest
```

Puts the binary in `$(go env GOPATH)/bin`, which needs to be on your `PATH`.

### From a release

Download the archive for your platform from the
[latest release](https://github.com/magna-nz/tallybook/releases/latest), unpack it, and put
`tallybook` somewhere on your `PATH`. macOS and Linux builds are provided for both Intel and ARM,
Windows for amd64 and arm64.

On macOS a downloaded archive is quarantined, so clear the flag before the first run:

```sh
xattr -dr com.apple.quarantine ./tallybook
```

### From source

Go 1.27 or newer.

```sh
git clone https://github.com/magna-nz/tallybook.git
cd tallybook
go build ./cmd/tallybook
```

### Requirements

Claude Code or Codex CLI, with sessions where they put them by default:
`~/.claude/projects` (or under `CLAUDE_CONFIG_DIR` if you have set it) and `~/.codex/sessions`.
macOS, Linux or Windows. No API key and no account of any kind. Tallybook only reads files that
are already on your disk.

### Check it worked

```sh
tallybook --version
tallybook --since 7d
```

## Recording sessions automatically

```sh
tallybook setup hook
```

Prints a `SessionEnd` hook for `~/.claude/settings.json`; `--write` adds it for you. It records
each session as it ends, so reports are instant, and appends a line per session to
`~/.config/tallybook/sessions.log`. Claude Code does not show a hook's output, so that file is
where the line goes.

## Ask your agent mid-session

`tallybook-mcp` serves the same ledger over the [Model Context Protocol](https://modelcontextprotocol.io/)
on stdio, so an agent can ask about its own spend, and about what to change, instead of you running
the CLI. It ships next to `tallybook` in every release and is built the same way.

```sh
claude mcp add tallybook -- tallybook-mcp
```

Codex and other MCP clients take the same command with no arguments. Once added, ask the agent
"what have my sub-agents cost this week?" or "is there anything cheaper I should be doing?" and it
will call the tools itself.

| Tool | Returns |
|---|---|
| `report` | The window's total, the split by tool and by model, and the findings with their ids |
| `finding` | One finding in full by id: the four sections, the evidence, the patch |
| `changes` | Every sub-agent model change with before, after and a verdict |
| `agents` | Spend per sub-agent type, with the model and effort level it mostly ran at |
| `sessions` | Sessions by cost or by time |
| `session` | One session turn by turn, by id or a unique prefix |
| `prices` | The price table and when it was verified |
| `refresh` | Rescan transcripts now |

The window tools (`report`, `finding`, `changes`, `agents`, `sessions`) take `since`, `project`,
`source` (`claude-code` or `codex`) and `currency`; `session` takes an `id` and `currency`;
`prices` and `refresh` take nothing. Every tool returns both prose and a structured value with the
time of the last scan. The server scans once at startup and again when a tool is called more than
a minute after the last scan, re-reading the config and plan each time. On a subscription the
structured `currency` is `list_price_equivalent`, not `usd`: the figures are what the usage would
have cost on the API, not a bill.

It is read-only apart from its own database, makes no network calls, and never returns prompt
text, tool output or command lines, because the database never holds them.

## Privacy

Read-only. Reads the frontmatter of your `.claude/agents` files to check its own advice, never
their bodies, which are prompts. Stores token counts, tool names, models, effort levels,
timestamps, project paths and a flag on turns that followed a compaction, in
`~/.config/tallybook/tallybook.db`. Never stores prompt text, tool output or command lines. No
network calls.

To spot the same tool being called with the same input over and over, the database also holds a
short hash of each call's input, salted with a random value generated when the database was
created. The input itself is never written, the salt is never shown, and the hash cannot be turned
back into a command or a path.

## Documentation

* [`DESIGN.md`](https://github.com/magna-nz/tallybook/blob/main/docs/DESIGN.md) — parsing rules, pricing formula, what is stored.
* Prices verified 2026-09-10. `tallybook prices` shows the table.

One limitation worth stating: the Codex parser was built from the Codex source and synthetic
fixtures, and has not yet been run against a real Codex session. The Claude Code side has been
run against several hundred.
