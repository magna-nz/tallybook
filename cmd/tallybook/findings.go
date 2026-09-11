package main

import (
	"github.com/magna-nz/tallybook/internal/report"
	"github.com/spf13/cobra"
)

func newFindingsCmd(flags *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "findings",
		Short: "Every finding in this window, grouped by the change it asks for",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runFindings(cmd, flags)
		},
	}
}

// runFindings lists every finding with no cap and no minimum saving. It
// takes the same path to the data as the report, so the numbers it prints
// are the numbers `tallybook finding <n>` takes.
func runFindings(cmd *cobra.Command, flags *globalFlags) error {
	ctx, err := openApp(flags)
	if err != nil {
		return err
	}
	defer ctx.close()

	_, totals, err := sessionsAndTotals(ctx)
	if err != nil {
		return err
	}
	fs, err := findingsFor(ctx, totals)
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	if flags.json {
		return report.FindingsJSON(out, fs)
	}
	return report.Findings(out, report.FindingsData{Findings: fs, Plan: ctx.plan})
}
