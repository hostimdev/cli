package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newDomainCmd(c *cli) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "domain",
		Aliases: []string{"domains"},
		Short:   "Manage custom domains on an app",
	}
	cmd.AddCommand(domainAddCmd(c), domainRemoveCmd(c))
	return cmd
}

func domainAddCmd(c *cli) *cobra.Command {
	var app string
	cmd := &cobra.Command{
		Use:   "add <domain>",
		Short: "Add a custom domain to an app",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if app == "" {
				return fmt.Errorf("--app is required")
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
			fmt.Fprintf(cmd.OutOrStdout(), "Added domain %s to %s.\n", args[0], app)
			return nil
		},
	}
	cmd.Flags().StringVar(&app, "app", "", "app to add the domain to (required)")
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
				return fmt.Errorf("--app is required")
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
	cmd.Flags().StringVar(&app, "app", "", "app to remove the domain from (required)")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")
	return cmd
}
