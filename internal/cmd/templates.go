package cmd

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/hostimdev/cli/api"
	"github.com/hostimdev/cli/internal/client"
)

func newTemplatesCmd(c *cli) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "templates",
		Aliases: []string{"template", "tpl"},
		Short:   "List and deploy templates (including from a docker-compose file)",
	}
	cmd.AddCommand(templatesListCmd(c), templatesShowCmd(c), templatesApplyCmd(c), templatesValidateCmd(c))
	return cmd
}

// ---- list ----

func templatesListCmd(c *cli) *cobra.Command {
	return &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List the curated templates offered by the platform",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, err := c.Client()
			if err != nil {
				return err
			}
			resp, err := a.GetTemplatesWithResponse(cmd.Context())
			if err != nil {
				return err
			}
			if err := checkResp(resp.StatusCode(), resp.Body); err != nil {
				return err
			}
			var items []api.Template
			if resp.JSON200 != nil {
				items = *resp.JSON200
			}
			rows := make([][]string, 0, len(items))
			for _, t := range items {
				rows = append(rows, []string{t.Id, t.Name, dash(str(t.Category)), summarize(&t)})
			}
			return c.printer.Render(items, []string{"ID", "NAME", "CATEGORY", "COMPONENTS"}, rows)
		},
	}
}

// ---- show ----

func templatesShowCmd(c *cli) *cobra.Command {
	return &cobra.Command{
		Use:   "show <id>",
		Short: "Show a curated template's resources",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := c.Client()
			if err != nil {
				return err
			}
			t, err := fetchTemplate(cmd.Context(), a, args[0])
			if err != nil {
				return err
			}
			return c.printer.Render(t, []string{"FIELD", "VALUE"}, templateRows(t))
		},
	}
}

var htmlTagRe = regexp.MustCompile(`<[^>]+>`)

// templateRows renders a template as readable FIELD/VALUE rows: metadata first,
// then one row per resource. The full structure is still available via -o json.
func templateRows(t *api.Template) [][]string {
	rows := [][]string{
		{"Name", t.Name},
		{"ID", t.Id},
		{"Category", dash(str(t.Category))},
		{"Description", t.Description},
		{"Source", dash(str(t.SourceUrl))},
	}
	for _, v := range t.Components.Volumes {
		rows = append(rows, []string{"volume", fmt.Sprintf("%s (plan %s)", v.Name, v.Plan)})
	}
	for _, d := range t.Components.Postgres {
		rows = append(rows, []string{"postgres", fmt.Sprintf("%s (plan %s)", d.Name, d.Plan)})
	}
	for _, d := range t.Components.Mysql {
		rows = append(rows, []string{"mysql", fmt.Sprintf("%s (plan %s)", d.Name, d.Plan)})
	}
	for _, d := range t.Components.Redis {
		rows = append(rows, []string{"redis", fmt.Sprintf("%s (plan %s)", d.Name, d.Plan)})
	}
	for i := range t.Components.Apps {
		app := t.Components.Apps[i]
		rows = append(rows, []string{"app", fmt.Sprintf("%s (%s, plan %s%s)", app.Name, describeSource(&app), app.Plan, portSuffix(app.HttpPort))})
	}
	if t.Notes != nil {
		for _, n := range *t.Notes {
			if clean := strings.TrimSpace(htmlTagRe.ReplaceAllString(n, "")); clean != "" {
				rows = append(rows, []string{"note", clean})
			}
		}
	}
	return rows
}

// describeSource summarizes an app's deployment source, e.g. "docker nginx" or
// "git github.com/x/y".
func describeSource(app *api.App) string {
	ds := app.DeploymentSource
	if ds.Type == api.Git && ds.Git != nil {
		return "git " + ds.Git.Url
	}
	if ds.Type == api.Docker && ds.Docker != nil {
		return "docker " + ds.Docker.Image
	}
	return string(ds.Type)
}

func portSuffix(p *int) string {
	if p == nil {
		return ""
	}
	return fmt.Sprintf(", port %d", *p)
}

// ---- apply ----

type applyFlags struct {
	id      string
	file    string
	compose string

	newProject string
	region     string

	save         string
	skipExisting bool
	yes          bool

	wait     bool
	timeout  time.Duration
	interval time.Duration
}

func templatesApplyCmd(c *cli) *cobra.Command {
	f := &applyFlags{}
	cmd := &cobra.Command{
		Use:   "apply",
		Short: "Deploy a template's resources one by one",
		Long: "Deploy every resource in a template (volumes, databases, redis, then\n" +
			"apps) into a project, creating each and waiting for it to become ready.\n\n" +
			"Pick exactly one source:\n" +
			"  --id <template-id>   a curated template from the platform\n" +
			"  -f <file>            a local template YAML file (single template or a\n" +
			"                       list like backend/templates.yml — with a list, use\n" +
			"                       --id to pick the entry)\n" +
			"  --compose <file>     a docker-compose file, converted to a template via the API\n\n" +
			"The resources land in the resolved project (-p / current project) unless\n" +
			"--new-project is given, which creates a fresh project first.\n\n" +
			"Instead of deploying, pass --save <file> to write the resolved template to\n" +
			"a local YAML file (works with --id and --compose too). Edit it, then deploy\n" +
			"it with `apply -f <file>` — handy for fixing a converted compose file.\n\n" +
			"Before anything is created, apply prints the resources it will deploy and\n" +
			"asks for confirmation (skip with --yes).\n\n" +
			"If any resource already exists in the target project, apply aborts before\n" +
			"creating anything (so it never overwrites a scaled or configured resource).\n" +
			"Pass --skip-existing to create only the resources that are missing.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runApply(cmd, c, f)
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&f.id, "id", "", "curated template ID to deploy, or the entry to pick out of a -f list")
	fl.StringVarP(&f.file, "file", "f", "", "local template YAML file to deploy")
	fl.StringVar(&f.compose, "compose", "", "docker-compose file to convert and deploy")
	fl.StringVar(&f.newProject, "new-project", "", "create a new project with this name before deploying")
	fl.StringVar(&f.region, "region", "", "region for --new-project (defaults to the only region when there is one)")
	fl.StringVar(&f.save, "save", "", "write the resolved template to this YAML file instead of deploying")
	fl.BoolVar(&f.skipExisting, "skip-existing", false, "skip resources that already exist instead of aborting")
	fl.BoolVarP(&f.yes, "yes", "y", false, "skip the confirmation prompt")
	fl.BoolVar(&f.wait, "wait", true, "wait for each resource to become ready")
	fl.DurationVar(&f.timeout, "timeout", 15*time.Minute, "max time to wait per resource")
	fl.DurationVar(&f.interval, "poll-interval", 3*time.Second, "status poll interval")
	return cmd
}

func runApply(cmd *cobra.Command, c *cli, f *applyFlags) error {
	ctx := cmd.Context()
	out := cmd.OutOrStdout()
	// Under -o json the per-resource progress lines would sit in front of the
	// result object, so they are dropped and the command answers with a single
	// summary instead.
	if c.jsonOut() {
		out = io.Discard
	}

	// One source. --id doubles as the entry selector inside a -f file that holds
	// a list of templates (which is what backend/templates.yml is).
	switch {
	case f.file != "" && f.compose != "":
		return fmt.Errorf("provide exactly one of --id, --file or --compose")
	case f.compose != "" && f.id != "":
		return fmt.Errorf("provide exactly one of --id, --file or --compose")
	case f.file == "" && f.compose == "" && f.id == "":
		return fmt.Errorf("provide exactly one of --id, --file or --compose")
	}

	a, err := c.Client()
	if err != nil {
		return err
	}

	// Load the template from the chosen source.
	var tmpl *api.Template
	switch {
	case f.file != "":
		tmpl, err = loadTemplateFile(f.file, f.id)
	case f.compose != "":
		tmpl, err = parseCompose(ctx, out, a, f.compose)
	default:
		tmpl, err = fetchTemplate(ctx, a, f.id)
	}
	if err != nil {
		return err
	}

	// --save writes the template to a local file (with GENERATE_ME_<n>
	// placeholders intact) instead of deploying, so the user can edit it and
	// re-apply with -f. Validation issues are warned about, not fatal, so a
	// converted compose file can be saved and then fixed.
	if f.save != "" {
		if err := saveTemplate(out, tmpl, f.save); err != nil {
			return err
		}
		return c.result(cmd, res("saved", "template", tmpl.Name, "file", f.save,
			"resources", templateResources(tmpl)), "")
	}

	// Expand GENERATE_ME_<n> secrets now so the plan we show and the names we
	// validate reflect exactly what will be created.
	for i := range tmpl.Components.Apps {
		if err := expandGenerateMe(&tmpl.Components.Apps[i]); err != nil {
			return err
		}
	}

	// Some apps (typically compose services that build from a local Dockerfile)
	// arrive without a usable deployment source. Mirror the dashboard: guide the
	// user to supply a docker image or git repo before deploying.
	if err := completeAppSources(cmd, out, tmpl); err != nil {
		return err
	}

	// Validate before touching anything (including before creating --new-project),
	// so a bad name or missing plan never leaves an orphaned project behind.
	if err := checkPlans(tmpl); err != nil {
		return err
	}
	if err := validateNames(tmpl); err != nil {
		return fmt.Errorf("%w\n\ntip: save it with --save <file>, fix the names, then deploy with `apply -f <file>`", err)
	}

	// Review: show what will be deployed and confirm before creating anything.
	target := f.newProject
	if target == "" {
		if ref, perr := c.Project(); perr == nil {
			target = ref
		} else {
			target = "the current project"
		}
	}
	printPlan(out, tmpl, target)
	if err := confirmYesNo(cmd, fmt.Sprintf("Deploy these resources into %q?", target), f.yes); err != nil {
		return err
	}

	// Resolve (or create) the target project.
	project, err := resolveApplyProject(ctx, out, c, a, f)
	if err != nil {
		return err
	}

	if err := applyTemplate(ctx, out, a, project, tmpl, f.skipExisting, f.wait, f.timeout, f.interval); err != nil {
		return err
	}
	return c.result(cmd, res("deployed", "template", tmpl.Name, "project", project,
		"resources", templateResources(tmpl)), "")
}

// templateResources lists what a template contains as kind/name pairs, for the
// -o json summary.
func templateResources(tmpl *api.Template) []map[string]string {
	out := []map[string]string{}
	add := func(kind, name string) { out = append(out, map[string]string{"kind": kind, "name": name}) }
	for _, v := range tmpl.Components.Volumes {
		add("volume", v.Name)
	}
	for _, d := range tmpl.Components.Postgres {
		add("postgres", d.Name)
	}
	for _, d := range tmpl.Components.Mysql {
		add("mysql", d.Name)
	}
	for _, d := range tmpl.Components.Redis {
		add("redis", d.Name)
	}
	for _, app := range tmpl.Components.Apps {
		add("app", app.Name)
	}
	return out
}

// printPlan lists the resources a template will create, so the user can review
// them before confirming the apply.
func printPlan(out io.Writer, tmpl *api.Template, target string) {
	fmt.Fprintf(out, "About to deploy %q (%s) into %q:\n", tmpl.Name, summarize(tmpl), target)
	for _, v := range tmpl.Components.Volumes {
		fmt.Fprintf(out, "  volume    %s (plan %s)\n", v.Name, v.Plan)
	}
	for _, d := range tmpl.Components.Postgres {
		fmt.Fprintf(out, "  postgres  %s (plan %s)\n", d.Name, d.Plan)
	}
	for _, d := range tmpl.Components.Mysql {
		fmt.Fprintf(out, "  mysql     %s (plan %s)\n", d.Name, d.Plan)
	}
	for _, d := range tmpl.Components.Redis {
		fmt.Fprintf(out, "  redis     %s (plan %s)\n", d.Name, d.Plan)
	}
	for i := range tmpl.Components.Apps {
		app := tmpl.Components.Apps[i]
		fmt.Fprintf(out, "  app       %s (%s, plan %s%s)\n", app.Name, describeSource(&app), app.Plan, portSuffix(app.HttpPort))
	}
}

// appSourceIncomplete reports whether an app lacks a usable deployment source:
// a docker source with no image, a git source with no URL, or no type at all.
// These are common in converted compose files whose services build from a local
// Dockerfile, which the platform cannot do.
func appSourceIncomplete(app *api.App) bool {
	ds := app.DeploymentSource
	switch ds.Type {
	case api.Docker:
		return ds.Docker == nil || ds.Docker.Image == ""
	case api.Git:
		return ds.Git == nil || ds.Git.Url == ""
	default:
		return true
	}
}

// incompleteSourceNames lists the apps whose deployment source is incomplete.
func incompleteSourceNames(tmpl *api.Template) []string {
	var names []string
	for i := range tmpl.Components.Apps {
		if appSourceIncomplete(&tmpl.Components.Apps[i]) {
			names = append(names, tmpl.Components.Apps[i].Name)
		}
	}
	return names
}

// completeAppSources fills in any app with an incomplete deployment source,
// mirroring the dashboard's "Needs configuration" step. In an interactive
// session it prompts for a docker image or git repo; otherwise it fails with a
// message pointing at --save so the user can edit the template by hand.
func completeAppSources(cmd *cobra.Command, out io.Writer, tmpl *api.Template) error {
	names := incompleteSourceNames(tmpl)
	if len(names) == 0 {
		return nil
	}
	if !isInteractive(cmd) {
		return fmt.Errorf("these apps have no deployment source: %s\n"+
			"(the compose services likely build from a local Dockerfile, which the platform can't do)\n"+
			"save the template with --save <file>, set a docker image or git repo for each app, "+
			"then deploy with `apply -f <file>`", strings.Join(names, ", "))
	}
	r := bufio.NewReader(cmd.InOrStdin())
	for i := range tmpl.Components.Apps {
		app := &tmpl.Components.Apps[i]
		if !appSourceIncomplete(app) {
			continue
		}
		fmt.Fprintf(out, "\nApp %q needs a deployment source (its compose service builds from a local Dockerfile).\n", app.Name)
		if err := promptAppSource(out, r, app); err != nil {
			return err
		}
	}
	return nil
}

// promptAppSource interactively collects a docker image or git repo for an app
// and writes it into the app's deployment source.
func promptAppSource(out io.Writer, r *bufio.Reader, app *api.App) error {
	typ, err := promptStr(out, r, "Deployment type [docker/git]", "docker")
	if err != nil {
		return err
	}
	ds := deploymentSource{}
	switch strings.ToLower(strings.TrimSpace(typ)) {
	case "git":
		url, err := promptRequired(out, r, "Git repository URL (e.g. git@github.com:user/repo.git)")
		if err != nil {
			return err
		}
		branch, err := promptStr(out, r, "Branch", "main")
		if err != nil {
			return err
		}
		dockerfile, err := promptStr(out, r, "Dockerfile path", "Dockerfile")
		if err != nil {
			return err
		}
		ds.Type = string(api.Git)
		ds.Git = &gitSource{Url: url}
		setOpt(&ds.Git.Branch, branch)
		setOpt(&ds.Git.Dockerfilepath, dockerfile)
		private, err := promptYes(out, r, "Private repo? provide an access token")
		if err != nil {
			return err
		}
		if private {
			token, err := promptStr(out, r, "Access token", "")
			if err != nil {
				return err
			}
			setOpt(&ds.Git.Token, token)
		}
	default: // docker
		image, err := promptRequired(out, r, "Docker image (e.g. nginx:latest)")
		if err != nil {
			return err
		}
		ds.Type = string(api.Docker)
		ds.Docker = &dockSource{Image: image}
		private, err := promptYes(out, r, "Private registry? provide credentials")
		if err != nil {
			return err
		}
		if private {
			registry, err := promptStr(out, r, "Registry (blank for Docker Hub)", "")
			if err != nil {
				return err
			}
			user, err := promptStr(out, r, "Username", "")
			if err != nil {
				return err
			}
			pass, err := promptStr(out, r, "Password", "")
			if err != nil {
				return err
			}
			setOpt(&ds.Docker.Registry, registry)
			setOpt(&ds.Docker.Username, user)
			setOpt(&ds.Docker.Password, pass)
		}
	}
	nb, err := json.Marshal(ds)
	if err != nil {
		return err
	}
	return json.Unmarshal(nb, &app.DeploymentSource)
}

// errInputAborted is returned when the user ends input (EOF) at a prompt.
var errInputAborted = errors.New("input aborted")

// promptStr asks for a line, returning def when the user just hits enter. EOF
// with no input aborts.
func promptStr(out io.Writer, r *bufio.Reader, label, def string) (string, error) {
	if def != "" {
		fmt.Fprintf(out, "%s (%s): ", label, def)
	} else {
		fmt.Fprintf(out, "%s: ", label)
	}
	line, err := r.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		if err != nil {
			return "", errInputAborted
		}
		return def, nil
	}
	return line, nil
}

// promptRequired asks until a non-empty value is given (or the user aborts).
func promptRequired(out io.Writer, r *bufio.Reader, label string) (string, error) {
	for {
		v, err := promptStr(out, r, label, "")
		if err != nil {
			return "", err
		}
		if v != "" {
			return v, nil
		}
		fmt.Fprintln(out, "  (required)")
	}
}

// promptYes asks a yes/no question defaulting to no.
func promptYes(out io.Writer, r *bufio.Reader, label string) (bool, error) {
	v, err := promptStr(out, r, label+" [y/N]", "")
	if err != nil {
		return false, err
	}
	v = strings.ToLower(v)
	return v == "y" || v == "yes", nil
}

// existingResources holds the names of resources already present in a project,
// keyed by kind, for the pre-apply conflict check.
type existingResources struct {
	apps     map[string]bool
	postgres map[string]bool
	mysql    map[string]bool
	redis    map[string]bool
	volumes  map[string]bool
}

// findConflicts returns the "kind name" of every template resource that already
// exists in the project, in creation order.
func findConflicts(tmpl *api.Template, e *existingResources) []string {
	var conflicts []string
	for _, v := range tmpl.Components.Volumes {
		if e.volumes[v.Name] {
			conflicts = append(conflicts, "volume "+v.Name)
		}
	}
	for _, d := range tmpl.Components.Postgres {
		if e.postgres[d.Name] {
			conflicts = append(conflicts, "postgres "+d.Name)
		}
	}
	for _, d := range tmpl.Components.Mysql {
		if e.mysql[d.Name] {
			conflicts = append(conflicts, "mysql "+d.Name)
		}
	}
	for _, d := range tmpl.Components.Redis {
		if e.redis[d.Name] {
			conflicts = append(conflicts, "redis "+d.Name)
		}
	}
	for _, app := range tmpl.Components.Apps {
		if e.apps[app.Name] {
			conflicts = append(conflicts, "app "+app.Name)
		}
	}
	return conflicts
}

// fetchExisting lists the resources already in the project so apply can detect
// name collisions before creating anything.
func fetchExisting(ctx context.Context, a *api.ClientWithResponses, project string) (*existingResources, error) {
	e := &existingResources{
		apps:     map[string]bool{},
		postgres: map[string]bool{},
		mysql:    map[string]bool{},
		redis:    map[string]bool{},
		volumes:  map[string]bool{},
	}

	apps, err := a.GetAppsWithResponse(ctx, project)
	if err != nil {
		return nil, err
	}
	if err := checkResp(apps.StatusCode(), apps.Body); err != nil {
		return nil, err
	}
	if apps.JSON200 != nil {
		for _, x := range *apps.JSON200 {
			e.apps[x.Name] = true
		}
	}

	pg, err := a.GetPostgresWithResponse(ctx, project)
	if err != nil {
		return nil, err
	}
	if err := checkResp(pg.StatusCode(), pg.Body); err != nil {
		return nil, err
	}
	if pg.JSON200 != nil {
		for _, x := range *pg.JSON200 {
			e.postgres[x.Name] = true
		}
	}

	my, err := a.GetMysqlsWithResponse(ctx, project)
	if err != nil {
		return nil, err
	}
	if err := checkResp(my.StatusCode(), my.Body); err != nil {
		return nil, err
	}
	if my.JSON200 != nil {
		for _, x := range *my.JSON200 {
			e.mysql[x.Name] = true
		}
	}

	rd, err := a.GetRedisesWithResponse(ctx, project)
	if err != nil {
		return nil, err
	}
	if err := checkResp(rd.StatusCode(), rd.Body); err != nil {
		return nil, err
	}
	if rd.JSON200 != nil {
		for _, x := range *rd.JSON200 {
			e.redis[x.Name] = true
		}
	}

	vol, err := a.GetVolumesWithResponse(ctx, project)
	if err != nil {
		return nil, err
	}
	if err := checkResp(vol.StatusCode(), vol.Body); err != nil {
		return nil, err
	}
	if vol.JSON200 != nil {
		for _, x := range *vol.JSON200 {
			e.volumes[x.Name] = true
		}
	}
	return e, nil
}

// resolveApplyProject creates a new project when --new-project is set, otherwise
// resolves the project from -p / HOSTIM_PROJECT / the current project.
func resolveApplyProject(ctx context.Context, out io.Writer, c *cli, a *api.ClientWithResponses, f *applyFlags) (string, error) {
	if f.newProject != "" {
		if f.region == "" {
			region, err := defaultRegion(ctx, a)
			if err != nil {
				return "", err
			}
			f.region = region
			fmt.Fprintf(out, "Using region %s.\n", region)
		}
		resp, err := a.CreateProjectWithResponse(ctx, &api.CreateProjectParams{
			RegionName: f.region,
			Name:       f.newProject,
		})
		if err != nil {
			return "", err
		}
		if err := checkResp(resp.StatusCode(), resp.Body); err != nil {
			return "", err
		}
		// createProject is documented as 201 but the server currently answers 200,
		// so the generated JSON201 field may be nil. Read the typed field when
		// present, otherwise parse the body, and finally fall back to a name
		// lookup — robust either way.
		id := ""
		if resp.JSON201 != nil {
			id = resp.JSON201.Id
		} else {
			var p api.Project
			if json.Unmarshal(resp.Body, &p) == nil {
				id = p.Id
			}
		}
		if id == "" {
			id, err = resolveProjectID(ctx, a, f.newProject)
			if err != nil {
				return "", fmt.Errorf("project created but could not resolve its ID: %w", err)
			}
		}
		fmt.Fprintf(out, "Created project %q (%s).\n", f.newProject, id)
		return id, nil
	}
	ref, err := c.Project()
	if err != nil {
		return "", fmt.Errorf("%w; or pass --new-project to create one", err)
	}
	return resolveProjectID(ctx, a, ref)
}

// applyTemplate creates every resource in the template, in dependency order
// (volumes, postgres, mysql, redis, then apps), waiting for each to be ready.
func applyTemplate(ctx context.Context, out io.Writer, a *api.ClientWithResponses, project string, tmpl *api.Template, skipExisting, wait bool, timeout, interval time.Duration) error {
	comps := tmpl.Components

	// Pre-flight: never overwrite resources that already exist. Abort before
	// creating anything unless the caller opted into skipping them.
	existing, err := fetchExisting(ctx, a, project)
	if err != nil {
		return err
	}
	conflicts := findConflicts(tmpl, existing)
	if len(conflicts) > 0 && !skipExisting {
		return fmt.Errorf("these resources already exist in %s: %s\n"+
			"apply will not overwrite them. Re-run with --skip-existing to create only the\n"+
			"missing resources, or --new-project to deploy into a fresh project",
			project, strings.Join(conflicts, ", "))
	}

	for _, v := range comps.Volumes {
		if skipExisting && existing.volumes[v.Name] {
			fmt.Fprintf(out, "volume %q ... exists, skipping\n", v.Name)
			continue
		}
		fmt.Fprintf(out, "volume %q ... ", v.Name)
		resp, err := a.CreateVolumeWithResponse(ctx, project, v)
		if err != nil {
			fmt.Fprintln(out)
			return err
		}
		if err := checkResp(resp.StatusCode(), resp.Body); err != nil {
			fmt.Fprintln(out)
			return fmt.Errorf("volume %q: %w", v.Name, err)
		}
		fmt.Fprintln(out, "created")
	}

	for _, db := range comps.Postgres {
		if skipExisting && existing.postgres[db.Name] {
			fmt.Fprintf(out, "postgres %q ... exists, skipping\n", db.Name)
			continue
		}
		fmt.Fprintf(out, "postgres %q ... ", db.Name)
		resp, err := a.CreatePostgresWithResponse(ctx, project, db)
		if err != nil {
			fmt.Fprintln(out)
			return err
		}
		if err := checkResp(resp.StatusCode(), resp.Body); err != nil {
			fmt.Fprintln(out)
			return fmt.Errorf("postgres %q: %w", db.Name, err)
		}
		if !wait {
			fmt.Fprintln(out, "created")
			continue
		}
		name := db.Name
		if err := waitFor(ctx, interval, timeout, func() (bool, error) {
			s, err := a.GetPostgresStatusWithResponse(ctx, project, name)
			if err != nil {
				return false, err
			}
			if err := checkResp(s.StatusCode(), s.Body); err != nil {
				return false, err
			}
			return s.JSON200 != nil && s.JSON200.Created, nil
		}); err != nil {
			return fmt.Errorf("postgres %q: %w", name, err)
		}
		fmt.Fprintln(out, "ready")
	}

	for _, db := range comps.Mysql {
		if skipExisting && existing.mysql[db.Name] {
			fmt.Fprintf(out, "mysql %q ... exists, skipping\n", db.Name)
			continue
		}
		fmt.Fprintf(out, "mysql %q ... ", db.Name)
		resp, err := a.CreateMysqlWithResponse(ctx, project, db)
		if err != nil {
			fmt.Fprintln(out)
			return err
		}
		if err := checkResp(resp.StatusCode(), resp.Body); err != nil {
			fmt.Fprintln(out)
			return fmt.Errorf("mysql %q: %w", db.Name, err)
		}
		if !wait {
			fmt.Fprintln(out, "created")
			continue
		}
		name := db.Name
		if err := waitFor(ctx, interval, timeout, func() (bool, error) {
			s, err := a.GetMysqlStatusWithResponse(ctx, project, name)
			if err != nil {
				return false, err
			}
			if err := checkResp(s.StatusCode(), s.Body); err != nil {
				return false, err
			}
			return s.JSON200 != nil && s.JSON200.Created, nil
		}); err != nil {
			return fmt.Errorf("mysql %q: %w", name, err)
		}
		fmt.Fprintln(out, "ready")
	}

	for _, db := range comps.Redis {
		if skipExisting && existing.redis[db.Name] {
			fmt.Fprintf(out, "redis %q ... exists, skipping\n", db.Name)
			continue
		}
		fmt.Fprintf(out, "redis %q ... ", db.Name)
		resp, err := a.CreateRedisWithResponse(ctx, project, db)
		if err != nil {
			fmt.Fprintln(out)
			return err
		}
		if err := checkResp(resp.StatusCode(), resp.Body); err != nil {
			fmt.Fprintln(out)
			return fmt.Errorf("redis %q: %w", db.Name, err)
		}
		if !wait {
			fmt.Fprintln(out, "created")
			continue
		}
		name := db.Name
		if err := waitFor(ctx, interval, timeout, func() (bool, error) {
			s, err := a.GetRedisStatusWithResponse(ctx, project, name)
			if err != nil {
				return false, err
			}
			if err := checkResp(s.StatusCode(), s.Body); err != nil {
				return false, err
			}
			return s.JSON200 != nil && s.JSON200.Available, nil
		}); err != nil {
			return fmt.Errorf("redis %q: %w", name, err)
		}
		fmt.Fprintln(out, "ready")
	}

	for _, app := range comps.Apps {
		app := app
		if skipExisting && existing.apps[app.Name] {
			fmt.Fprintf(out, "app %q ... exists, skipping\n", app.Name)
			continue
		}
		normalizeApp(&app)
		if app.Replicas < 1 {
			app.Replicas = 1
		}
		fmt.Fprintf(out, "app %q ... ", app.Name)
		resp, err := a.CreateAppWithResponse(ctx, project, app)
		if err != nil {
			fmt.Fprintln(out)
			return err
		}
		if err := checkResp(resp.StatusCode(), resp.Body); err != nil {
			fmt.Fprintln(out)
			return fmt.Errorf("app %q: %w", app.Name, err)
		}
		if !wait {
			fmt.Fprintln(out, "created")
			continue
		}
		// A git app builds before running (wait on the build); a docker app has no
		// build phase, so wait on runtime readiness instead.
		expectBuild := app.DeploymentSource.Type == api.Git
		cctx, cancel := context.WithTimeout(ctx, timeout)
		res, perr := client.PollBuild(cctx, a, project, app.Name, interval, expectBuild, nil)
		cancel()
		if perr != nil {
			switch {
			case errors.Is(perr, client.ErrBuildFailed):
				return fmt.Errorf("app %q: build failed; see `hostim logs %s --build`", app.Name, app.Name)
			case errors.Is(perr, client.ErrDeployFailed):
				return fmt.Errorf("app %q: did not become healthy (runtime: %s); see `hostim logs %s`", app.Name, dash(string(res.RuntimeStatus)), app.Name)
			case errors.Is(perr, context.DeadlineExceeded):
				return fmt.Errorf("app %q: timed out after %s", app.Name, timeout)
			default:
				return fmt.Errorf("app %q: %w", app.Name, perr)
			}
		}
		fmt.Fprintln(out, "running")
	}

	fmt.Fprintf(out, "\nDeployed %q into %s.\n", tmpl.Name, project)
	return nil
}

// ---- helpers ----

// fetchTemplate looks up a curated template by ID from the platform.
func fetchTemplate(ctx context.Context, a *api.ClientWithResponses, id string) (*api.Template, error) {
	resp, err := a.GetTemplatesWithResponse(ctx)
	if err != nil {
		return nil, err
	}
	if err := checkResp(resp.StatusCode(), resp.Body); err != nil {
		return nil, err
	}
	var ids []string
	if resp.JSON200 != nil {
		for i := range *resp.JSON200 {
			t := (*resp.JSON200)[i]
			if t.Id == id || t.Name == id {
				return &t, nil
			}
			ids = append(ids, t.Id)
		}
	}
	return nil, fmt.Errorf("template %q not found; available: %s", id, strings.Join(ids, ", "))
}

// loadTemplates reads a local YAML file holding either one template object or a
// list of them (the shape of backend/templates.yml). YAML is converted to JSON
// so the struct's json tags (camelCase) decode correctly.
func loadTemplates(path string) ([]api.Template, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	jb, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	var list []api.Template
	if _, isList := doc.([]any); isList {
		if err := json.Unmarshal(jb, &list); err != nil {
			return nil, fmt.Errorf("interpreting %s: %w", path, err)
		}
	} else {
		var t api.Template
		if err := json.Unmarshal(jb, &t); err != nil {
			return nil, fmt.Errorf("interpreting %s: %w", path, err)
		}
		list = []api.Template{t}
	}
	for _, t := range list {
		if t.Name == "" {
			return nil, fmt.Errorf("%s is not a valid template (an entry is missing name)", path)
		}
	}
	if len(list) == 0 {
		return nil, fmt.Errorf("%s contains no templates", path)
	}
	return list, nil
}

// loadTemplateFile reads one template from a local YAML file. When the file is a
// list, id picks the entry by id or name; a single-entry list needs no id.
func loadTemplateFile(path, id string) (*api.Template, error) {
	list, err := loadTemplates(path)
	if err != nil {
		return nil, err
	}
	if id == "" {
		if len(list) == 1 {
			return &list[0], nil
		}
		return nil, fmt.Errorf("%s contains %d templates; pick one with --id <id> (available: %s)",
			path, len(list), strings.Join(templateIDs(list), ", "))
	}
	for i := range list {
		if list[i].Id == id || list[i].Name == id {
			return &list[i], nil
		}
	}
	return nil, fmt.Errorf("template %q not found in %s; available: %s", id, path, strings.Join(templateIDs(list), ", "))
}

func templateIDs(list []api.Template) []string {
	ids := make([]string, 0, len(list))
	for _, t := range list {
		if t.Id != "" {
			ids = append(ids, t.Id)
			continue
		}
		ids = append(ids, t.Name)
	}
	return ids
}

// templatesValidateCmd runs the same offline checks apply does, without touching
// the API, so a freshly written templates.yml entry can be checked before commit.
func templatesValidateCmd(c *cli) *cobra.Command {
	var file, id string
	cmd := &cobra.Command{
		Use:   "validate -f <file>",
		Short: "Check a local template file offline (no API calls)",
		Long: "Validate a local template YAML file: every resource has a plan and a\n" +
			"valid name. Accepts a single template or a list (backend/templates.yml).\n" +
			"Without --id every entry in the file is checked.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if file == "" {
				return fmt.Errorf("--file is required: pass the template file to validate")
			}
			list, err := loadTemplates(file)
			if err != nil {
				return err
			}
			if id != "" {
				t, err := loadTemplateFile(file, id)
				if err != nil {
					return err
				}
				list = []api.Template{*t}
			}
			out := cmd.OutOrStdout()
			bad := 0
			for i := range list {
				t := &list[i]
				name := t.Id
				if name == "" {
					name = t.Name
				}
				var problems []string
				if err := checkPlans(t); err != nil {
					problems = append(problems, err.Error())
				}
				if err := validateNames(t); err != nil {
					problems = append(problems, err.Error())
				}
				if len(problems) == 0 {
					fmt.Fprintf(out, "ok    %s (%s)\n", name, summarize(t))
					continue
				}
				bad++
				fmt.Fprintf(out, "FAIL  %s\n", name)
				for _, p := range problems {
					fmt.Fprintf(out, "      %s\n", p)
				}
			}
			if bad > 0 {
				return fmt.Errorf("%d of %d templates invalid", bad, len(list))
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "", "local template YAML file to validate (required)")
	cmd.Flags().StringVar(&id, "id", "", "validate only this entry of a template list")
	return cmd
}

// saveTemplate writes a template to a local YAML file for editing. Validation
// problems are reported as warnings (not errors) so a converted compose file
// with, say, an over-long name can be saved and then fixed by hand.
func saveTemplate(out io.Writer, tmpl *api.Template, path string) error {
	data, err := templateToYAML(tmpl)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(out, "Saved template to %s (%s).\n", path, summarize(tmpl))
	var warns []string
	if err := checkPlans(tmpl); err != nil {
		warns = append(warns, err.Error())
	}
	if err := validateNames(tmpl); err != nil {
		warns = append(warns, err.Error())
	}
	if names := incompleteSourceNames(tmpl); len(names) > 0 {
		warns = append(warns, fmt.Sprintf("these apps have no deployment source (set a docker image or git repo): %s", strings.Join(names, ", ")))
	}
	for _, w := range warns {
		fmt.Fprintf(out, "warning: %s\n", w)
	}
	fmt.Fprintf(out, "Edit it, then deploy with: hostim tpl apply -f %s -p <project>\n", path)
	return nil
}

// templateToYAML renders a template as YAML using the JSON field names, so the
// output round-trips through loadTemplateFile.
func templateToYAML(tmpl *api.Template) ([]byte, error) {
	jb, err := json.Marshal(tmpl)
	if err != nil {
		return nil, err
	}
	var doc any
	if err := json.Unmarshal(jb, &doc); err != nil {
		return nil, err
	}
	return yaml.Marshal(doc)
}

// parseCompose sends a docker-compose file to the API, which converts it into a
// deployment template.
func parseCompose(ctx context.Context, out io.Writer, a *api.ClientWithResponses, path string) (*api.Template, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	fmt.Fprintln(out, "Converting compose file to a template (this can take up to a minute) ...")
	resp, err := a.ParseDockerComposeWithResponse(ctx, api.ParseDockerComposeJSONRequestBody{
		ComposeYAML: string(raw),
	})
	if err != nil {
		return nil, err
	}
	if err := checkResp(resp.StatusCode(), resp.Body); err != nil {
		return nil, err
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("compose conversion returned no template")
	}
	return resp.JSON200, nil
}

var generateMeRe = regexp.MustCompile(`^GENERATE_ME_(\d+)$`)

// expandGenerateMe replaces any env value of the form GENERATE_ME_<n> with an
// n-character random secret, mirroring the dashboard's template transform.
func expandGenerateMe(app *api.App) error {
	if app.EnvVars == nil {
		return nil
	}
	vars := *app.EnvVars
	for i := range vars {
		m := generateMeRe.FindStringSubmatch(vars[i].Value)
		if m == nil {
			continue
		}
		n, _ := strconv.Atoi(m[1])
		s, err := secureRandom(n)
		if err != nil {
			return err
		}
		vars[i].Value = s
	}
	return nil
}

const randAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

// secureRandom returns an n-character string drawn uniformly from randAlphabet
// using crypto/rand.
func secureRandom(n int) (string, error) {
	if n <= 0 {
		return "", nil
	}
	b := make([]byte, n)
	max := big.NewInt(int64(len(randAlphabet)))
	for i := range b {
		idx, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", fmt.Errorf("generating secret: %w", err)
		}
		b[i] = randAlphabet[idx.Int64()]
	}
	return string(b), nil
}

// checkPlans verifies every resource in the template has a plan set, so we fail
// fast rather than mid-deploy.
func checkPlans(t *api.Template) error {
	var missing []string
	for _, a := range t.Components.Apps {
		if a.Plan == "" {
			missing = append(missing, "app "+a.Name)
		}
	}
	for _, v := range t.Components.Volumes {
		if v.Plan == "" {
			missing = append(missing, "volume "+v.Name)
		}
	}
	for _, d := range t.Components.Postgres {
		if d.Plan == "" {
			missing = append(missing, "postgres "+d.Name)
		}
	}
	for _, d := range t.Components.Mysql {
		if d.Plan == "" {
			missing = append(missing, "mysql "+d.Name)
		}
	}
	for _, d := range t.Components.Redis {
		if d.Plan == "" {
			missing = append(missing, "redis "+d.Name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("template has resources without a plan: %s (set 'plan:' for each; see `hostim regions pricing`)", strings.Join(missing, ", "))
	}
	return nil
}

// nameRe mirrors the backend's DNS-1035 resource-name rule: lowercase letter
// first, then lowercase alphanumerics or '-', ending alphanumeric.
var nameRe = regexp.MustCompile(`^[a-z]([-a-z0-9]*[a-z0-9])?$`)

// maxNameLen mirrors the backend's 20-character cap on resource names.
const maxNameLen = 20

// validateNames checks every resource name against the platform's rules before
// apply creates anything, so a bad name (common in converted compose files)
// fails up front with a clear message instead of mid-deploy.
func validateNames(t *api.Template) error {
	var bad []string
	check := func(kind, name string) {
		switch {
		case len(name) > maxNameLen:
			bad = append(bad, fmt.Sprintf("%s %q: %d chars, max %d", kind, name, len(name), maxNameLen))
		case !nameRe.MatchString(name):
			bad = append(bad, fmt.Sprintf("%s %q: must be lowercase letters, digits and '-', start with a letter", kind, name))
		}
	}
	for _, v := range t.Components.Volumes {
		check("volume", v.Name)
	}
	for _, d := range t.Components.Postgres {
		check("postgres", d.Name)
	}
	for _, d := range t.Components.Mysql {
		check("mysql", d.Name)
	}
	for _, d := range t.Components.Redis {
		check("redis", d.Name)
	}
	for _, a := range t.Components.Apps {
		check("app", a.Name)
	}
	if len(bad) > 0 {
		return fmt.Errorf("some resource names are not valid:\n  %s\n"+
			"edit the template (or rename the compose services/volumes) and try again",
			strings.Join(bad, "\n  "))
	}
	return nil
}

// summarize renders a one-line component count for the list view.
func summarize(t *api.Template) string {
	var parts []string
	if n := len(t.Components.Apps); n > 0 {
		parts = append(parts, fmt.Sprintf("%d app", n))
	}
	if n := len(t.Components.Postgres); n > 0 {
		parts = append(parts, fmt.Sprintf("%d postgres", n))
	}
	if n := len(t.Components.Mysql); n > 0 {
		parts = append(parts, fmt.Sprintf("%d mysql", n))
	}
	if n := len(t.Components.Redis); n > 0 {
		parts = append(parts, fmt.Sprintf("%d redis", n))
	}
	if n := len(t.Components.Volumes); n > 0 {
		parts = append(parts, fmt.Sprintf("%d volume", n))
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, ", ")
}

// waitFor polls check until it returns true, the timeout elapses, or ctx is
// cancelled.
func waitFor(ctx context.Context, interval, timeout time.Duration, check func() (bool, error)) error {
	deadline := time.Now().Add(timeout)
	for {
		ok, err := check()
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out after %s", timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
}

// defaultRegion returns the only region the platform offers, so --region can be
// omitted while there is nothing to choose between.
func defaultRegion(ctx context.Context, a *api.ClientWithResponses) (string, error) {
	resp, err := a.GetRegionsWithResponse(ctx)
	if err != nil {
		return "", err
	}
	if err := checkResp(resp.StatusCode(), resp.Body); err != nil {
		return "", err
	}
	var names []string
	if resp.JSON200 != nil {
		names = *resp.JSON200
	}
	if len(names) == 1 {
		return names[0], nil
	}
	return "", fmt.Errorf("--region is required with --new-project (available: %s)", strings.Join(names, ", "))
}
