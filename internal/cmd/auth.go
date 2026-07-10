package cmd

import (
	"fmt"
	"strings"

	"github.com/hostimdev/cli/internal/client"
	"github.com/hostimdev/cli/internal/config"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func newLoginCmd(c *cli) *cobra.Command {
	var tokenFlag string
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Store an API token and validate it",
		Long: "Store a Hostim API token for future commands. Create a token in the\n" +
			"dashboard, then paste it here (input is hidden). The token is validated\n" +
			"against the API and saved to ~/.config/hostim/config.yml (mode 0600).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			token := strings.TrimSpace(tokenFlag)
			if token == "" {
				fmt.Fprint(cmd.OutOrStdout(), "API token: ")
				b, err := term.ReadPassword(0)
				fmt.Fprintln(cmd.OutOrStdout())
				if err != nil {
					return fmt.Errorf("reading token: %w", err)
				}
				token = strings.TrimSpace(string(b))
			}
			if token == "" {
				return fmt.Errorf("no token provided")
			}

			// Validate against the resolved API URL before persisting.
			r := c.resolved
			r.Token = token
			cl, err := client.New(r)
			if err != nil {
				return err
			}
			resp, err := cl.GetProjectsWithResponse(cmd.Context())
			if err != nil {
				return fmt.Errorf("validating token: %w", err)
			}
			if err := checkResp(resp.StatusCode(), resp.Body); err != nil {
				return fmt.Errorf("token rejected: %w", err)
			}

			f, err := config.Load()
			if err != nil {
				return err
			}
			f.Token = token
			if c.flagAPIURL != "" {
				f.APIURL = c.flagAPIURL
			}
			if err := config.Save(f); err != nil {
				return err
			}
			n := 0
			if resp.JSON200 != nil {
				n = len(*resp.JSON200)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Logged in. Token can see %d project(s).\n", n)
			return nil
		},
	}
	cmd.Flags().StringVar(&tokenFlag, "token-value", "", "provide the token non-interactively instead of prompting")
	return cmd
}

func newLogoutCmd(_ *cli) *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Remove the stored API token",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			f, err := config.Load()
			if err != nil {
				return err
			}
			f.Token = ""
			if err := config.Save(f); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Logged out.")
			return nil
		},
	}
}

func newWhoamiCmd(c *cli) *cobra.Command {
	return &cobra.Command{
		Use:   "whoami",
		Short: "Show the active token source and accessible projects",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cl, err := c.Client()
			if err != nil {
				return err
			}
			resp, err := cl.GetProjectsWithResponse(cmd.Context())
			if err != nil {
				return err
			}
			if err := checkResp(resp.StatusCode(), resp.Body); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "API URL:        %s\n", c.resolved.APIURL)
			fmt.Fprintf(out, "Token source:   %s\n", dash(c.resolved.TokenSource))
			fmt.Fprintf(out, "Current project: %s", dash(c.resolved.Project))
			if c.resolved.ProjectSource != "" {
				fmt.Fprintf(out, " (%s)", c.resolved.ProjectSource)
			}
			fmt.Fprintln(out)
			if resp.JSON200 != nil {
				fmt.Fprintf(out, "Projects:       %d accessible\n", len(*resp.JSON200))
			}
			return nil
		},
	}
}

func newUseCmd(_ *cli) *cobra.Command {
	return &cobra.Command{
		Use:   "use <project>",
		Short: "Set the default project for resource commands",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			f, err := config.Load()
			if err != nil {
				return err
			}
			f.CurrentProject = args[0]
			if err := config.Save(f); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Now using project %q.\n", args[0])
			return nil
		},
	}
}
