package main

import (
	"github.com/magna-nz/tallybook/internal/ledger"
	"github.com/magna-nz/tallybook/internal/report"
	"github.com/spf13/cobra"
)

func newChangesCmd(flags *globalFlags) *cobra.Command {
	var minRuns int
	var showAll bool

	cmd := &cobra.Command{
		Use:   "changes",
		Short: "Did a sub-agent's model change actually help?",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if minRuns < 1 {
				return usageErrorf("--min-runs must be at least 1, got %d", minRuns)
			}
			return runChanges(cmd, flags, minRuns, showAll)
		},
	}
	cmd.Flags().IntVar(&minRuns, "min-runs", 3, "runs required on each side of a change before judging it")
	cmd.Flags().BoolVar(&showAll, "all", false, "include changes with too few runs on one side to judge")
	return cmd
}

func runChanges(cmd *cobra.Command, flags *globalFlags, minRuns int, showAll bool) error {
	ctx, err := openApp(flags)
	if err != nil {
		return err
	}
	defer ctx.close()

	cs, err := ledger.Changes(ctx.st, ctx.prices, ctx.filter, minRuns)
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	if flags.json {
		return report.ChangesJSON(out, cs, showAll)
	}
	return report.Changes(out, cs, ctx.plan, showAll)
}
