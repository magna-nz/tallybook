// Command tallybook is a local ledger for what your coding agents cost.
package main

import (
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
		os.Exit(1)
	}
}

func newRoot() *cobra.Command {
	root := &cobra.Command{
		Use:   "tallybook",
		Short: "A local ledger for what your coding agents cost, and what you could have paid instead",
		Long: `tallybook reads the session transcripts that Claude Code and Codex already
write to disk, works out what each turn cost, and reports where the money went
and what would have been cheaper. Nothing leaves your machine.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.Version = fmt.Sprintf("%s (%s, %s)", version, commit, date)
	return root
}
