// Package config loads tallybook's optional configuration file and the
// environment overrides that shadow it. Every value has a default, so a
// missing file is not an error.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/BurntSushi/toml"
)

// Plan says how the user pays, which decides the report's currency wording.
type Plan string

const (
	PlanAPI          Plan = "api"          // pays per token: report in dollars
	PlanSubscription Plan = "subscription" // Max/Pro style plan: report share of usage
)

// Config is the fully resolved configuration.
type Config struct {
	Plan         Plan     `toml:"plan"`
	DefaultSince string   `toml:"default_since"`
	DBPath       string   `toml:"db_path"`
	ClaudeRoots  []string `toml:"claude_roots"`
	CodexRoots   []string `toml:"codex_roots"`
	Findings     Findings `toml:"findings"`
	// Prices overrides the built-in table: model id -> rate in USD per million tokens.
	Prices map[string]Rate `toml:"prices"`
}

// Rate mirrors pricing.Rate so config does not import pricing.
type Rate struct {
	Input        float64 `toml:"input"`
	CacheRead    float64 `toml:"cache_read"`
	CacheWrite5m float64 `toml:"cache_write_5m"`
	CacheWrite1h float64 `toml:"cache_write_1h"`
	Output       float64 `toml:"output"`
}

// Findings holds per-rule thresholds. Zero means "use the rule's default".
type Findings struct {
	Disabled           []string `toml:"disabled"`
	MinRuns            int      `toml:"min_runs"`             // evidence floor for per-agent findings
	ToolOutputShare    float64  `toml:"tool_output_share"`    // report when tool output exceeds this share of context
	MinCacheRebuilds   int      `toml:"min_cache_rebuilds"`   // per session
	CacheGapMinutes    int      `toml:"cache_gap_minutes"`    // gap that counts as a cache expiry
	RetryThreshold     int      `toml:"retry_threshold"`      // same tool+input repeats before it is a loop
	ThinkingRelayTurns int      `toml:"thinking_relay_turns"` // relay turns with thinking before it is reported
}

// Default returns the configuration used when no file exists.
func Default() Config {
	return Config{
		Plan:         PlanAuto,
		DefaultSince: "30d",
		DBPath:       filepath.Join(Dir(), "tallybook.db"),
		Findings: Findings{
			MinRuns:            3,
			ToolOutputShare:    0.5,
			MinCacheRebuilds:   3,
			CacheGapMinutes:    5,
			RetryThreshold:     3,
			ThinkingRelayTurns: 20,
		},
	}
}

// Dir is where tallybook keeps its config and database:
// $TALLYBOOK_DIR, else $XDG_CONFIG_HOME/tallybook, else (on Windows only,
// where XDG_CONFIG_HOME is not a convention) os.UserConfigDir()/tallybook,
// else ~/.config/tallybook.
func Dir() string {
	if d := os.Getenv("TALLYBOOK_DIR"); d != "" {
		return d
	}
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "tallybook")
	}
	if runtime.GOOS == "windows" {
		if dir, err := os.UserConfigDir(); err == nil {
			return filepath.Join(dir, "tallybook")
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".tallybook"
	}
	return filepath.Join(home, ".config", "tallybook")
}

// Path is the config file location.
func Path() string { return filepath.Join(Dir(), "config.toml") }

// Load reads Path() if it exists, applies environment overrides, and returns
// the result. A missing file yields Default().
func Load() (Config, error) {
	return LoadFrom(Path())
}

// LoadFrom is Load with an explicit file, for tests.
func LoadFrom(path string) (Config, error) {
	cfg := Default()
	if _, err := toml.DecodeFile(path, &cfg); err != nil && !errors.Is(err, os.ErrNotExist) {
		return cfg, fmt.Errorf("read %s: %w", path, err)
	}
	applyEnv(&cfg)
	if cfg.Plan != PlanAPI && cfg.Plan != PlanSubscription && cfg.Plan != PlanAuto {
		return cfg, fmt.Errorf("plan must be %q, %q or %q, got %q", PlanAuto, PlanAPI, PlanSubscription, cfg.Plan)
	}
	return cfg, nil
}

func applyEnv(cfg *Config) {
	if v := os.Getenv("TALLYBOOK_PLAN"); v != "" {
		cfg.Plan = Plan(strings.ToLower(v))
	}
	if v := os.Getenv("TALLYBOOK_DB"); v != "" {
		cfg.DBPath = v
	}
	if v := os.Getenv("TALLYBOOK_CLAUDE_ROOTS"); v != "" {
		cfg.ClaudeRoots = filepath.SplitList(v)
	}
	if v := os.Getenv("TALLYBOOK_CODEX_ROOTS"); v != "" {
		cfg.CodexRoots = filepath.SplitList(v)
	}
	cfg.DBPath = expandHome(cfg.DBPath)
	for i, r := range cfg.ClaudeRoots {
		cfg.ClaudeRoots[i] = expandHome(r)
	}
	for i, r := range cfg.CodexRoots {
		cfg.CodexRoots[i] = expandHome(r)
	}
}

func expandHome(p string) string {
	// A Windows user writes "~\\path", and leaving that literal would silently
	// create a directory named "~". Only that spelling has its separators
	// rewritten: on Unix a backslash is an ordinary character in a filename,
	// so "~/od\\d.db" must stay one file rather than becoming two directories.
	switch {
	case strings.HasPrefix(p, "~/"):
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, filepath.FromSlash(p[2:]))
		}
	case strings.HasPrefix(p, `~\`):
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, filepath.FromSlash(strings.ReplaceAll(p[2:], `\`, "/")))
		}
	}
	return p
}

// Example is the annotated file `tallybook config init` writes.
const Example = `# tallybook configuration. Every value is optional.

# "auto" reads how Claude Code is logged in. "api" shows dollars (a real bill).
# "subscription" shows share of usage, for Max/Pro plans where tokens are not
# billed one by one; dollars are then only a list-price equivalent.
plan = "auto"

# Default window for reports: 7d, 30d, 90d, all, or a date like 2026-08-01.
default_since = "30d"

# Where transcripts live. Defaults: ~/.claude/projects and ~/.codex/sessions.
# claude_roots = ["~/.claude/projects"]
# codex_roots  = ["~/.codex/sessions", "~/.codex/archived_sessions"]

[findings]
# disabled = ["thinking-on-relay"]
min_runs = 3                # do not report a per-agent finding on fewer runs
tool_output_share = 0.5     # report when tool output is more than half of context
min_cache_rebuilds = 3      # per session
cache_gap_minutes = 5       # pause that lets the prompt cache expire

# Override or pin a price, USD per million tokens.
# [prices."claude-opus-5"]
# input = 5
# cache_read = 0.5
# cache_write_5m = 6.25
# cache_write_1h = 10
# output = 25
`
