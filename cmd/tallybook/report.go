package main

import (
	"github.com/magna-nz/tallybook/internal/report"
	"github.com/spf13/cobra"
)

func newReportCmd(flags *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "report",
		Short: "The default report: what you spent and the top findings",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runReport(cmd, flags)
		},
	}
}

func runReport(cmd *cobra.Command, flags *globalFlags) error {
	ctx, err := openApp(flags)
	if err != nil {
		return err
	}
	defer ctx.close()

	sessions, totals, err := sessionsAndTotals(ctx)
	if err != nil {
		return err
	}
	fs, err := findingsFor(ctx, totals)
	if err != nil {
		return err
	}

	earliest, latest := sessionSpan(sessions)
	mainCount, subCount := countMainAndSub(sessions)

	data := report.ReportData{
		SinceFlag:    ctx.sinceFlag,
		Window:       ctx.window,
		Earliest:     earliest,
		Latest:       latest,
		Sessions:     mainCount,
		Subagents:    subCount,
		Totals:       totals,
		Findings:     fs,
		Plan:         ctx.plan,
		PricesDated:  ctx.prices.Dated(),
		SkippedFiles: ctx.ingestResult.Failed,
	}

	out := cmd.OutOrStdout()
	if flags.json {
		return report.ReportJSON(out, data)
	}
	return report.Report(out, data)
}
