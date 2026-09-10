package main

import (
	"github.com/magna-nz/tallybook/internal/ledger"
	"github.com/magna-nz/tallybook/internal/report"
	"github.com/spf13/cobra"
)

func newAgentsCmd(flags *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "agents",
		Short: "Spend broken down by sub-agent",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAgents(cmd, flags)
		},
	}
}

func runAgents(cmd *cobra.Command, flags *globalFlags) error {
	ctx, err := openApp(flags)
	if err != nil {
		return err
	}
	defer ctx.close()

	rows, err := ledger.Agents(ctx.st, ctx.prices, ctx.filter)
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	if flags.json {
		return report.AgentsJSON(out, rows)
	}
	return report.Agents(out, rows)
}
