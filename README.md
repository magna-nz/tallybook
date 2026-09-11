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

```
$ tallybook

Scanned 131 sessions, 70 sub-agent runs (Aug 20 – Sep 10)

Last 30 days                          list price   share
  Total                                  $412.80    100%
  Main session turns                     $271.10     66%
  Sub-agents                             $141.70     34%

Top findings (estimated saving / month)

 1.  $58.00   Read-only sub-agents ran on Opus                 high confidence
 2.  $34.00   You asked for a cheaper model but got Opus       high confidence
 3.  $29.00   Your saved context was rebuilt mid-session       medium confidence
 4.  $21.00   Command output is filling your context           medium confidence
 5.      --   3 sessions look under-powered                    do not downgrade

Run `tallybook finding <n>` for evidence and the change to make.
```

```
$ tallybook finding 1

Read-only sub-agents ran on Opus                          saves about $58.00/month

  What happened
  41 times you launched a researcher agent and it only read files and searched.

  Why it costs money
  Opus is billed at about 2.5x the rate of Sonnet for the same tokens.

  What to change
  Open .claude/agents/researcher.md and add this line at the top:

      model: sonnet

  What to expect
  Researcher runs should cost about 40% of what they do now.
```

Numbers are illustrative. On a Max or Pro plan the dollar column becomes share of your usage.

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
compares the runs either side of it.

```
implementer: Opus 5 to Sonnet 5, 10 Sep                                 keep

  Before  3 runs   $3.14 each   7% of tool calls failed
  After   5 runs   $0.22 each   5% of tool calls failed

  About 93% cheaper per run, and nothing started failing more. Worth keeping.
```

This one is measured, not estimated. Both sides are real runs that really
happened, so it is the strongest number the tool produces.

## What it finds

Every finding has four parts: what happened, why it costs money, what to change, what to expect.
The change names the file and the line. `--patch` prints it as a diff. Tallybook never edits your
config.

## Install

```sh
brew install --cask magna-nz/tap/tallybook
```

Or:

```sh
go install github.com/magna-nz/tallybook/cmd/tallybook@latest
```

Needs Claude Code or Codex CLI with sessions in their default folders. macOS, Linux or Windows.
No API key.

## Recording sessions automatically

```sh
tallybook setup hook
```

Prints a `SessionEnd` hook for `~/.claude/settings.json`; `--write` adds it for you. It records
each session as it ends, so reports are instant, and appends a line per session to
`~/.config/tallybook/sessions.log`. Claude Code does not show a hook's output, so that file is
where the line goes.

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
