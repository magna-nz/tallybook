package main

import (
	"github.com/magna-nz/tallybook/internal/config"
	"github.com/magna-nz/tallybook/internal/pricing"
	"github.com/magna-nz/tallybook/internal/report"
	"github.com/spf13/cobra"
)

func newPricesCmd(flags *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "prices",
		Short: "The built-in price table",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPrices(cmd, flags)
		},
	}
}

func runPrices(cmd *cobra.Command, flags *globalFlags) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	table := pricing.Default()
	for id, r := range cfg.Prices {
		table.Set(id, pricing.Rate{
			Input:        r.Input,
			CacheRead:    r.CacheRead,
			CacheWrite5m: r.CacheWrite5m,
			CacheWrite1h: r.CacheWrite1h,
			Output:       r.Output,
		})
	}

	out := cmd.OutOrStdout()
	if flags.json {
		return report.PricesJSON(out, table)
	}
	return report.Prices(out, table)
}
