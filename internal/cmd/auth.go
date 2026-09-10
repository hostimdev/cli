package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/hostimdev/cli/internal/client"
	"github.com/hostimdev/cli/internal/config"
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
				return fmt.Errorf("no token provided: create one in the dashboard at https://console.hostim.dev")
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
			return c.result(cmd, res("ok", "login", "", "projects", n),
				"Logged in. Token can see %d project(s).", n)
		},
	}
	cmd.Flags().StringVar(&tokenFlag, "token-value", "", "provide the token non-interactively instead of prompting")
	return cmd
}

func newLogoutCmd(c *cli) *cobra.Command {
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
			return c.result(cmd, res("ok", "logout", ""), "Logged out.")
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
			n := 0
			if resp.JSON200 != nil {
				n = len(*resp.JSON200)
			}
			if c.printer.Format == "json" {
				return c.printer.JSON(struct {
					APIURL             string `json:"apiUrl"`
					TokenSource        string `json:"tokenSource"`
					CurrentProject     string `json:"currentProject"`
					ProjectSource      string `json:"projectSource"`
					ProjectsAccessible int    `json:"projectsAccessible"`
				}{
					c.resolved.APIURL, c.resolved.TokenSource, c.resolved.Project,
					c.resolved.ProjectSource, n,
				})
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "API URL:        %s\n", c.resolved.APIURL)
			fmt.Fprintf(out, "Token source:   %s\n", dash(c.resolved.TokenSource))
			fmt.Fprintf(out, "Current project: %s", dash(c.resolved.Project))
			if c.resolved.ProjectSource != "" {
				fmt.Fprintf(out, " (%s)", c.resolved.ProjectSource)
			}
			fmt.Fprintln(out)
			fmt.Fprintf(out, "Projects:       %d accessible\n", n)
			return nil
		},
	}
}

func newUseCmd(c *cli) *cobra.Command {
	var clear bool
	var force bool
	cmd := &cobra.Command{
		Use:   "use [project]",
		Short: "Set the default project for resource commands",
		Long: "Set the default project used by resource commands when no -p/--project\n" +
			"flag or HOSTIM_PROJECT env var is given. The project is verified to\n" +
			"exist before it is saved; pass --force to skip that check (offline).\n" +
			"Pass --clear to unset it.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if clear == (len(args) == 1) {
				return fmt.Errorf("provide a project name or --clear, not both or neither")
			}
			f, err := config.Load()
			if err != nil {
				return err
			}
			if clear {
				f.CurrentProject = ""
				if err := config.Save(f); err != nil {
					return err
				}
				return c.result(cmd, res("cleared", "project", ""), "Cleared the default project.")
			}
			// Reject a typo'd name up front rather than persisting a default
			// that every later command would fail on.
			if !force {
				a, err := c.Client()
				if err != nil {
					return err
				}
				if _, err := resolveProjectID(cmd.Context(), a, args[0]); err != nil {
					return err
				}
			}
			f.CurrentProject = args[0]
			if err := config.Save(f); err != nil {
				return err
			}
			return c.result(cmd, res("ok", "project", args[0]), "Now using project %q.", args[0])
		},
	}
	cmd.Flags().BoolVar(&clear, "clear", false, "unset the default project")
	cmd.Flags().BoolVar(&force, "force", false, "skip verifying the project exists")
	return cmd
}
