package cmd

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"sync"

	"github.com/hostimdev/cli/api"
	"github.com/spf13/cobra"
)

func newOverviewCmd(c *cli) *cobra.Command {
	return &cobra.Command{
		Use:     "overview [project]",
		Aliases: []string{"resources"},
		Short:   "List every resource across all your projects",
		Long: "Show a single table of all resources (apps, databases, redis, volumes)\n" +
			"across every project, with their plan, key details and monthly cost.\n\n" +
			"Pass a project name or ID to limit the overview to one project.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := c.Client()
			if err != nil {
				return err
			}
			filter := ""
			if len(args) == 1 {
				filter = args[0]
			}
			return runOverview(cmd, c, a, filter)
		},
	}
}

// resourceRow is one resource in the flat overview, across all projects.
type resourceRow struct {
	Project string   `json:"project"`
	Region  string   `json:"region"`
	Kind    string   `json:"kind"`
	Name    string   `json:"name"`
	Plan    string   `json:"plan"`
	Info    string   `json:"info,omitempty"`
	Cost    *float32 `json:"costPerMonth,omitempty"`
}

func runOverview(cmd *cobra.Command, c *cli, a *api.ClientWithResponses, filter string) error {
	ctx := cmd.Context()

	projResp, err := a.GetProjectsWithResponse(ctx)
	if err != nil {
		return err
	}
	if err := checkResp(projResp.StatusCode(), projResp.Body); err != nil {
		return err
	}
	var projects []api.Project
	if projResp.JSON200 != nil {
		projects = *projResp.JSON200
	}

	// Optionally narrow to a single project (by name or ID).
	if filter != "" {
		var kept []api.Project
		for _, p := range projects {
			if p.Id == filter || (p.Name != nil && *p.Name == filter) {
				kept = append(kept, p)
			}
		}
		if len(kept) == 0 {
			return fmt.Errorf("project %q not found", filter)
		}
		projects = kept
	}

	// Gather each project's resources concurrently; keep results ordered by
	// project and record per-project errors as warnings rather than aborting.
	results := make([][]resourceRow, len(projects))
	errs := make([]error, len(projects))
	var wg sync.WaitGroup
	for i := range projects {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = gatherProject(ctx, a, projects[i])
		}(i)
	}
	wg.Wait()

	var all []resourceRow
	var warnings []string
	for i, p := range projects {
		if errs[i] != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %v", projectLabel(p), errs[i]))
			continue
		}
		all = append(all, results[i]...)
	}

	if c.printer.Format == "json" {
		if err := c.printer.JSON(all); err != nil {
			return err
		}
		return warnErr(warnings)
	}

	rows := make([][]string, 0, len(all))
	var total float32
	for _, r := range all {
		rows = append(rows, []string{r.Project, r.Region, r.Kind, r.Name, r.Plan, r.Info, money(r.Cost)})
		if r.Cost != nil {
			total += *r.Cost
		}
	}
	if err := c.printer.Render(all,
		[]string{"PROJECT", "REGION", "KIND", "NAME", "PLAN", "INFO", "€/MO"}, rows); err != nil {
		return err
	}
	if len(rows) > 0 {
		fmt.Fprintf(c.printer.Out, "\n%d resources across %d project(s), €%s/mo\n",
			len(all), len(projects), strconv.FormatFloat(float64(total), 'f', 2, 32))
	}
	return warnErr(warnings)
}

// gatherProject lists every resource in one project and flattens it into rows.
func gatherProject(ctx context.Context, a *api.ClientWithResponses, p api.Project) ([]resourceRow, error) {
	name := str(p.Name)
	if name == "" {
		name = p.Id
	}
	region := str(p.Region)
	var rows []resourceRow

	apps, err := a.GetAppsWithResponse(ctx, p.Id)
	if err != nil {
		return nil, err
	}
	if err := checkResp(apps.StatusCode(), apps.Body); err != nil {
		return nil, err
	}
	if apps.JSON200 != nil {
		for i := range *apps.JSON200 {
			app := (*apps.JSON200)[i]
			rows = append(rows, resourceRow{name, region, "app", app.Name, app.Plan, appInfo(&app), app.Cost})
		}
	}

	pg, err := a.GetPostgresWithResponse(ctx, p.Id)
	if err != nil {
		return nil, err
	}
	if err := checkResp(pg.StatusCode(), pg.Body); err != nil {
		return nil, err
	}
	if pg.JSON200 != nil {
		for _, d := range *pg.JSON200 {
			rows = append(rows, resourceRow{name, region, "postgres", d.Name, d.Plan, dbInfo(d.Type, d.StorageMB), d.Cost})
		}
	}

	my, err := a.GetMysqlsWithResponse(ctx, p.Id)
	if err != nil {
		return nil, err
	}
	if err := checkResp(my.StatusCode(), my.Body); err != nil {
		return nil, err
	}
	if my.JSON200 != nil {
		for _, d := range *my.JSON200 {
			rows = append(rows, resourceRow{name, region, "mysql", d.Name, d.Plan, dbInfo(d.Type, d.StorageMB), d.Cost})
		}
	}

	rd, err := a.GetRedisesWithResponse(ctx, p.Id)
	if err != nil {
		return nil, err
	}
	if err := checkResp(rd.StatusCode(), rd.Body); err != nil {
		return nil, err
	}
	if rd.JSON200 != nil {
		for _, d := range *rd.JSON200 {
			rows = append(rows, resourceRow{name, region, "redis", d.Name, d.Plan, sizeMB(d.Storage), d.Cost})
		}
	}

	vol, err := a.GetVolumesWithResponse(ctx, p.Id)
	if err != nil {
		return nil, err
	}
	if err := checkResp(vol.StatusCode(), vol.Body); err != nil {
		return nil, err
	}
	if vol.JSON200 != nil {
		for _, d := range *vol.JSON200 {
			rows = append(rows, resourceRow{name, region, "volume", d.Name, d.Plan, sizeMB(d.StorageMB), d.Cost})
		}
	}

	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Kind != rows[j].Kind {
			return kindOrder(rows[i].Kind) < kindOrder(rows[j].Kind)
		}
		return rows[i].Name < rows[j].Name
	})
	return rows, nil
}

// appInfo summarizes an app: its source, replica count and visibility.
func appInfo(app *api.App) string {
	info := describeSource(app)
	if app.Replicas > 1 {
		info += fmt.Sprintf(" x%d", app.Replicas)
	}
	if app.Public {
		info += " · public"
	}
	return info
}

// dbInfo summarizes a database: its tier (shared/dedicated) and storage.
func dbInfo(typ *string, storageMB *float32) string {
	s := sizeMB(storageMB)
	if typ != nil && *typ != "" {
		if s != "-" {
			return *typ + ", " + s
		}
		return *typ
	}
	return s
}

// sizeMB renders an optional size in MB as GB (or "-").
func sizeMB(mb *float32) string {
	if mb == nil || *mb <= 0 {
		return "-"
	}
	gb := float64(*mb) / 1024
	if gb == float64(int(gb)) {
		return strconv.Itoa(int(gb)) + "GB"
	}
	return strconv.FormatFloat(gb, 'f', 1, 64) + "GB"
}

func kindOrder(kind string) int {
	switch kind {
	case "app":
		return 0
	case "postgres":
		return 1
	case "mysql":
		return 2
	case "redis":
		return 3
	case "volume":
		return 4
	default:
		return 5
	}
}

func projectLabel(p api.Project) string {
	if p.Name != nil && *p.Name != "" {
		return *p.Name
	}
	return p.Id
}

// warnErr prints per-project warnings to stderr; it never fails the command.
func warnErr(warnings []string) error {
	for _, w := range warnings {
		fmt.Fprintln(os.Stderr, "warning: "+w)
	}
	return nil
}
