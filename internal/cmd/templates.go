package cmd

import (
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

	"github.com/hostimdev/cli/api"
	"github.com/hostimdev/cli/internal/client"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func newTemplatesCmd(c *cli) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "templates",
		Aliases: []string{"template", "tpl"},
		Short:   "List and deploy templates (including from a docker-compose file)",
	}
	cmd.AddCommand(templatesListCmd(c), templatesShowCmd(c), templatesApplyCmd(c))
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
			"  -f <file>            a local template YAML file\n" +
			"  --compose <file>     a docker-compose file, converted to a template via the API\n\n" +
			"The resources land in the resolved project (-p / current project) unless\n" +
			"--new-project is given, which creates a fresh project first.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runApply(cmd, c, f)
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&f.id, "id", "", "curated template ID to deploy")
	fl.StringVarP(&f.file, "file", "f", "", "local template YAML file to deploy")
	fl.StringVar(&f.compose, "compose", "", "docker-compose file to convert and deploy")
	fl.StringVar(&f.newProject, "new-project", "", "create a new project with this name before deploying")
	fl.StringVar(&f.region, "region", "", "region for --new-project")
	fl.BoolVar(&f.wait, "wait", true, "wait for each resource to become ready")
	fl.DurationVar(&f.timeout, "timeout", 15*time.Minute, "max time to wait per resource")
	fl.DurationVar(&f.interval, "poll-interval", 3*time.Second, "status poll interval")
	return cmd
}

func runApply(cmd *cobra.Command, c *cli, f *applyFlags) error {
	ctx := cmd.Context()
	out := cmd.OutOrStdout()

	// Exactly one source.
	n := 0
	for _, s := range []string{f.id, f.file, f.compose} {
		if s != "" {
			n++
		}
	}
	if n != 1 {
		return fmt.Errorf("provide exactly one of --id, --file or --compose")
	}

	a, err := c.Client()
	if err != nil {
		return err
	}

	// Load the template from the chosen source.
	var tmpl *api.Template
	switch {
	case f.id != "":
		tmpl, err = fetchTemplate(ctx, a, f.id)
	case f.file != "":
		tmpl, err = loadTemplateFile(f.file)
	case f.compose != "":
		tmpl, err = parseCompose(ctx, out, a, f.compose)
	}
	if err != nil {
		return err
	}

	// Resolve (or create) the target project.
	project, err := resolveApplyProject(ctx, out, c, a, f)
	if err != nil {
		return err
	}

	return applyTemplate(ctx, out, a, project, tmpl, f.wait, f.timeout, f.interval)
}

// resolveApplyProject creates a new project when --new-project is set, otherwise
// resolves the project from -p / HOSTIM_PROJECT / the current project.
func resolveApplyProject(ctx context.Context, out io.Writer, c *cli, a *api.ClientWithResponses, f *applyFlags) (string, error) {
	if f.newProject != "" {
		if f.region == "" {
			return "", fmt.Errorf("--region is required with --new-project")
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
func applyTemplate(ctx context.Context, out io.Writer, a *api.ClientWithResponses, project string, tmpl *api.Template, wait bool, timeout, interval time.Duration) error {
	comps := tmpl.Components

	// The dashboard replaces GENERATE_ME_<n> env values with a random secret
	// client-side; the backend does not, so we do the same here.
	for i := range comps.Apps {
		if err := expandGenerateMe(&comps.Apps[i]); err != nil {
			return err
		}
	}

	// Fail before creating anything if any resource lacks a plan.
	if err := checkPlans(tmpl); err != nil {
		return err
	}

	for _, v := range comps.Volumes {
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
				return fmt.Errorf("app %q: build failed", app.Name)
			case errors.Is(perr, client.ErrDeployFailed):
				return fmt.Errorf("app %q: did not become healthy (runtime: %s)", app.Name, dash(string(res.RuntimeStatus)))
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

// loadTemplateFile reads a single template from a local YAML file. YAML is
// converted to JSON so the struct's json tags (camelCase) decode correctly.
func loadTemplateFile(path string) (*api.Template, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	if _, isList := doc.([]any); isList {
		return nil, fmt.Errorf("%s contains a list of templates; --file expects a single template object", path)
	}
	jb, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	var t api.Template
	if err := json.Unmarshal(jb, &t); err != nil {
		return nil, fmt.Errorf("interpreting %s: %w", path, err)
	}
	if t.Name == "" {
		return nil, fmt.Errorf("%s is not a valid template (missing name)", path)
	}
	return &t, nil
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
