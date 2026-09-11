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
* [Configuration](#configuration)
* [Install](#install)
* [Recording sessions automatically](#recording-sessions-automatically)
* [Ask your agent mid-session](#ask-your-agent-mid-session)
* [Privacy](#privacy)
* [Documentation](#documentation)

## What it looks like

Real output from a working machine on a Max plan, which is why the share column leads and the
dollars are labelled as a list-price equivalent. On API billing the dollar column leads and the
numbers are a bill. A report shows totals, counts and session-id prefixes; no file paths, prompts or
command lines ever appear in one.

```
$ tallybook

Scanned 137 sessions, 96 sub-agent runs (Aug 20 – Sep 11)

Last 30 days                     share list-price equiv.
  Total                           100%         $1,296.73
  Main session turns               86%         $1,120.68
  Sub-agents                       14%           $176.05
  Cache hit rate                   99%

Top findings (estimated saving / month)

 1.     20%   Long sessions pay to carry their own history
              27 sessions in the last 30 days grew past 100,000 tokens of conversation.
 2.      2%   Thinking was spent on turns that did no thinking
              On 2,947 turns in the last 30 days, the model spent thinking tokens and then did no…
 3.      1%   Your saved context was rebuilt mid-session
              In 3 sessions in the last 30 days, the conversation so far was re-sent and charged…
 4.      1%   An hour of cache lifetime went unused
              In 46 sessions in the last 30 days, most of the cache writes were made with the hou…
 5.      1%   Identical tool calls were repeated
              In 9 sessions in the last 30 days, a read-only tool was called with the same input…

Also worth knowing

 8. 8 sessions look under-powered
 9. Your plan is paying for itself

2 more findings. Run `tallybook findings` to see them all.

Run `tallybook finding <n>` for evidence and the change to make.
Savings are estimated one finding at a time. Where two touch the same runs
they overlap, so they do not add up.

You are on a subscription: dollars are what this usage would cost on the API, not what you paid.
Share is the number to watch.
```

The report stops at five so it stays readable. Everything the checks found, grouped by the kind of
change each one asks for:

```
$ tallybook findings

Every finding in this window (estimated saving / month)

Move work to a cheaper model

 6.     <1%   Read-only sub-agents ran on Opus
              4 times in the last 30 days you launched a researcher agent and it only read files…
 7.     <1%   Short look-ups ran on Opus
              8 of your own sessions in the last 30 days ran to 10 turns or fewer and only read:…

Shrink what is sent every turn

 1.     20%   Long sessions pay to carry their own history
              27 sessions in the last 30 days grew past 100,000 tokens of conversation.
 5.      1%   Identical tool calls were repeated
              In 9 sessions in the last 30 days, a read-only tool was called with the same input…

Keep the prompt cache warm

 3.      1%   Your saved context was rebuilt mid-session
              In 3 sessions in the last 30 days, the conversation so far was re-sent and charged…
 4.      1%   An hour of cache lifetime went unused
              In 46 sessions in the last 30 days, most of the cache writes were made with the hou…

Lower thinking effort

 2.      2%   Thinking was spent on turns that did no thinking
              On 2,935 turns in the last 30 days, the model spent thinking tokens and then did no…

A setting is not doing what you think

 9.      --   Your plan is paying for itself
              At list price, your usage in the last 30 days works out to about $1,289.13 a month,…

Needs a stronger model or a better brief

 8.      --   8 sessions look under-powered
              Sessions 81fd6a4a, bed422e3, 086db154 and 5 more tried the same kind of action 3 or…

Run `tallybook finding <n>` for evidence and the change to make.
```

Then one finding in full, with the change to make:

```
$ tallybook finding 1

Long sessions pay to carry their own history   frees about 20% of your usage

  What happened
  27 sessions in the last 30 days grew past 100,000 tokens of conversation.
  Across them, 4,606 turns ran above that mark, and the largest conversation
  sent was 777,015 tokens.

  Why it costs money
  Every turn sends the whole conversation again, so once a session is this
  long, most of what is sent is history the task in hand no longer needs. Most
  of it comes back from the cache at the cheaper read price, and the rest is
  fresh input and cache writes, which is why it never looks like much on any
  one turn and still comes to about $522.75 a month across these sessions. The
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

Two more, for the habits rather than the settings:

```
$ tallybook finding 5

Identical tool calls were repeated              frees about 1% of your usage

  What happened
  In 9 sessions in the last 30 days, a read-only tool was called with the same
  input 3 or more times in a row, 11 repeated groups in total. The worst case:
  Read was called with the same input 28 times in session 490ddd47.

  Why it costs money
  Every call after the first puts the same result back into the conversation,
  and from then on it is carried on every later turn, even though nothing
  about it changed.

  What to change
  Habits, not settings. Add these lines to CLAUDE.md so they apply to every
  session:

    - Read a file once and quote the part you need, rather than reading it again.
    - After an edit, re-read only the changed range, not the whole file.
    - Use a sub-agent for a survey that would otherwise read the same files more than once.

  For Codex, the same three lines belong in AGENTS.md.

  What to expect
  Repeated reads of the same input should drop to zero. Sessions stay usable
  for longer because less of the context is a copy of something already sent.
```

```
$ tallybook finding 7

Short look-ups ran on Opus frees <1% of your usage, about $1.86 a month at list price

  What happened
  8 of your own sessions in the last 30 days ran to 10 turns or fewer and only
  read: they looked at files and searched, and never edited anything or ran a
  command that changed the project. These are the sessions you drove yourself,
  not the sub-agents they launched. Together they cost $3.10. In 3 of them the
  model called no tools at all: a question and an answer.

  Why it costs money
  Opus is billed at about 2.5x the rate of Sonnet for the same tokens. A
  question that takes a few file reads and a paragraph of answer is work the
  cheaper model does about as well, so on these sessions you paid the premium
  without getting anything for it.

  What to change
  This is a habit rather than a file, so there is nothing to edit. Start a
  quick look-up with `/model sonnet`, then press s to keep that choice for
  this session only, which leaves your saved default untouched.

  What to expect
  Sessions like these should cost about 40% of what they do now. If the
  answers start getting worse, switch back: there is nothing to undo.
```

And the per-agent table, which now says what effort level each agent mostly ran at:

```
$ tallybook agents

agent                 runs             model  effort  avg cost  read-only  errors  requested≠actual
implementer             52   claude-sonnet-5    high     $2.12         0%      75  0
verifier                15     claude-opus-5    high     $2.66         0%      22  0
general-purpose         20   claude-sonnet-5    high     $0.89         0%       4  0
researcher               6   claude-opus-4-8    high     $1.09       100%       0  0
claude-code-guide        2   claude-sonnet-5    high     $0.19        50%       0  0
```

## Use

| Command | Example |
|---|---|
| Report | `tallybook` |
| Last week only | `tallybook --since 7d` |
| One tool only | `tallybook --claude` or `tallybook --codex` |
| This window against the one before | `tallybook --compare` |
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

### This window against the one before

`--compare` asks the same question of the whole window. It prints the window of the same length
that ended where this one started, and says which way each figure moved:

```
$ tallybook --since 7d --compare

Scanned 9 sessions, 34 sub-agent runs (Sep 8 – Sep 11)

Last 7 days                      share list-price equiv.
  Total                           100%           $234.66
  Main session turns               81%           $190.98
  Sub-agents                       19%            $43.68
  Cache hit rate                   98%

Compared with the 7 days before (Aug 28 – Sep 4)
  Total               down 36%        $367.47 before, $234.66 now
  Main session turns  down 44%        $339.02 before, $190.98 now
  Sub-agents          up 54%          $28.45 before, $43.68 now
  Sessions            down 40%        15 before, 9 now
  Sub-agent runs      up 278%         9 before, 34 now
  Cache hit rate      about the same  99% before, 98% now
```

A move under one percent reads as "about the same", because a report that says "up 0.3%" invites a
decision the number does not support. The cache hit rate moves in points rather than percent. Every
figure here is measured. `--since all` has nothing before it, so `--compare` refuses it.

The **cache hit rate** line in every report is the share of everything sent to the model that came
back from the prompt cache rather than being processed afresh: cache reads over fresh input, cache
reads and both kinds of cache write. It is the one number that says whether caching is doing its
job. Claude Code usually sits in the high nineties; a fall means something is invalidating the
cache, and the [cache findings](#keep-the-prompt-cache-warm) say what.

## What it finds

Every finding has four parts: what happened, why it costs money, what to change, what to expect.
The change names the file and the line. `--patch` prints it as a diff. Tallybook never edits your
config.

Thirteen checks run over every report. Each has a floor below which it says nothing, so a quiet
report means nothing crossed it, not that nothing was checked. Two kinds of number appear and
they are not equally strong: a **measured** figure is a token count the API charged, priced at the
rate in force that day; an **estimated** figure prices one model's recorded tokens at another
model's rates, or assumes a fraction, and the prose says which assumption it made. Where a
cheaper-model estimate crosses a tokenizer family (Opus 4.7 and later against Sonnet 4.6 and
earlier), the finding adds a sentence naming the direction of the error and drops one step of
confidence.

### Move work to a cheaper model

**Read-only sub-agents ran on Opus or Fable** (`readonly-agent-on-strong-model`, medium). A
sub-agent run in which every tool call only looked at the project (Read, Grep, Glob, a `git
status`) and nothing edited or ran a command that changed anything. Grouped by agent type, priced
at the cheaper model's rates: estimated. Needs `min_runs` runs of that agent. The advice reads
the agent's own `.claude/agents/<name>.md` and says whether to add `model: sonnet`, change the
line it has, or leave it because it already says so. `--patch` prints the diff.

**Short look-ups ran on Opus** (`main-session-on-strong-model`, medium). The same idea for the
sessions you drive yourself: at most `main_session_turns` turns, every tool call read-only, or
no tool calls at all (a question and an answer), on the Opus or Fable tier. Estimated at the
cheaper model's rates. Needs three such sessions. The advice is a habit, `/model sonnet` and
then `s` to keep it for the session only; when more than half of your main sessions look like
this, it also suggests making the cheaper model the default in `~/.claude/settings.json`.

### A setting is not doing what you think

**You asked for a cheaper model but got Opus** (`requested-model-not-honoured`, high). The Agent
call asked for one model and the child's own turns ran on another. The saving is what those runs
would have cost at the model you asked for. Claude Code resolves a sub-agent's model at the call
site first and the agent file second; the advice says which one overrode you.

**Your plan against list price** (`subscription-break-even`). Only on a subscription, only with
`plan_price` set, and only on a window of at least 14 days. Scales the window's list-price total
to 30 days and compares it with the plan. Under the plan price it is a low-confidence saving with
a note that rate limits, bundled features and the one-hour cache default differ; over it, an
informational note that the plan is paying for itself.

### Shrink what is sent every turn

**Command output is filling your context** (`tool-output-bloat`, medium). Sessions that sent more
than 200,000 tokens of context in total, where raw tool output carried for the rest of the
session made up at least `tool_output_share` of it. Assumes trimming halves the cost of re-reading
that output: estimated. The advice is three lines for CLAUDE.md.

**Long sessions pay to carry their own history** (`long-context-tax`, medium). Every turn whose
context exceeded `long_context_tokens`. The tax on such a turn is its input-side cost (fresh
input, cache reads and cache writes, never output) scaled by the share of the context above the
threshold. That tax is measured; the claimed saving is half of it, because a fresh session still
has to be told what it needs. Needs three sessions with at least $0.50 of tax each. The advice is
`/context`, `/clear` between tasks, `/compact` at a natural break, and `autoCompactWindow` for a
lasting change.

**Identical tool calls were repeated** (`repeated-tool-calls`, medium). The same read-only tool
called with byte-identical input at least `repeat_threshold` times in one session, seen through
the salted hash described under [Privacy](#privacy). Each repeat's result is carried on every
later turn, so the cost is result tokens times the turns that followed, at the cache-read rate.
That claim is capped at what the session measurably spent on cache reads, and image results are
sized at what the API charges for an image rather than the length of their base64. Needs two
sessions and $1. The advice is habits for CLAUDE.md or AGENTS.md.

**Your sessions are compacting more than once** (`repeated-compaction`, informational). Claude
Code main sessions with two or more compactions. The summarising request is a separate call the
transcript never records, so no dollar figure is claimed; the evidence shows the context size just
before each compaction. Codex has no compaction marker to find.

### Keep the prompt cache warm

**Your saved context was rebuilt mid-session** (`cache-rebuilt-mid-session`, medium). A pause
longer than `cache_gap_minutes` followed by a turn that re-wrote at least half the previous
context to the cache. The waste is the write price less the read price for those tokens:
measured. Needs `min_cache_rebuilds` in a session. The transcript records whether each write took
the five-minute or the one-hour lifetime, so the advice knows whether `promptCacheTtl` would have
helped or whether the pauses simply ran past an hour.

**An hour of cache lifetime went unused** (`cache-1h-without-pauses`). The mirror image: sessions
where most cache writes took the one-hour lifetime and no pause between turns ever exceeded five
minutes. The premium is the one-hour write rate less the five-minute rate on those tokens:
measured. On API billing it is high confidence and the fix is `promptCacheTtl` set to `5m`. On a
subscription it is low confidence, because the hour is the default Claude Code picks within plan
usage and is not billed per token, and the finding says there is nothing to act on today.

### Lower thinking effort

**Thinking was spent on turns that did no thinking** (`thinking-on-relay`, low). Turns with one
tool call, under 200 characters of visible text, and thinking tokens: a hand-off, not an answer.
Grouped by agent; needs `thinking_relay_turns` of them. The thinking is priced at the output
rate, and the advice is the effort setting.

**Read-only sub-agents ran at high effort** (`effort-on-read-only-agents`, low). A sub-agent run
that only read, with every turn at high, xhigh or max effort. Assumes dropping to medium halves
its thinking tokens: estimated, and said so. Needs three runs of the agent. The advice is `effort:
medium` in the agent file, with a patch; never a session-wide effort change, which would slow the
main session too.

### Needs a stronger model or a better brief

**Sessions look under-powered** (`retry-loops`, informational). One tool failing
`retry_threshold` or more times inside six consecutive calls, or a quarter of at least eight
calls erroring. This is the counterweight to the downgrade findings: these sessions needed a
fuller brief or a stronger model, and moving them to a cheaper one would make it worse. Never a
saving.

### Reading the report

The default report lists the five biggest savings, then up to two notes under "Also worth
knowing", then one line counting what it left out. A saving below `min_saving_usd` a month is
counted rather than listed. `tallybook findings` lists everything, grouped as above. Both number
findings by their position in the full list, so `tallybook finding <n>` means the same thing
from either, and the printed numbers may skip. `--json` always carries every finding.

Savings are estimated one finding at a time. Two findings that touch the same runs overlap, so
they do not add up, and the report says so whenever it prints more than one.

## Configuration

`tallybook config init` writes an annotated `config.toml` to `~/.config/tallybook` (or
`$XDG_CONFIG_HOME/tallybook`, or `$TALLYBOOK_DIR`). Every key is optional.

| Key | Default | What it does |
|---|---|---|
| `plan` | `auto` | `api` reports dollars as a bill; `subscription` leads with share. `auto` reads Claude Code's login. |
| `default_since` | `30d` | The window when `--since` is not given. |
| `db_path` | `<config dir>/tallybook.db` | Where the ledger lives. |
| `claude_roots`, `codex_roots` | `~/.claude/projects`, `~/.codex/sessions` | Where transcripts are read from. |
| `[prices."<model>"]` | built-in table | Override or pin a price, USD per million tokens. |

Under `[findings]`:

| Key | Default | Used by |
|---|---|---|
| `disabled` | `[]` | Rule ids to skip entirely. |
| `min_runs` | `3` | Per-agent findings: runs before an agent is reported. |
| `tool_output_share` | `0.5` | Tool output as a share of context before it is bloat. |
| `min_cache_rebuilds` | `3` | Rebuilds in one session before it is reported. |
| `cache_gap_minutes` | `5` | A pause this long counts as a cache expiry. |
| `retry_threshold` | `3` | Errors from one tool inside six calls. |
| `thinking_relay_turns` | `20` | Relay turns with thinking, per agent. |
| `main_session_turns` | `10` | A main session this short that only read is a look-up. |
| `long_context_tokens` | `100000` | Past this, a turn is mostly carrying history. |
| `repeat_threshold` | `3` | Identical read-only calls before it is a repeat. |
| `min_saving_usd` | `1.0` | Smallest monthly saving the default report lists. |
| `report_limit` | `5` | How many findings the default report lists. |
| `plan_price` | unset | Your subscription's monthly price; turns on the break-even note. |

The plan can also be forced per run with `--currency usd|share`, and the database with `--db`.

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
| `report` | The window's total, the split by tool and by model, the cache hit rate, and the findings with their ids; with `compare` set, the window before as well |
| `finding` | One finding in full by id: the four sections, the evidence, the patch |
| `changes` | Every sub-agent model change with before, after and a verdict |
| `agents` | Spend per sub-agent type, with the model and effort level it mostly ran at |
| `sessions` | Sessions by cost or by time |
| `session` | One session turn by turn, by id or a unique prefix |
| `prices` | The price table and when it was verified |
| `refresh` | Rescan transcripts now |

The window tools (`report`, `finding`, `changes`, `agents`, `sessions`) take `since`, `project`,
`source` (`claude-code` or `codex`) and `currency`; `report` also takes `compare`; `session` takes an `id` and `currency`;
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
