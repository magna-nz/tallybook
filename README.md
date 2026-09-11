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
$ tallybook --since 7d

Scanned 4 sessions, 6 sub-agent runs (Sep 4 – Sep 10)

Last 7 days                           list price   share
  Total                                  $118.40    100%
  Main session turns                     $115.62     98%
  Sub-agents                               $2.78      2%

Top findings (estimated saving / month)

 1.  $8.58   Thinking was spent on turns that did no thinking low confidence
 2.  $1.33   Read-only sub-agents ran on Opus                 medium confidence
 3.  $1.33   You asked for a cheaper model but got Opus       high confidence

Run `tallybook finding <n>` for evidence and the change to make.
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
tallybook                    # this period's report
tallybook finding 1 --patch  # the fix, as a diff
tallybook changes            # did a past change actually help?
tallybook setup hook         # record sessions automatically
```

## Docs

Full command reference, the MCP server's tools, sample output, and the privacy model live on the
**[documentation site](https://magna-nz.github.io/tallybook/)**. Parsing rules and pricing
formula are in [`docs/DESIGN.md`](docs/DESIGN.md).

## License

MIT
