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
func Execute() {
	c := &cli{}

	root := &cobra.Command{
		Use:           "hostim",
		Short:         "Manage Hostim projects, apps, databases and volumes",
		Long:          "hostim is the command-line interface to the Hostim cloud platform.\nIt talks to the public REST API at https://api.hostim.dev.",
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
		newDBCmd(c),
		newVolumesCmd(c),
		newRegionsCmd(c),
		newCompletionCmd(),
	)

	if err := root.Execute(); err != nil {
		// errAborted's message was already shown by the confirm helper; other
		// errors get the "error:" prefix.
		if !errors.Is(err, errAborted) {
			fmt.Fprintln(os.Stderr, "error: "+err.Error())
		}
		os.Exit(1)
	}
}
