// Command tallybook is a local ledger for what your coding agents cost.
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// Set by GoReleaser via -ldflags.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	if err := newRoot().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "tallybook:", err)
		var ue usageError
		if errors.As(err, &ue) {
			os.Exit(2)
		}
		os.Exit(1)
	}
}

func newRoot() *cobra.Command {
	flags := &globalFlags{}

	root := &cobra.Command{
		Use:   "tallybook",
		Short: "A local ledger for what your coding agents cost, and what you could have paid instead",
		Long: `tallybook reads the session transcripts that Claude Code and Codex already
write to disk, works out what each turn cost, and reports where the money went
and what would have been cheaper. Nothing leaves your machine.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runReport(cmd, flags)
		},
	}
	root.Version = fmt.Sprintf("%s (%s, %s)", version, commit, date)

	root.PersistentFlags().StringVar(&flags.since, "since", "", "window: 7d, 30d, 90d, all, or YYYY-MM-DD (default from config)")
	root.PersistentFlags().StringVar(&flags.project, "project", "", "restrict to one project path")
	root.PersistentFlags().BoolVar(&flags.json, "json", false, "write a JSON document instead of text")
	root.PersistentFlags().BoolVar(&flags.noIngest, "no-ingest", false, "skip scanning transcripts for new sessions")
	root.PersistentFlags().StringVar(&flags.currency, "currency", "", "override the plan's currency: usd or share")
	root.PersistentFlags().StringVar(&flags.db, "db", "", "path to the tallybook database (overrides config)")
	root.PersistentFlags().BoolVar(&flags.claude, "claude", false, "only Claude Code sessions (default: every source)")
	root.PersistentFlags().BoolVar(&flags.codex, "codex", false, "only Codex CLI sessions (default: every source)")

	root.AddCommand(newReportCmd(flags))
	root.AddCommand(newFindingCmd(flags))
	root.AddCommand(newAgentsCmd(flags))
	root.AddCommand(newSessionsCmd(flags))
	root.AddCommand(newSessionCmd(flags))
	root.AddCommand(newStatusCmd(flags))
	root.AddCommand(newPricesCmd(flags))
	root.AddCommand(newConfigCmd())

	return root
}
