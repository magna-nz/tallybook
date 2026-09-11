<div align="center">
  <img src="docs/tallybook-mark.svg" alt="Tallybook logo" width="104" />
  <h1>Tallybook</h1>
  <p><strong>Prices every Claude Code and Codex session on your disk, shows what each agent cost over any period, and tells you in plain English what would have been cheaper.</strong></p>
  <p>
    <a href="https://github.com/magna-nz/tallybook/actions/workflows/ci.yml"><img src="https://github.com/magna-nz/tallybook/actions/workflows/ci.yml/badge.svg?branch=main" alt="CI" /></a>
    <a href="https://github.com/magna-nz/tallybook/releases/latest"><img src="https://img.shields.io/github/v/release/magna-nz/tallybook?sort=semver&label=release" alt="Latest release" /></a>
    <a href="https://modelcontextprotocol.io/"><img src="https://img.shields.io/badge/MCP-server-005FBA" alt="MCP server" /></a>
    <a href="LICENSE"><img src="https://img.shields.io/badge/License-MIT-yellow.svg" alt="MIT License" /></a>
  </p>
  <p><a href="https://magna-nz.github.io/tallybook/">Documentation</a></p>
</div>

<br />

No proxy, no API key, nothing leaves your machine. Run it as a CLI, or add `tallybook-mcp` to
Claude Code or Codex and ask the agent itself what it's spending and what would be cheaper.

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

And this week against last week:

```
$ tallybook --since 7d --compare

Scanned 9 sessions, 34 sub-agent runs (Sep 8 – Sep 11)

Last 7 days                      share list-price equiv.
  Total                           100%           $235.47
  Main session turns               81%           $191.78
  Sub-agents                       19%            $43.68
  Cache hit rate                   98%

Compared with the 7 days before (Aug 28 – Sep 4)
  Total               down 36%        $367.47 before, $235.47 now
  Main session turns  down 43%        $339.02 before, $191.78 now
  Sub-agents          up 54%          $28.45 before, $43.68 now
  Sessions            down 40%        15 before, 9 now
  Sub-agent runs      up 278%         9 before, 34 now
  Cache hit rate      about the same  99% before, 98% now
```

## Install

```sh
brew install --cask magna-nz/tap/tallybook
```

Also available via `go install github.com/magna-nz/tallybook/cmd/tallybook@latest`, or as a
[release download](https://github.com/magna-nz/tallybook/releases/latest). Works on macOS, Linux
and Windows.

Add the MCP server so your agent can check its own spend mid-session — `tallybook-mcp` ships
alongside `tallybook`, so no separate install:

```sh
claude mcp add tallybook -- tallybook-mcp   # Claude Code
```

For Codex, add it to `~/.codex/config.toml`:

```toml
[mcp_servers.tallybook]
command = "tallybook-mcp"
```

## Use

```sh
tallybook                         # this period's report: the five biggest findings
tallybook findings                # every finding, grouped by what kind of change it asks for
tallybook --since 7d --compare    # this week against last week: spend, sessions, cache hit rate
tallybook finding 1               # finding #1 in full: what happened, why, what to change
tallybook finding 1 --evidence    # the same, with the sessions behind it
tallybook finding 1 --patch       # finding #1's fix, as an applyable diff
tallybook agents                  # spend by sub-agent type, with the model and effort each ran at
tallybook sessions --sort cost    # sessions ranked by what they cost
tallybook changes                 # did a past model swap actually save money?
tallybook setup hook              # record sessions automatically as they end
```

Thirteen checks run over every report: which model short look-ups and read-only sub-agents ran on,
what effort they ran at, how much of each turn was history or repeated output, whether the prompt
cache was paid for and then wasted, and whether a subscription is paying for itself. Each one names
the setting or the habit to change, and says whether its number is measured or estimated. The full
list is in [`docs/DESIGN.md`](docs/DESIGN.md#findings).

## Docs

Full command reference, the MCP server's tools, sample output, and the privacy model live on the
**[documentation site](https://magna-nz.github.io/tallybook/)**. Parsing rules and pricing
formula are in [`docs/DESIGN.md`](docs/DESIGN.md).

## License

MIT
