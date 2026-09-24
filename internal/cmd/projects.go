package cmd

import (
	"context"
	"fmt"
	"os"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/hostimdev/cli/api"
	"github.com/hostimdev/cli/internal/client"
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
		projectsExportCmd(c),
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
			id, err := client.ResolveProjectID(cmd.Context(), cl, args[0])
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
				return client.ErrEmptyResponse
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
				return fmt.Errorf("--region is required: list regions with `hostim regions ls`")
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
			return c.result(cmd, res("created", "project", args[0], "region", region),
				"Created project %q in %s.", args[0], region)
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
			id, err := client.ResolveProjectID(cmd.Context(), cl, args[0])
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
			return c.result(cmd, res("deleted", "project", args[0]), "Deleted project %q.", args[0])
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")
	return cmd
}

// ---- export ----

func projectsExportCmd(c *cli) *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export a project as a deployable template YAML",
		Long: "Export a project's resources (apps with env vars, volumes, postgres,\n" +
			"mysql and redis) as a template YAML that `hostim templates apply` can\n" +
			"redeploy, for example into a fresh project:\n\n" +
			"  hostim projects export -p prod -f prod.yml\n" +
			"  hostim templates apply -f prod.yml --new-project prod-clone\n\n" +
			"This is a desired-state document, NOT a backup: the CONTENTS of volumes\n" +
			"and databases (your data) are NOT exported — only the resources and\n" +
			"their configuration.\n\n" +
			"IDs, costs, built-in domains, timestamps and unused deployment-source\n" +
			"blocks are always stripped, and env var values are exported RAW: the\n" +
			"output contains passwords, API keys and other secrets in plaintext,\n" +
			"so store and share the file like a password file. Docker registry\n" +
			"passwords and git tokens are not readable through the API (they come\n" +
			"back redacted), so they are left out — set them again after applying.\n" +
			"Registry usernames are kept, since private registries need them.\n\n" +
			"Without -f the YAML goes to stdout; -o json prints the template as\n" +
			"JSON instead.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			out := cmd.OutOrStdout()
			errOut := cmd.ErrOrStderr()
			a, project, err := c.clientAndProject(ctx)
			if err != nil {
				return err
			}
			name, tmpl, err := fetchProjectTemplate(ctx, a, project)
			if err != nil {
				return err
			}
			redacted := stripForExport(tmpl)
			tmpl.Id = name
			tmpl.Name = name
			tmpl.Description = fmt.Sprintf("Exported from project %s", name)
			fmt.Fprintln(errOut, "warning: this export contains env var values in plaintext, including passwords, API keys and other secrets. Store and share it like a password file.")
			if redacted {
				fmt.Fprintln(errOut, "note: registry passwords and git tokens are not readable through the API and were left out; set them again after applying.")
			}

			if c.jsonOut() {
				p := c.printer
				p.Out = out
				return p.JSON(tmpl)
			}
			data, err := templateToYAML(tmpl)
			if err != nil {
				return err
			}
			if file == "" {
				_, err = out.Write(data)
				return err
			}
			if err := os.WriteFile(file, data, 0o600); err != nil {
				return err
			}
			fmt.Fprintf(out, "Exported %s to %s (%s).\n", name, file, summarize(tmpl))
			fmt.Fprintf(out, "Note: volume and database CONTENTS (data) are not exported — this is not a backup.\n")
			return nil
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "", "write the template YAML to this file instead of stdout")
	return cmd
}

// fetchProjectTemplate walks a project's resources (apps with env vars, volumes,
// postgres, mysql and redis) and assembles them into a live api.Template,
// returning the project's name along with it.
func fetchProjectTemplate(ctx context.Context, a *api.ClientWithResponses, project string) (string, *api.Template, error) {
	pr, err := a.GetProjectWithResponse(ctx, project)
	if err != nil {
		return "", nil, err
	}
	if err := checkResp(pr.StatusCode(), pr.Body); err != nil {
		return "", nil, err
	}
	if pr.JSON200 == nil {
		return "", nil, client.ErrEmptyResponse
	}
	name := str(pr.JSON200.Name)

	t := &api.Template{}

	apps, err := a.GetAppsWithResponse(ctx, project)
	if err != nil {
		return "", nil, err
	}
	if err := checkResp(apps.StatusCode(), apps.Body); err != nil {
		return "", nil, err
	}
	if apps.JSON200 != nil {
		t.Components.Apps = *apps.JSON200
		for i := range t.Components.Apps {
			if t.Components.Apps[i].EnvVars != nil {
				continue
			}
			ev, err := a.GetAppEnvWithResponse(ctx, project, t.Components.Apps[i].Name)
			if err != nil {
				return "", nil, err
			}
			if err := checkResp(ev.StatusCode(), ev.Body); err != nil {
				return "", nil, err
			}
			if ev.JSON200 != nil {
				t.Components.Apps[i].EnvVars = ev.JSON200
			}
		}
	}

	pg, err := a.GetPostgresWithResponse(ctx, project)
	if err != nil {
		return "", nil, err
	}
	if err := checkResp(pg.StatusCode(), pg.Body); err != nil {
		return "", nil, err
	}
	if pg.JSON200 != nil {
		t.Components.Postgres = *pg.JSON200
	}

	my, err := a.GetMysqlsWithResponse(ctx, project)
	if err != nil {
		return "", nil, err
	}
	if err := checkResp(my.StatusCode(), my.Body); err != nil {
		return "", nil, err
	}
	if my.JSON200 != nil {
		t.Components.Mysql = *my.JSON200
	}

	rd, err := a.GetRedisesWithResponse(ctx, project)
	if err != nil {
		return "", nil, err
	}
	if err := checkResp(rd.StatusCode(), rd.Body); err != nil {
		return "", nil, err
	}
	if rd.JSON200 != nil {
		t.Components.Redis = *rd.JSON200
	}

	vol, err := a.GetVolumesWithResponse(ctx, project)
	if err != nil {
		return "", nil, err
	}
	if err := checkResp(vol.StatusCode(), vol.Body); err != nil {
		return "", nil, err
	}
	if vol.JSON200 != nil {
		t.Components.Volumes = *vol.JSON200
	}
	return name, t, nil
}

// stripForExport turns a live template into a desired-state document: it
// removes cost fields, built-in domains, the unused half of each app's
// deployment source, and the credentials the API never returns in cleartext
// (docker registry password, git token — they come back redacted). Env var
// values are kept raw, so the caller must treat the result as a secret file.
// It mutates t, makes no API calls and reports whether a redacted credential
// was present, so it is unit-testable without a live project.
func stripForExport(t *api.Template) bool {
	redacted := false
	for i := range t.Components.Apps {
		a := &t.Components.Apps[i]
		a.Cost = nil
		a.BuiltInDomain = nil
		if a.DeploymentSource.Type == api.Git && a.DeploymentSource.Docker != nil {
			// A git source builds its own image; a docker image attached to a
			// git app is the built artifact, not the deployment source.
			a.DeploymentSource.Docker = nil
		}
		if a.DeploymentSource.Type == api.Docker {
			// A docker source never reads the git block.
			a.DeploymentSource.Git = nil
		}
		if d := a.DeploymentSource.Docker; d != nil {
			// Username is kept: private registries need it. The password is
			// redacted by the API and dropped.
			if d.Password != nil {
				redacted = true
			}
			d.Password = nil
		}
		if g := a.DeploymentSource.Git; g != nil {
			if g.Token != nil {
				redacted = true
			}
			g.Token = nil
		}
	}
	for i := range t.Components.Volumes {
		t.Components.Volumes[i].Cost = nil
	}
	for i := range t.Components.Postgres {
		t.Components.Postgres[i].Cost = nil
	}
	for i := range t.Components.Mysql {
		t.Components.Mysql[i].Cost = nil
	}
	for i := range t.Components.Redis {
		t.Components.Redis[i].Cost = nil
	}
	return redacted
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
