package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/magna-nz/tallybook/internal/config"
	"github.com/spf13/cobra"
)

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage tallybook's configuration file",
	}
	cmd.AddCommand(newConfigInitCmd())
	cmd.AddCommand(newConfigPathCmd())
	return cmd
}

func newConfigInitCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Write an annotated config file",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			path := config.Path()

			if !force {
				if _, err := os.Stat(path); err == nil {
					return usageErrorf("%s already exists; use --force to overwrite", path)
				} else if !os.IsNotExist(err) {
					return err
				}
			}

			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return fmt.Errorf("create config directory: %w", err)
			}
			if err := os.WriteFile(path, []byte(config.Example), 0o644); err != nil {
				return fmt.Errorf("write %s: %w", path, err)
			}

			fmt.Fprintln(cmd.OutOrStdout(), path)
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing config file")
	return cmd
}

func newConfigPathCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "path",
		Short: "Print the config file location",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), config.Path())
			return nil
		},
	}
}
