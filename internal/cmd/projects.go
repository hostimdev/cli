package cmd

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/hostimdev/cli/api"
)

func newProjectsCmd(c *cli) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "projects",
		Aliases: []string{"project", "proj"},
		Short:   "Manage projects",
	}
	cmd.AddCommand(
		projectsListCmd(c),
		projectsGetCmd(c),
		projectsCreateCmd(c),
		projectsRemoveCmd(c),
	)
	return cmd
}

func projectsListCmd(c *cli) *cobra.Command {
	return &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List projects",
		Args:    cobra.NoArgs,
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
			var projects []api.Project
			if resp.JSON200 != nil {
				projects = *resp.JSON200
			}
			rows := make([][]string, 0, len(projects))
			for _, p := range projects {
				rows = append(rows, []string{
					str(p.Name), str(p.Region),
					strconv.Itoa(deployed(p.DeployedServices)),
					money(p.ProjectedMonthlyCosts),
				})
			}
			return c.printer.Render(projects,
				[]string{"NAME", "REGION", "SERVICES", "MONTHLY"}, rows)
		},
	}
}

func projectsGetCmd(c *cli) *cobra.Command {
	return &cobra.Command{
		Use:   "get <project>",
		Short: "Show a project",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := c.Client()
			if err != nil {
				return err
			}
			id, err := resolveProjectID(cmd.Context(), cl, args[0])
			if err != nil {
				return err
			}
			resp, err := cl.GetProjectWithResponse(cmd.Context(), id)
			if err != nil {
				return err
			}
			if err := checkResp(resp.StatusCode(), resp.Body); err != nil {
				return err
			}
			p := resp.JSON200
			if p == nil {
				return fmt.Errorf("empty response")
			}
			rows := [][]string{
				{"Name", str(p.Name)},
				{"Region", str(p.Region)},
				{"ID", p.Id},
				{"Services", strconv.Itoa(deployed(p.DeployedServices))},
				{"Monthly cost", money(p.ProjectedMonthlyCosts)},
			}
			return c.printer.Render(p, []string{"FIELD", "VALUE"}, rows)
		},
	}
}

func projectsCreateCmd(c *cli) *cobra.Command {
	var region string
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a project in a region",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if region == "" {
				return fmt.Errorf("--region is required")
			}
			cl, err := c.Client()
			if err != nil {
				return err
			}
			resp, err := cl.CreateProjectWithResponse(cmd.Context(), &api.CreateProjectParams{
				Name:       args[0],
				RegionName: region,
			})
			if err != nil {
				return err
			}
			if err := checkResp(resp.StatusCode(), resp.Body); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Created project %q in %s.\n", args[0], region)
			return nil
		},
	}
	cmd.Flags().StringVar(&region, "region", "", "region to create the project in (required)")
	return cmd
}

func projectsRemoveCmd(c *cli) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "rm <project>",
		Aliases: []string{"delete", "remove"},
		Short:   "Delete a project",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := c.Client()
			if err != nil {
				return err
			}
			id, err := resolveProjectID(cmd.Context(), cl, args[0])
			if err != nil {
				return err
			}
			if err := confirmByName(cmd, "project", args[0], yes); err != nil {
				return err
			}
			resp, err := cl.DeleteProjectWithResponse(cmd.Context(), id)
			if err != nil {
				return err
			}
			if err := checkResp(resp.StatusCode(), resp.Body); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Deleted project %q.\n", args[0])
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")
	return cmd
}

func deployed(f *float32) int {
	if f == nil {
		return 0
	}
	return int(*f)
}

func money(f *float32) string {
	if f == nil {
		return "-"
	}
	return "€" + strconv.FormatFloat(float64(*f), 'f', 2, 32)
}
