package main

import (
	"sort"

	"github.com/magna-nz/tallybook/internal/ledger"
	"github.com/magna-nz/tallybook/internal/report"
	"github.com/spf13/cobra"
)

func newSessionsCmd(flags *globalFlags) *cobra.Command {
	var limit int
	var sortBy string

	cmd := &cobra.Command{
		Use:   "sessions",
		Short: "Spend broken down by session",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if sortBy != "cost" && sortBy != "time" {
				return usageErrorf("--sort must be \"cost\" or \"time\", got %q", sortBy)
			}
			return runSessions(cmd, flags, limit, sortBy)
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 20, "maximum number of sessions to show")
	cmd.Flags().StringVar(&sortBy, "sort", "time", "sort by \"cost\" or \"time\"")
	return cmd
}

func runSessions(cmd *cobra.Command, flags *globalFlags, limit int, sortBy string) error {
	ctx, err := openApp(flags)
	if err != nil {
		return err
	}
	defer ctx.close()

	rows, err := ledger.Sessions(ctx.st, ctx.prices, ctx.filter)
	if err != nil {
		return err
	}

	switch sortBy {
	case "cost":
		sort.SliceStable(rows, func(i, j int) bool { return rows[i].USD > rows[j].USD })
	default: // "time": most recent first
		sort.SliceStable(rows, func(i, j int) bool { return rows[i].StartedAt.After(rows[j].StartedAt) })
	}

	out := cmd.OutOrStdout()
	if flags.json {
		return report.SessionsJSON(out, rows, limit)
	}
	return report.Sessions(out, rows, limit)
}
