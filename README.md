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
    <a href="#-install">Install</a> ·
    <a href="#-quick-start">Quick start</a> ·
    <a href="#-what-you-get">Findings</a> ·
    <a href="docs/DESIGN.md">Design</a>
  </p>
</div>

<br />

## 🤔 Why

You run Claude Code and Codex CLI for hours and the only thing you see at the end is a bill, or a
rate-limit warning. Tools like `ccusage` will tell you how much you spent. They will not tell you
what for, or what it would have cost on a cheaper choice.

**Tallybook reads the session transcripts your agents already write to disk, prices every
response at list price, and prints plain-English findings about where the money went.** No proxy,
no API key, nothing leaves your machine. The first time you run it, it works retroactively over
months of history you already have sitting in `~/.claude/projects` and `~/.codex/sessions`.

> A *tally book* is the pocket ledger a rancher counts the herd in.

## ✨ What you get

* 📒 **A ledger** — every response, grouped by session, agent, model, project and day.
* 🔍 **Read-only sub-agents on a strong model** — researcher and Explore style agents that only
  Read, Grep and Glob, but ran on Opus. Tallybook recomputes what the same runs would have cost on
  a cheaper tier.
* 🎯 **Requested model not honoured** — you asked for Sonnet, the sub-agent ran on Opus. The
  likely cause: a `model:` line in the agent file wins over the call site.
* 🧠 **Cache rebuilt mid-session** — a pause longer than the prompt cache's TTL re-bills the whole
  conversation from scratch.
* 📦 **Tool output filling context** — the share of context made of raw command output and file
  dumps, re-paid on every turn that follows.
* 🔁 **Retry loops** — sessions that look under-powered because a command kept failing and getting
  retried. The finding says do **not** downgrade these.
* 💭 **Thinking spent on relay turns** — reasoning tokens burned on turns that just hand work to a
  tool and back.
* 🧾 **Plain-English findings** — every finding is four short parts: what happened, why it costs
  money, what to change, what to expect. The change names the exact file and line. `--patch`
  prints it as a diff; tallybook never writes to your config on its own.
* 🧮 **Two currencies** — dollars at list price if you pay per token, share of usage if you set
  `plan = "subscription"` for a Max or Pro plan.

Illustrative report, not live data:

```
$ tallybook

Scanned 193 sessions, 62 sub-agent runs (Jun 12 – Sep 10)

Last 30 days                    list price     share
  Total                            $412.80      100%
  Main session turns               $271.10       66%
  Sub-agents                       $141.70       34%

Top findings (estimated saving / month)

 1. $58   Read-only sub-agents ran on Opus             high confidence
          41 runs of researcher and Explore used only Read/Grep/Glob.
          Same runs on Sonnet: $12.

 2. $34   Requested model was not honoured             high confidence
 3. $29   Cache rebuilt mid-session                    medium confidence
 4. $21   Tool output is 61% of context                medium confidence
 5.  --   3 sessions look under-powered                do not downgrade

Run `tallybook finding <n>` for evidence and the change to make.
```

And the finding it points to:

```
$ tallybook finding 1

Read-only sub-agents ran on Opus                        saves about $58/month

  What happened
  41 times you launched a researcher or Explore agent and it only read
  files and searched. It never edited anything or ran a command that
  changed the project.

  Why it costs money
  Opus is billed at about two and a half times the rate of Sonnet for the
  same tokens. Reading and summarising files is work the cheaper models do
  about as well, so you pay the premium without getting the benefit.

  What to change
  Open .claude/agents/researcher.md and add this line to the block at the
  top of the file:

      model: sonnet

  What to expect
  Researcher runs should cost about 40% of what they do now. If their
  reports start missing things, switch back. The next report will show
  whether the error rate moved.
```

## 📋 Requirements

* Claude Code and/or Codex CLI, having written sessions to their default locations
  (`~/.claude/projects`, `~/.codex/sessions`).
* macOS 12+ or Linux.
* Nothing else. No API key.

## 📦 Install

### macOS and Linux

```sh
brew install --cask magna-nz/tap/tallybook
```

That pulls in [magna-nz/homebrew-tap](https://github.com/magna-nz/homebrew-tap) on the way, so
there's no separate `brew tap` step.

### Go

```sh
go install github.com/magna-nz/tallybook/cmd/tallybook@latest
```

### From a release

Download the tarball for your platform from the [latest release](https://github.com/magna-nz/tallybook/releases/latest).

### From source

```sh
git clone https://github.com/magna-nz/tallybook.git
cd tallybook
go build ./cmd/tallybook
```

## 🚀 Quick start

1. Run `tallybook`. The first run ingests everything on disk; it takes seconds, not minutes.
2. Run `tallybook finding 1` to see the evidence behind the top finding.
3. Apply the change it suggests. `tallybook finding 1 --patch` prints the config change as a diff
   so you can see it before you touch anything.
4. Next week, run `tallybook` again and check whether the number moved.

`tallybook config init` writes an annotated config to `~/.config/tallybook/config.toml`. If you're
on a Max or Pro plan rather than paying per token, set `plan = "subscription"` there and reports
switch from dollars to share of usage.

Roots and the database path can also be set with environment variables: `TALLYBOOK_DIR`,
`TALLYBOOK_CLAUDE_ROOTS`, `TALLYBOOK_CODEX_ROOTS`.

## 🖥️ Commands

This is the v0.1 surface.

| Command | What it shows |
|---|---|
| `tallybook` / `tallybook report` | The default report. Flags: `--since 7d\|30d\|all\|YYYY-MM-DD`, `--project`, `--json`, `--currency usd\|share`. |
| `tallybook finding <n>` | One finding in full, with `--evidence` and `--patch`. |
| `tallybook agents` | Spend broken down by sub-agent. |
| `tallybook sessions` | Spend broken down by session. |
| `tallybook session <id>` | One session in detail. |
| `tallybook status` | Ingest state: sources found, sessions scanned, last run. |
| `tallybook prices` | The built-in price table. |
| `tallybook config init` | Write an annotated config to `~/.config/tallybook/config.toml`. |

Planned, not yet built:

* `tallybook changes` — before/after tracking of config edits you've applied.
* `tallybook hook stop` — a one-line session summary for a Claude Code stop hook.
* A status line integration.
* `tallybook watch` — a live view while a session runs.
* `tallybook export` / `tallybook import` — move the ledger between machines.
* A local proxy, so tools that don't write transcripts to disk can feed the same ledger.

## 🔒 Privacy

Tallybook reads transcripts read-only. It stores token counts, tool names, model names,
timestamps and project paths in a local SQLite database at `~/.config/tallybook/tallybook.db`. It
never stores prompt text or tool-result text. It makes no network calls at all.

## 📚 Documentation

* [docs/DESIGN.md](docs/DESIGN.md) — parsing rules for Claude Code and Codex transcripts, and the
  pricing formula.
* Prices were last verified 2026-09-10; run `tallybook prices` to see the table tallybook actually
  uses.
