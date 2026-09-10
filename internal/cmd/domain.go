package cmd

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/hostimdev/cli/api"
)

func newDomainCmd(c *cli) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "domain",
		Aliases: []string{"domains"},
		Short:   "Manage custom domains on an app",
	}
	cmd.AddCommand(domainAddCmd(c), domainRemoveCmd(c), domainStatusCmd(c))
	return cmd
}

// appFlag registers the --app/-a flag shared by every command that targets an
// app through a flag rather than a positional argument.
func appFlag(cmd *cobra.Command, app *string, usage string) {
	cmd.Flags().StringVarP(app, "app", "a", "", usage)
}

func domainAddCmd(c *cli) *cobra.Command {
	var app string
	cmd := &cobra.Command{
		Use:   "add <domain>",
		Short: "Add a custom domain to an app",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if app == "" {
				return fmt.Errorf("--app is required: list apps with `hostim apps ls`")
			}
			a, project, err := c.clientAndProject(cmd.Context())
			if err != nil {
				return err
			}
			resp, err := a.AddDomainWithResponse(cmd.Context(), project, app, args[0])
			if err != nil {
				return err
			}
			if err := checkResp(resp.StatusCode(), resp.Body); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Added domain %s to %s.\n", args[0], app)
			// Print the DNS record the domain needs, since that is the next thing
			// the user has to do and the ingress IP is otherwise only in
			// `regions get`.
			st, err := domainStatuses(cmd, c, app)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: could not read domain status: %v\n", err)
				return nil
			}
			for _, s := range st {
				if s.Domain != args[0] {
					continue
				}
				fmt.Fprintf(out, "\nPoint it at the region ingress:\n  %s  A  %s\n", s.Domain, s.ExpectedIP)
				if !s.Pointing {
					fmt.Fprintf(out, "It does not resolve there yet (currently: %s).\n", dash(joinStrings(s.ResolvedIPs)))
				}
			}
			return nil
		},
	}
	appFlag(cmd, &app, "app to add the domain to (required)")
	return cmd
}

func domainRemoveCmd(c *cli) *cobra.Command {
	var app string
	var yes bool
	cmd := &cobra.Command{
		Use:     "rm <domain>",
		Aliases: []string{"remove", "delete"},
		Short:   "Remove a custom domain from an app",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if app == "" {
				return fmt.Errorf("--app is required: list apps with `hostim apps ls`")
			}
			a, project, err := c.clientAndProject(cmd.Context())
			if err != nil {
				return err
			}
			if err := confirmYesNo(cmd, fmt.Sprintf("Remove domain %s from app %s?", args[0], app), yes); err != nil {
				return err
			}
			resp, err := a.RemoveDomainWithResponse(cmd.Context(), project, app, args[0])
			if err != nil {
				return err
			}
			if err := checkResp(resp.StatusCode(), resp.Body); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Removed domain %s from %s.\n", args[0], app)
			return nil
		},
	}
	appFlag(cmd, &app, "app to remove the domain from (required)")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")
	return cmd
}

// domainStatusCmd lists an app's custom domains with their DNS state. It doubles
// as `domain ls`: the status response already contains every attached domain.
func domainStatusCmd(c *cli) *cobra.Command {
	return &cobra.Command{
		Use:     "status <app>",
		Aliases: []string{"ls", "list"},
		Short:   "List an app's custom domains and their DNS status",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := domainStatuses(cmd, c, args[0])
			if err != nil {
				return err
			}
			rows := make([][]string, 0, len(st))
			for _, s := range st {
				rows = append(rows, []string{
					s.Domain,
					strconv.FormatBool(s.Pointing),
					s.ExpectedIP,
					dash(joinStrings(s.ResolvedIPs)),
				})
			}
			return c.printer.Render(st, []string{"DOMAIN", "POINTING", "EXPECTED IP", "RESOLVES TO"}, rows)
		},
	}
}

func domainStatuses(cmd *cobra.Command, c *cli, app string) ([]api.DomainStatus, error) {
	a, project, err := c.clientAndProject(cmd.Context())
	if err != nil {
		return nil, err
	}
	resp, err := a.GetDomainStatusWithResponse(cmd.Context(), project, app)
	if err != nil {
		return nil, err
	}
	if err := checkResp(resp.StatusCode(), resp.Body); err != nil {
		return nil, err
	}
	if resp.JSON200 == nil {
		return nil, nil
	}
	return *resp.JSON200, nil
}
