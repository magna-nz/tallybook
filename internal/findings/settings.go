package findings

// Every setting name this package puts in front of a user lives here, with
// where it was checked and when.
//
// This exists because a made-up setting name once shipped. Advice that names a
// setting which does not exist is worse than no advice: the reader follows it,
// nothing happens, and they stop believing the rest of the report. The names
// below are the only ones the rules may emit, and settingsRegistry is asserted
// against the rules' own output in the tests, so a new invented name fails the
// build rather than reaching a release.
//
// When adding one: check it against the vendor's current documentation, put
// the URL and the date in the comment, and never write one down from memory.
const (
	// Claude Code sub-agent frontmatter keys.
	// code.claude.com/docs/en/sub-agents, checked 2026-09-11.
	keyAgentModel  = "model"
	keyAgentEffort = "effort"

	// Claude Code settings.json keys.
	// code.claude.com/docs/en/settings-reference, checked 2026-09-11.
	keyPromptCacheTTL         = "promptCacheTtl"
	keySubagentPromptCacheTTL = "subagentPromptCacheTtl"
	keyEffortLevel            = "effortLevel"

	// Claude Code slash commands.
	// code.claude.com/docs/en/model-config, checked 2026-09-11.
	cmdEffort = "/effort"
)

// settingsRegistry is every name above. The tests check that nothing outside
// this set appears in the advice the rules generate.
var settingsRegistry = map[string]bool{
	keyAgentModel:             true,
	keyAgentEffort:            true,
	keyPromptCacheTTL:         true,
	keySubagentPromptCacheTTL: true,
	keyEffortLevel:            true,
	cmdEffort:                 true,

	// Not settings, but they appear in advice and are not inventions:
	// the agent-file directory and the settings file itself.
	"~/.claude/settings.json": true,
	".claude/agents":          true,
}
