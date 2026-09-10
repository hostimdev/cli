package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newAgentCmd(manual string) *cobra.Command {
	return &cobra.Command{
		Use:   "agent",
		Short: "Print the full CLI manual to stdout",
		Long: "Print the complete CLI manual: install, login, configuration, every\n" +
			"command with a runnable example, JSON output and environment variables.\n" +
			"It is the README of the CLI repository, embedded in the binary, so an\n" +
			"agent or a script gets the manual for the version it is actually running.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), manual)
			return err
		},
	}
}
