<div align="center">
  <img src="docs/tallybook-mark.svg" alt="Tallybook logo" width="104" />
  <h1>Tallybook</h1>
  <p><strong>Shows you what you overpaid Claude Code and Codex CLI for.</strong></p>
  <p>
    <a href="https://github.com/magna-nz/tallybook/actions/workflows/ci.yml"><img src="https://github.com/magna-nz/tallybook/actions/workflows/ci.yml/badge.svg?branch=main" alt="CI" /></a>
    <a href="https://github.com/magna-nz/tallybook/releases/latest"><img src="https://img.shields.io/github/v/release/magna-nz/tallybook?sort=semver&label=release" alt="Latest release" /></a>
    <img src="https://img.shields.io/badge/reads-Claude%20Code%20%7C%20Codex-blue" alt="Reads Claude Code and Codex CLI transcripts" />
    <img src="https://img.shields.io/badge/platform-macOS%20%7C%20Linux-lightgrey" alt="Runs on macOS and Linux" />
    <a href="LICENSE"><img src="https://img.shields.io/badge/License-MIT-yellow.svg" alt="MIT License" /></a>
  </p>
  <p>
    <a href="#install">Install</a> ·
    <a href="#use">Use</a> ·
    <a href="#what-it-finds">Findings</a> ·
    <a href="docs/DESIGN.md">Design</a>
  </p>
</div>

<br />

## Why

You run Claude Code or Codex for hours and all you see at the end is a bill or a rate-limit
warning. Tallybook prices every session from the transcripts already on your disk, shows what the
money went on, and what it would have cost on a cheaper choice.

No proxy, no API key, nothing leaves your machine. The first run covers every session you already
have.

## What it looks like

Real output, from a sample project rather than anyone's private history.

```
$ tallybook

Scanned 11 sessions, 15 sub-agent runs (Aug 14 – Sep 8)

Last 30 days                          list price   share
  Total                                  $503.43    100%
  Main session turns                     $494.81     98%
  Sub-agents                               $8.62      2%

Top findings (estimated saving / month)

 1.  $8.58   Thinking was spent on turns that did no thinking low confidence
             On 245 turns in the last 30 days, the model spent thinking tokens and then did noth…
 2.  $1.33   Read-only sub-agents ran on Opus                 medium confidence
             6 times in the last 30 days you launched a researcher agent and it only read files…
 3.  $1.33   You asked for a cheaper model but got Opus       high confidence
             6 times in the last 30 days your main session launched an agent and asked for Sonne…

Run `tallybook finding <n>` for evidence and the change to make.
Savings are estimated one finding at a time. Where two touch the same runs
they overlap, so they do not add up.

Prices are Anthropic and OpenAI list prices, verified 2026-09-10.
```

Then the evidence and the change to make:

```
$ tallybook finding 2

Read-only sub-agents ran on Opus                     saves about $1.33/month

  What happened
  6 times in the last 30 days you launched a researcher agent and it only read
  files and searched. It never edited anything or ran a command that changed
  the project. Between them those runs used Glob, Grep and Read, and nothing
  else.

  Why it costs money
  Opus is billed at about 2.5x the rate of Sonnet for the same tokens. Reading
  and summarising files is work the cheaper model does about as well, so you
  pay the premium without getting the benefit.

  What to change
  ~/.claude/agents/researcher.md does not pin a model. Add this line to the
  block at the top of the file:

    model: sonnet

  What to expect
  Researcher runs should cost about 40% of what they do now. If their reports
  start missing things, switch back. The next report will show whether the
  error rate moved.
```

On a Max or Pro plan the share column leads instead, and the dollars are
labelled as what the usage would have cost rather than what you paid.

## Use

| Command | Example |
|---|---|
| Report | `tallybook` |
| Last week only | `tallybook --since 7d` |
| One tool only | `tallybook --claude` or `tallybook --codex` |
| One finding, with evidence | `tallybook finding 1 --evidence` |
| The change as a diff | `tallybook finding 1 --patch` |
| Spend by sub-agent | `tallybook agents` |
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

`tallybook changes` finds every point where a sub-agent's model changed and
compares the runs either side of it. Runs that overlapped in time are not a
change: dispatching a wave of sub-agents with mixed models is a choice, not a
switch. Changes with too few runs on one side to judge are counted rather than
printed; `--all` shows them.

```
$ tallybook changes

implementer: Opus 5 to Sonnet 5, 4 Sep                                  keep

  Before  4 runs   $1.07 each   9% of tool calls failed
  After   5 runs   $0.43 each   5% of tool calls failed

  About 60% cheaper per run, and nothing started failing more. Worth keeping.
```

This one is measured, not estimated. Both sides are real runs that really
happened, so it is the strongest number the tool produces.

## What it finds

Every finding has four parts: what happened, why it costs money, what to change, what to expect.
The change names the file and the line. `--patch` prints it as a diff. Tallybook never edits your
config.

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

`tallybook-mcp` serves the same ledger over the Model Context Protocol on stdio, so an agent can
ask about its own spend instead of you running the CLI. It ships next to `tallybook` in every
release and is built the same way.

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
| `agents` | Spend per sub-agent type |
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
their bodies, which are prompts. Stores token counts, tool names, models, timestamps and project
paths in
`~/.config/tallybook/tallybook.db`. Never stores prompt text, tool output or command lines. No
network calls.


## Documentation

* [`docs/DESIGN.md`](docs/DESIGN.md) — parsing rules, pricing formula, what is stored.
* Prices verified 2026-09-10. `tallybook prices` shows the table.

One limitation worth stating: the Codex parser was built from the Codex source and synthetic
fixtures, and has not yet been run against a real Codex session. The Claude Code side has been
run against several hundred.
