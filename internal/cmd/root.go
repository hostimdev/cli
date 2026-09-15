// Package cmd implements the hostim CLI command tree.
package cmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/hostimdev/cli/api"
	"github.com/hostimdev/cli/internal/client"
	"github.com/hostimdev/cli/internal/config"
	"github.com/hostimdev/cli/internal/output"
)

// version is set via -ldflags at build time (see Makefile).
var version = "dev"

// cli holds process-wide state resolved once in the root PersistentPreRunE and
// shared by every subcommand.
type cli struct {
	// raw flag values
	flagToken   string
	flagAPIURL  string
	flagProject string
	flagOutput  string

	resolved config.Resolved
	printer  output.Printer
}

// Client builds an authenticated API client, returning ErrNoToken if unset.
func (c *cli) Client() (*api.ClientWithResponses, error) {
	return client.New(c.resolved)
}

// Project returns the resolved project or an actionable error.
func (c *cli) Project() (string, error) {
	return c.resolved.RequireProject()
}

// Execute builds the command tree and runs it.
func Execute(manual string) {
	c := &cli{}
	root := newRootCmd(c, manual)

	if err := root.Execute(); err != nil {
		// A command that ran something remotely (hostim exec) reports that
		// program's exit status as ours, with no message of our own.
		var ee exitError
		if errors.As(err, &ee) {
			os.Exit(ee.code)
		}
		// errAborted's message was already shown by the confirm helper; other
		// errors get the "error:" prefix.
		if !errors.Is(err, errAborted) {
			reportError(c, os.Stderr, err)
		}
		os.Exit(1)
	}
}

// newRootCmd builds the whole command tree.
func newRootCmd(c *cli, manual string) *cobra.Command {
	root := &cobra.Command{
		Use:   "hostim",
		Short: "Manage Hostim projects, apps, databases and volumes",
		Long: "hostim is the command-line interface to the Hostim cloud platform.\n" +
			"It talks to the public REST API at https://api.hostim.dev.\n\n" +
			"Scripting this, or driving it from a coding agent? Run `hostim agent`.\n" +
			"It prints the complete manual to stdout: install, login, every command\n" +
			"with a runnable example, JSON output and environment variables.",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version,
		PersistentPreRunE: func(_ *cobra.Command, _ []string) error {
			f, err := config.Load()
			if err != nil {
				return err
			}
			c.resolved = config.Resolve(f, config.Overrides{
				Token:   c.flagToken,
				APIURL:  c.flagAPIURL,
				Project: c.flagProject,
			})
			fm, err := output.Parse(c.flagOutput)
			if err != nil {
				return err
			}
			c.printer = output.Printer{Format: fm, Out: os.Stdout}
			return nil
		},
	}

	pf := root.PersistentFlags()
	pf.StringVar(&c.flagToken, "token", "", "API token (overrides HOSTIM_TOKEN and config)")
	pf.StringVar(&c.flagAPIURL, "api-url", "", "API base URL (default https://api.hostim.dev)")
	pf.StringVarP(&c.flagProject, "project", "p", "", "project to target (overrides HOSTIM_PROJECT and current project)")
	pf.StringVarP(&c.flagOutput, "output", "o", "table", "output format: table or json")

	root.AddCommand(
		newLoginCmd(c),
		newLogoutCmd(c),
		newWhoamiCmd(c),
		newUseCmd(c),
		newDeployCmd(c),
		newTemplatesCmd(c),
		newStatusCmd(c),
		newLogsCmd(c),
		newEventsCmd(c),
		newOverviewCmd(c),
		newProjectsCmd(c),
		newAppsCmd(c),
		newEnvCmd(c),
		newDomainCmd(c),
		newExecCmd(c),
		newDBCmd(c),
		newVolumesCmd(c),
		newRegionsCmd(c),
		newCompletionCmd(),
		newAgentCmd(manual),
		newMCPCmd(c),
		newVersionCmd(),
	)
	return root
}

// newVersionCmd mirrors --version as a subcommand, because `hostim version` is
// the first thing most people (and every agent) try.
func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the CLI version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "hostim version %s\n", version)
			return err
		},
	}
}

// exitError carries an exit status for the process, in place of an error message.
type exitError struct{ code int }

func (e exitError) Error() string { return fmt.Sprintf("exit status %d", e.code) }
