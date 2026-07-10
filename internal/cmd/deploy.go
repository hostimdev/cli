package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/hostimdev/cli/api"
	"github.com/hostimdev/cli/internal/client"
	"github.com/spf13/cobra"
)

type deployFlags struct {
	git        string
	branch     string
	dockerfile string
	gitToken   string

	dockerImage string
	registry    string
	dockerUser  string
	dockerPass  string

	plan     string
	replicas int
	port     int
	public   bool
	domains  []string

	wait     bool
	timeout  time.Duration
	interval time.Duration
}

func newDeployCmd(c *cli) *cobra.Command {
	f := &deployFlags{}
	cmd := &cobra.Command{
		Use:   "deploy <app>",
		Short: "Create or update an app and wait for the build",
		Long: "Deploy an app: create it (from --git or --docker-image) if it does not\n" +
			"exist, otherwise update its source and trigger a rebuild. By default the\n" +
			"command waits for the build to finish and exits non-zero if it fails,\n" +
			"making it safe to use in CI pipelines.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDeploy(cmd, c, args[0], f)
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&f.git, "git", "", "git repository URL")
	fl.StringVar(&f.branch, "branch", "", "git branch")
	fl.StringVar(&f.dockerfile, "dockerfile", "", "path to the Dockerfile in the repo")
	fl.StringVar(&f.gitToken, "git-token", "", "token for private git repos")
	fl.StringVar(&f.dockerImage, "docker-image", "", "docker image reference")
	fl.StringVar(&f.registry, "registry", "", "docker registry")
	fl.StringVar(&f.dockerUser, "docker-user", "", "docker registry username")
	fl.StringVar(&f.dockerPass, "docker-pass", "", "docker registry password")
	fl.StringVar(&f.plan, "plan", "", "app plan (required when creating)")
	fl.IntVar(&f.replicas, "replicas", 1, "number of replicas (on create)")
	fl.IntVar(&f.port, "port", 0, "HTTP port (on create)")
	fl.BoolVar(&f.public, "public", true, "expose the app publicly (on create)")
	fl.StringArrayVar(&f.domains, "domain", nil, "custom domain (repeatable, on create)")
	fl.BoolVar(&f.wait, "wait", true, "wait for the build to finish (use --wait=false for fire-and-forget)")
	fl.DurationVar(&f.timeout, "timeout", 15*time.Minute, "max time to wait for the build")
	fl.DurationVar(&f.interval, "poll-interval", 3*time.Second, "status poll interval")
	return cmd
}

func runDeploy(cmd *cobra.Command, c *cli, appName string, f *deployFlags) error {
	a, project, err := c.clientAndProject(cmd.Context())
	if err != nil {
		return err
	}
	ctx := cmd.Context()
	out := cmd.OutOrStdout()

	// Does the app already exist?
	getResp, err := a.GetAppWithResponse(ctx, project, appName)
	if err != nil {
		return err
	}
	exists := getResp.StatusCode() == 200 && getResp.JSON200 != nil

	if exists {
		app := *getResp.JSON200
		changed, err := applySource(&app, f)
		if err != nil {
			return err
		}
		if changed {
			upd, err := a.UpdateAppWithResponse(ctx, project, appName, app)
			if err != nil {
				return err
			}
			if err := checkResp(upd.StatusCode(), upd.Body); err != nil {
				return err
			}
		}
		reb, err := a.RebuildAppWithResponse(ctx, project, appName)
		if err != nil {
			return err
		}
		if err := checkResp(reb.StatusCode(), reb.Body); err != nil {
			return err
		}
		fmt.Fprintf(out, "Rebuilding %q...\n", appName)
	} else {
		if getResp.StatusCode() != 404 {
			// A non-404 error is a real failure (auth, server), not "missing app".
			if err := checkResp(getResp.StatusCode(), getResp.Body); err != nil {
				return err
			}
		}
		app, err := buildNewApp(appName, f)
		if err != nil {
			return err
		}
		crt, err := a.CreateAppWithResponse(ctx, project, app)
		if err != nil {
			return err
		}
		if err := checkResp(crt.StatusCode(), crt.Body); err != nil {
			return err
		}
		fmt.Fprintf(out, "Created %q, building...\n", appName)
	}

	if !f.wait {
		fmt.Fprintln(out, "Not waiting for build (--wait=false).")
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, f.timeout)
	defer cancel()

	var last api.AppStatusBuildStatus
	res, err := client.PollBuild(ctx, a, project, appName, f.interval, func(st *api.AppStatus) {
		if st.BuildStatus != nil && *st.BuildStatus != last {
			last = *st.BuildStatus
			fmt.Fprintf(out, "  build: %s\n", *st.BuildStatus)
		}
	})
	if err != nil {
		if errors.Is(err, client.ErrBuildFailed) {
			return fmt.Errorf("deploy failed: build did not succeed")
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("timed out after %s waiting for build", f.timeout)
		}
		return err
	}
	fmt.Fprintf(out, "Deploy succeeded (runtime: %s).\n", dash(string(res.RuntimeStatus)))
	return nil
}

// deploymentSource mirrors the (inline, anonymous) App.DeploymentSource shape so
// we can construct/patch it with named types and copy it back via a JSON
// round-trip, avoiding brittle anonymous-struct type matching.
type deploymentSource struct {
	Type   string      `json:"type"`
	Git    *gitSource  `json:"git,omitempty"`
	Docker *dockSource `json:"docker,omitempty"`
}

type gitSource struct {
	Url            string  `json:"url"`
	Branch         *string `json:"branch,omitempty"`
	Dockerfilepath *string `json:"dockerfilepath,omitempty"`
	Token          *string `json:"token,omitempty"`
}

type dockSource struct {
	Image    string  `json:"image"`
	Registry *string `json:"registry,omitempty"`
	Username *string `json:"username,omitempty"`
	Password *string `json:"password,omitempty"`
}

// applySource patches app.DeploymentSource from any deploy flags that were set,
// returning true if anything changed (so the caller knows to PUT).
func applySource(app *api.App, f *deployFlags) (bool, error) {
	// Load the current source into our named type.
	cur := deploymentSource{}
	b, err := json.Marshal(app.DeploymentSource)
	if err != nil {
		return false, err
	}
	if err := json.Unmarshal(b, &cur); err != nil {
		return false, err
	}

	changed := false
	if f.git != "" {
		cur.Type = string(api.Git)
		if cur.Git == nil {
			cur.Git = &gitSource{}
		}
		cur.Git.Url = f.git
		setOpt(&cur.Git.Branch, f.branch)
		setOpt(&cur.Git.Dockerfilepath, f.dockerfile)
		setOpt(&cur.Git.Token, f.gitToken)
		cur.Docker = nil
		changed = true
	}
	if f.dockerImage != "" {
		cur.Type = string(api.Docker)
		if cur.Docker == nil {
			cur.Docker = &dockSource{}
		}
		cur.Docker.Image = f.dockerImage
		setOpt(&cur.Docker.Registry, f.registry)
		setOpt(&cur.Docker.Username, f.dockerUser)
		setOpt(&cur.Docker.Password, f.dockerPass)
		cur.Git = nil
		changed = true
	}
	if !changed {
		return false, nil
	}

	// Copy the patched source back into the generated App.
	nb, err := json.Marshal(cur)
	if err != nil {
		return false, err
	}
	if err := json.Unmarshal(nb, &app.DeploymentSource); err != nil {
		return false, err
	}
	return true, nil
}

func buildNewApp(name string, f *deployFlags) (api.App, error) {
	if f.plan == "" {
		return api.App{}, fmt.Errorf("--plan is required to create a new app")
	}
	if f.git == "" && f.dockerImage == "" {
		return api.App{}, fmt.Errorf("provide --git or --docker-image to create a new app")
	}
	app := api.App{
		Name:     name,
		Plan:     f.plan,
		Replicas: f.replicas,
		Public:   f.public,
		Domains:  f.domains,
	}
	if app.Domains == nil {
		app.Domains = []string{}
	}
	// volumeMounts is a required array; send [] rather than null on create.
	app.VolumeMounts = []struct {
		MountPath *string `json:"mountPath,omitempty"`
		Name      *string `json:"name,omitempty"`
	}{}
	if f.port > 0 {
		app.HttpPort = &f.port
	}
	if _, err := applySource(&app, f); err != nil {
		return api.App{}, err
	}
	return app, nil
}

// setOpt sets *dst to a copy of v when v is non-empty.
func setOpt(dst **string, v string) {
	if v != "" {
		s := v
		*dst = &s
	}
}
