package main

import (
	"github.com/magna-nz/tallybook/internal/report"
	"github.com/spf13/cobra"
)

func newReportCmd(flags *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "report",
		Short: "The default report: what you spent and the top findings",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runReport(cmd, flags)
		},
	}
	cmd.Flags().BoolVar(&flags.compare, "compare", false, "also show the window before this one, and how spend moved")
	return cmd
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

	var compare *report.Comparison
	if flags.compare {
		prior, ok, err := priorComparison(ctx)
		if err != nil {
			return err
		}
		if !ok {
			return usageErrorf("--compare needs a bounded window; --since all has nothing before it")
		}
		compare = prior
	}

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
		MinSavingUSD: ctx.cfg.Findings.MinSavingUSD,
		ReportLimit:  ctx.cfg.Findings.ReportLimit,
		SkippedFiles: ctx.ingestResult.Failed,
		Compare:      compare,
	}

	out := cmd.OutOrStdout()
	if flags.json {
		return report.ReportJSON(out, data)
	}
	return report.Report(out, data)
}
