package cmd

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/hostimdev/cli/api"
	"github.com/hostimdev/cli/internal/client"
	"github.com/hostimdev/cli/internal/config"
)

func newLoginCmd(c *cli) *cobra.Command {
	var tokenFlag string
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Log in with a device code, or store an API token",
		Long: "Log in by device code: the command prints a URL and a short code, you\n" +
			"approve the request in the browser, and the resulting API token is saved\n" +
			"to ~/.config/hostim/config.yml (mode 0600). The browser step works without\n" +
			"a terminal, so an agent can run this and show you the code.\n\n" +
			"Pass --token-value to store an existing API token instead (create one in\n" +
			"the dashboard at https://console.hostim.dev).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if token := strings.TrimSpace(tokenFlag); token != "" {
				return c.saveValidatedToken(cmd, token)
			}
			return c.deviceLogin(cmd)
		},
	}
	cmd.Flags().StringVar(&tokenFlag, "token-value", "", "store this API token instead of using the device login")
	return cmd
}

// saveValidatedToken checks a token against the API before persisting it, so a
// typo or a revoked token is refused up front rather than failing the next
// command.
func (c *cli) saveValidatedToken(cmd *cobra.Command, token string) error {
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
}

// deviceLogin runs the RFC 8628 device-authorization flow: start an
// authorization, show the code, then poll until the user approves in the
// browser. The instructions go to stderr so -o json leaves stdout to the single
// result object.
func (c *cli) deviceLogin(cmd *cobra.Command) error {
	anon, err := client.NewAnonymous(c.resolved.APIURL)
	if err != nil {
		return err
	}

	clientName := "hostim CLI"
	if host, err := os.Hostname(); err == nil && host != "" {
		clientName = "hostim CLI on " + host
	}

	resp, err := anon.DeviceAuthorizeWithResponse(cmd.Context(),
		api.DeviceAuthorizeJSONRequestBody{ClientName: &clientName})
	if err != nil {
		return fmt.Errorf("starting device login: %w", err)
	}
	if err := checkResp(resp.StatusCode(), resp.Body); err != nil {
		return err
	}
	if resp.JSON200 == nil {
		return errEmptyResponse
	}
	auth := resp.JSON200

	w := cmd.ErrOrStderr()
	fmt.Fprintf(w, "Open this URL in a browser:\n\n    %s\n\nand enter the code:\n\n    %s\n\n",
		auth.VerificationUri, auth.UserCode)
	fmt.Fprintln(w, "Waiting for you to approve the login. This command polls until you do,")
	fmt.Fprintln(w, "then saves the token itself - no need to poll or re-run it yourself.")

	interval := time.Duration(auth.Interval) * time.Second
	if interval <= 0 {
		interval = 5 * time.Second
	}
	deadline := time.Now().Add(time.Duration(auth.ExpiresIn) * time.Second)

	for {
		select {
		case <-cmd.Context().Done():
			return cmd.Context().Err()
		case <-time.After(interval):
		}

		tr, err := anon.DeviceTokenWithResponse(cmd.Context(),
			api.DeviceTokenJSONRequestBody{DeviceCode: auth.DeviceCode})
		if err != nil {
			return fmt.Errorf("polling device login: %w", err)
		}
		// A failing API is not a failed login: keep polling until the code
		// itself expires. Anything else (400, 401) is final.
		if tr.StatusCode() >= 500 {
			continue
		}
		if err := checkResp(tr.StatusCode(), tr.Body); err != nil {
			return err
		}
		if tr.JSON200 == nil {
			continue
		}
		switch tr.JSON200.Status {
		case api.DeviceTokenResultStatusApproved:
			if tr.JSON200.Token == nil {
				return errEmptyResponse
			}
			return c.saveValidatedToken(cmd, *tr.JSON200.Token)
		case api.DeviceTokenResultStatusDenied:
			return errors.New("device login was denied")
		case api.DeviceTokenResultStatusExpired:
			return errors.New("device login expired; run `hostim login` again")
		}
		if time.Now().After(deadline) {
			return errors.New("device login timed out; run `hostim login` again")
		}
	}
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
