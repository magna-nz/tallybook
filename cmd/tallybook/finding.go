package main

import (
	"fmt"
	"strconv"

	"github.com/magna-nz/tallybook/internal/report"
	"github.com/spf13/cobra"
)

func newFindingCmd(flags *globalFlags) *cobra.Command {
	var evidence, patch bool

	cmd := &cobra.Command{
		Use:   "finding <n>",
		Short: "One finding in full, with the evidence and the change to make",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			n, err := strconv.Atoi(args[0])
			if err != nil || n < 1 {
				return usageErrorf("finding number must be a positive integer, got %q", args[0])
			}
			return runFinding(cmd, flags, n, evidence, patch)
		},
	}
	cmd.Flags().BoolVar(&evidence, "evidence", false, "append the evidence table")
	cmd.Flags().BoolVar(&patch, "patch", false, "print only the file change as a diff")
	return cmd
}

func runFinding(cmd *cobra.Command, flags *globalFlags, n int, evidence, patch bool) error {
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

	if n > len(fs) {
		return usageErrorf("no finding %d: there are %d findings in this window", n, len(fs))
	}
	f := fs[n-1]

	if patch {
		if f.Patch == "" {
			fmt.Fprintln(cmd.ErrOrStderr(), "No file change for this finding.")
			return nil
		}
		fmt.Fprint(cmd.OutOrStdout(), f.Patch)
		return nil
	}

	mode := report.FindingMode{Plan: ctx.plan, Evidence: evidence}
	out := cmd.OutOrStdout()
	if flags.json {
		return report.FindingJSON(out, f, n, mode)
	}
	return report.Finding(out, f, n, mode)
}
