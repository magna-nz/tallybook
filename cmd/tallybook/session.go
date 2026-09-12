package main

import (
	"errors"

	"github.com/magna-nz/tallybook/internal/model"
	"github.com/magna-nz/tallybook/internal/query"
	"github.com/magna-nz/tallybook/internal/report"
	"github.com/spf13/cobra"
)

func newSessionCmd(flags *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "session <id-or-prefix>",
		Short: "One session in detail",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSession(cmd, flags, args[0])
		},
	}
}

func runSession(cmd *cobra.Command, flags *globalFlags, idOrPrefix string) error {
	ctx, err := openApp(flags)
	if err != nil {
		return err
	}
	defer ctx.close()

	// The resolver is shared with the MCP server and the web UI, so every
	// surface names the same session for the same prefix. Its input errors
	// are usage errors here, as they always were.
	id, err := query.ResolveSessionID(ctx.st, idOrPrefix)
	if err != nil {
		if errors.Is(err, query.ErrBadInput) || errors.Is(err, query.ErrNotFound) || errors.Is(err, query.ErrAmbiguous) {
			return usageError{err}
		}
		return err
	}

	row, err := ctx.st.Session(id)
	if err != nil {
		return err
	}
	var source model.Source
	if row != nil {
		source = row.Source
	}
	turns, err := ctx.st.Turns(id)
	if err != nil {
		return err
	}
	var usd float64
	for _, t := range turns {
		v, _ := ctx.prices.CostAt(t.Model, t.Usage, t.Timestamp)
		usd += v
	}

	out := cmd.OutOrStdout()
	if flags.json {
		return report.SessionJSON(out, id, turns, usd)
	}
	return report.Session(out, id, source, turns, usd)
}
