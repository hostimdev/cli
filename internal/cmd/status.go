package cmd

import (
	"github.com/spf13/cobra"

	"github.com/hostimdev/cli/api"
)

func newStatusCmd(c *cli) *cobra.Command {
	return &cobra.Command{
		Use:   "status [app]",
		Short: "Show build/runtime status for one app or all apps",
		Long: "With an app name, show that app's build and runtime status.\n" +
			"Without one, show a one-glance health table for every app in the project.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, project, err := c.clientAndProject(cmd.Context())
			if err != nil {
				return err
			}
			if len(args) == 1 {
				return statusOne(cmd, c, a, project, args[0])
			}
			return statusAll(cmd, c, a, project)
		},
	}
}

func statusOne(cmd *cobra.Command, c *cli, a *api.ClientWithResponses, project, app string) error {
	resp, err := a.GetAppStatusWithResponse(cmd.Context(), project, app)
	if err != nil {
		return err
	}
	if err := checkResp(resp.StatusCode(), resp.Body); err != nil {
		return err
	}
	st := resp.JSON200
	if st == nil {
		return errEmptyResponse
	}
	rows := [][]string{
		{"App", app},
		{"Build", buildStr(st.BuildStatus)},
		{"Runtime", runtimeStr(st.RuntimeStatus)},
		{"Internal DNS", dash(str(st.InternalDNS))},
	}
	if st.LastDeployedAt != nil {
		rows = append(rows, []string{"Last deployed", st.LastDeployedAt.Format("2006-01-02 15:04:05")})
	}
	return c.printer.Render(st, []string{"FIELD", "VALUE"}, rows)
}

func statusAll(cmd *cobra.Command, c *cli, a *api.ClientWithResponses, project string) error {
	appsResp, err := a.GetAppsWithResponse(cmd.Context(), project)
	if err != nil {
		return err
	}
	if err := checkResp(appsResp.StatusCode(), appsResp.Body); err != nil {
		return err
	}
	var apps []api.App
	if appsResp.JSON200 != nil {
		apps = *appsResp.JSON200
	}

	type row struct {
		Name    string `json:"name"`
		Build   string `json:"build"`
		Runtime string `json:"runtime"`
	}
	out := make([]row, 0, len(apps))
	rows := make([][]string, 0, len(apps))
	for _, app := range apps {
		build, runtime := "-", "-"
		if sr, err := a.GetAppStatusWithResponse(cmd.Context(), project, app.Name); err == nil && sr.JSON200 != nil {
			build = buildStr(sr.JSON200.BuildStatus)
			runtime = runtimeStr(sr.JSON200.RuntimeStatus)
		}
		out = append(out, row{app.Name, build, runtime})
		rows = append(rows, []string{app.Name, build, runtime})
	}
	return c.printer.Render(out, []string{"APP", "BUILD", "RUNTIME"}, rows)
}

func buildStr(s *api.AppStatusBuildStatus) string {
	// Docker-image apps have no build phase, so the API returns an empty
	// build status; render it as "-" rather than a blank cell.
	if s == nil || string(*s) == "" {
		return "-"
	}
	return string(*s)
}

func runtimeStr(s *api.AppStatusRuntimeStatus) string {
	if s == nil {
		return "-"
	}
	return string(*s)
}
