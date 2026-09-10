package cmd

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/hostimdev/cli/api"
)

func newAppsCmd(c *cli) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "apps",
		Aliases: []string{"app"},
		Short:   "Manage apps",
	}
	cmd.AddCommand(
		appsListCmd(c),
		appsGetCmd(c),
		newDeployCmd(c), // apps deploy (also aliased top-level)
		appsRemoveCmd(c),
		appsRebuildCmd(c),
		appsRestartCmd(c),
		newStatusCmd(c), // apps status (also aliased top-level)
	)
	return cmd
}

func appsListCmd(c *cli) *cobra.Command {
	return &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List apps in the current project",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cl, project, err := c.clientAndProject(cmd.Context())
			if err != nil {
				return err
			}
			resp, err := cl.GetAppsWithResponse(cmd.Context(), project)
			if err != nil {
				return err
			}
			if err := checkResp(resp.StatusCode(), resp.Body); err != nil {
				return err
			}
			var apps []api.App
			if resp.JSON200 != nil {
				apps = *resp.JSON200
			}
			rows := make([][]string, 0, len(apps))
			for _, a := range apps {
				rows = append(rows, []string{
					a.Name,
					string(a.DeploymentSource.Type),
					strconv.Itoa(a.Replicas),
					strconv.FormatBool(a.Public),
					a.Plan,
					dash(str(a.BuiltInDomain)),
				})
			}
			return c.printer.Render(apps,
				[]string{"NAME", "SOURCE", "REPLICAS", "PUBLIC", "PLAN", "DOMAIN"}, rows)
		},
	}
}

func appsGetCmd(c *cli) *cobra.Command {
	return &cobra.Command{
		Use:   "get <app>",
		Short: "Show an app",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, project, err := c.clientAndProject(cmd.Context())
			if err != nil {
				return err
			}
			resp, err := cl.GetAppWithResponse(cmd.Context(), project, args[0])
			if err != nil {
				return err
			}
			if err := checkResp(resp.StatusCode(), resp.Body); err != nil {
				return err
			}
			a := resp.JSON200
			if a == nil {
				return errEmptyResponse
			}
			rows := [][]string{
				{"Name", a.Name},
				{"Source", string(a.DeploymentSource.Type)},
				{"Replicas", strconv.Itoa(a.Replicas)},
				{"Public", strconv.FormatBool(a.Public)},
				{"Plan", a.Plan},
				{"Built-in domain", dash(str(a.BuiltInDomain))},
				{"Domains", dash(joinStrings(a.Domains))},
				{"Monthly cost", money(a.Cost)},
			}
			return c.printer.Render(a, []string{"FIELD", "VALUE"}, rows)
		},
	}
}

func appsRemoveCmd(c *cli) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "rm <app>",
		Aliases: []string{"delete", "remove"},
		Short:   "Delete an app",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, project, err := c.clientAndProject(cmd.Context())
			if err != nil {
				return err
			}
			if err := confirmByName(cmd, "app", args[0], yes); err != nil {
				return err
			}
			resp, err := cl.DeleteAppWithResponse(cmd.Context(), project, args[0])
			if err != nil {
				return err
			}
			if err := checkResp(resp.StatusCode(), resp.Body); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Deleted app %q.\n", args[0])
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")
	return cmd
}

func appsRebuildCmd(c *cli) *cobra.Command {
	return &cobra.Command{
		Use:   "rebuild <app>",
		Short: "Trigger a rebuild of an app",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, project, err := c.clientAndProject(cmd.Context())
			if err != nil {
				return err
			}
			resp, err := cl.RebuildAppWithResponse(cmd.Context(), project, args[0])
			if err != nil {
				return err
			}
			if err := checkResp(resp.StatusCode(), resp.Body); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Rebuild triggered for %q.\n", args[0])
			return nil
		},
	}
}

func appsRestartCmd(c *cli) *cobra.Command {
	return &cobra.Command{
		Use:   "restart <app>",
		Short: "Restart an app",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, project, err := c.clientAndProject(cmd.Context())
			if err != nil {
				return err
			}
			resp, err := cl.RestartAppWithResponse(cmd.Context(), project, args[0])
			if err != nil {
				return err
			}
			if err := checkResp(resp.StatusCode(), resp.Body); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Restarted %q.\n", args[0])
			return nil
		},
	}
}

func joinStrings(ss []string) string {
	out := ""
	for i, s := range ss {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}
