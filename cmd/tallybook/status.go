package main

import (
	"github.com/magna-nz/tallybook/internal/report"
	"github.com/magna-nz/tallybook/internal/store"
	"github.com/magna-nz/tallybook/internal/transcript/claude"
	"github.com/magna-nz/tallybook/internal/transcript/codex"
	"github.com/spf13/cobra"
)

func newStatusCmd(flags *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Ingest state: sources found, sessions scanned, last run",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStatus(cmd, flags)
		},
	}
}

func runStatus(cmd *cobra.Command, flags *globalFlags) error {
	ctx, err := openApp(flags)
	if err != nil {
		return err
	}
	defer ctx.close()

	stats, err := ctx.st.Stats()
	if err != nil {
		return err
	}

	claudeRoots := ctx.cfg.ClaudeRoots
	if len(claudeRoots) == 0 {
		if root, err := claude.DefaultRoot(); err == nil {
			claudeRoots = []string{root}
		}
	}
	codexRoots := ctx.cfg.CodexRoots
	if len(codexRoots) == 0 {
		if roots, err := codex.DefaultRoots(); err == nil {
			codexRoots = roots
		}
	}

	rows, err := ctx.st.Sessions(store.Filter{})
	if err != nil {
		return err
	}
	claudeCount, codexCount := sourceCounts(rows)

	data := report.StatusData{
		DBPath:      ctx.cfg.DBPath,
		DBBytes:     stats.DBBytes,
		ClaudeRoots: claudeRoots,
		ClaudeCount: claudeCount,
		CodexRoots:  codexRoots,
		CodexCount:  codexCount,
		HaveIngest:  ctx.haveIngest,
		LastIngest:  ctx.ingestResult,
		Plan:        ctx.plan,
		PlanReason:  ctx.planWhy,
		PricesDated: ctx.prices.Dated(),
	}

	out := cmd.OutOrStdout()
	if flags.json {
		return report.StatusJSON(out, data)
	}
	return report.Status(out, data)
}

// sourceCounts returns how many ingested sessions came from each source.
func sourceCounts(rows []store.SessionRow) (claudeCount, codexCount int) {
	for _, r := range rows {
		switch r.Source {
		case "claude-code":
			claudeCount++
		case "codex":
			codexCount++
		}
	}
	return claudeCount, codexCount
}
