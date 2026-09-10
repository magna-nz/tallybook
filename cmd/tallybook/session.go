package main

import (
	"github.com/magna-nz/tallybook/internal/model"
	"strings"

	"github.com/magna-nz/tallybook/internal/report"
	"github.com/magna-nz/tallybook/internal/store"
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

	id, err := resolveSessionID(ctx.st, idOrPrefix)
	if err != nil {
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

// resolveSessionID finds the session with an exact id match, or the unique
// session whose id starts with idOrPrefix. Ambiguous prefixes and unknown
// ids are usage errors.
func resolveSessionID(st *store.Store, idOrPrefix string) (string, error) {
	if s, err := st.Session(idOrPrefix); err != nil {
		return "", err
	} else if s != nil {
		return s.ID, nil
	}

	rows, err := st.Sessions(store.Filter{})
	if err != nil {
		return "", err
	}

	var matches []string
	for _, r := range rows {
		if strings.HasPrefix(r.ID, idOrPrefix) {
			matches = append(matches, r.ID)
		}
	}

	switch len(matches) {
	case 0:
		return "", usageErrorf("no session matches %q", idOrPrefix)
	case 1:
		return matches[0], nil
	default:
		return "", usageErrorf("%q is ambiguous, matches: %s", idOrPrefix, strings.Join(matches, ", "))
	}
}
