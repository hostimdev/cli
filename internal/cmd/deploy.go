package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/hostimdev/cli/api"
	"github.com/hostimdev/cli/internal/client"
)

type deployFlags struct {
	source     string
	git        string
	branch     string
	dockerfile string
	gitToken   string
	// uploadSha is filled just before applySource when --source is uploaded.
	uploadSha string

	dockerImage string
	dockerUser  string
	dockerPass  string

	plan     string
	replicas int
	port     int
	public   bool
	domains  []string
	volumes  []string
	env      []string
	envFile  string

	healthCheckPath string
	command         string
	// *Set records whether the flag was given at all, so an explicit empty
	// value ("--health-check-path=") clears the field instead of being
	// indistinguishable from "not passed".
	healthCheckPathSet bool
	commandSet         bool

	wait     bool
	timeout  time.Duration
	interval time.Duration
}

func newDeployCmd(c *cli) *cobra.Command {
	f := &deployFlags{}
	cmd := &cobra.Command{
		Use:   "deploy <app>",
		Short: "Create or update an app and wait for the build",
		Long: "Deploy an app: create it (from --source, --git or --docker-image) if it\n" +
			"does not exist, otherwise update its source and trigger a rebuild. --source\n" +
			"uploads a local directory and builds its Dockerfile. By default the\n" +
			"command waits for the build to finish and exits non-zero if it fails,\n" +
			"making it safe to use in CI pipelines.\n\n" +
			"This command deploys one app. An app that also needs a database, a redis\n" +
			"or a volume is deployed as a template: `hostim templates apply --id <id>\n" +
			"--save stack.yml` writes a curated stack to copy from, `--compose\n" +
			"docker-compose.yml` converts a compose file, `templates validate -f` checks\n" +
			"the result offline and `templates apply -f` creates every resource in order.\n\n" +
			"With -o json the command prints a single result object and nothing else;\n" +
			"failures print a JSON object on stderr and exit non-zero.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			f.healthCheckPathSet = cmd.Flags().Changed("health-check-path")
			f.commandSet = cmd.Flags().Changed("command")
			return runDeploy(cmd, c, args[0], f)
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&f.source, "source", "", "deploy a local directory: it is uploaded and built with its Dockerfile")
	fl.StringVar(&f.git, "git", "", "git repository URL")
	fl.StringVar(&f.branch, "branch", "", "git branch")
	fl.StringVar(&f.dockerfile, "dockerfile", "", "path to the Dockerfile in the repo or source dir; its directory is the build context")
	fl.StringVar(&f.gitToken, "git-token", "", "token for private git repos")
	fl.StringVar(&f.dockerImage, "docker-image", "", "docker image reference, registry host included")
	fl.StringVar(&f.dockerUser, "docker-user", "", "docker registry username")
	fl.StringVar(&f.dockerPass, "docker-pass", "", "docker registry password")
	fl.StringVar(&f.plan, "plan", "", "app plan (required when creating)")
	fl.IntVar(&f.replicas, "replicas", 1, "number of replicas (on create)")
	fl.IntVar(&f.port, "port", 0, "HTTP port (on create; required unless --public=false)")
	fl.BoolVar(&f.public, "public", true, "expose the app publicly (on create)")
	fl.StringArrayVar(&f.domains, "domain", nil, "custom domain (repeatable, on create)")
	fl.StringArrayVar(&f.volumes, "volume", nil, "mount a volume as name:mountPath (repeatable)")
	fl.StringArrayVar(&f.env, "env", nil, "environment variable as KEY=VALUE (repeatable, merges with existing)")
	fl.StringVar(&f.healthCheckPath, "health-check-path", "", "HTTP path for the readiness probe (empty value disables it)")
	fl.StringVar(&f.command, "command", "", "override the container command (empty value restores the image default)")
	fl.StringVar(&f.envFile, "env-file", "", "read environment variables from a .env file (merges with existing)")
	fl.BoolVar(&f.wait, "wait", true, "wait for the build to finish (use --wait=false for fire-and-forget)")
	fl.DurationVar(&f.timeout, "timeout", 15*time.Minute, "max time to wait for the build")
	fl.DurationVar(&f.interval, "poll-interval", 3*time.Second, "status poll interval")
	return cmd
}

func runDeploy(cmd *cobra.Command, c *cli, appName string, f *deployFlags) error {
	if err := validateSourceFlags(f); err != nil {
		return err
	}
	a, project, err := c.clientAndProject(cmd.Context())
	if err != nil {
		return err
	}
	ctx := cmd.Context()

	// Does the app already exist?
	getResp, err := a.GetAppWithResponse(ctx, project, appName)
	if err != nil {
		return err
	}
	exists := getResp.StatusCode() == 200 && getResp.JSON200 != nil

	// srcType is the app's final deployment source ("git" or "docker"), used
	// below to decide whether to wait on a build or on runtime readiness.
	var srcType string
	// since is the app's state before this run changes it (nil on create), so
	// the poll does not mistake the previous build or rollout for this one.
	var since *client.Baseline
	// action is what this run did to the app, reported in the -o json result.
	action := "rebuilding"

	if exists {
		app := *getResp.JSON200
		since, err = client.NewBaseline(ctx, a, project, appName)
		if err != nil {
			return err
		}
		cur, err := loadSource(app.DeploymentSource)
		if err != nil {
			return err
		}
		if f.source != "" {
			if f.uploadSha, err = prepareUpload(ctx, a, project, f, cur, func(msg string) {
				c.progress(cmd, "  %s\n", msg)
			}); err != nil {
				return err
			}
		}
		oldImage := dockerImageOf(app)
		changed, err := applySource(&app, f)
		if err != nil {
			return err
		}
		srcType = deploymentType(app.DeploymentSource)
		// A rebuild does not roll a docker app's pods, so only a new image
		// guarantees a new rollout to wait for. Otherwise the current runtime
		// status is the answer.
		if srcType == string(api.Docker) && dockerImageOf(app) == oldImage {
			since.LastDeployedAt = nil
		}
		if changed {
			// domains/volumeMounts are required, non-nullable arrays; an app
			// fetched with none set has them as nil, which marshals to null and
			// is rejected on PUT. Send [] instead.
			client.NormalizeApp(&app)
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
			if srcType == string(api.UploadSource) && reb.StatusCode() == http.StatusConflict {
				return fmt.Errorf("the uploaded source expired after 7 days; run `hostim deploy %s --source <dir>` again", appName)
			}
			return err
		}
		c.progress(cmd, "Rebuilding %q...\n", appName)
	} else {
		if getResp.StatusCode() != 404 {
			// A non-404 error is a real failure (auth, server), not "missing app".
			if err := checkResp(getResp.StatusCode(), getResp.Body); err != nil {
				return err
			}
		}
		if err := checkNewAppFlags(appName, f); err != nil {
			return err
		}
		if f.source != "" {
			if f.uploadSha, err = prepareUpload(ctx, a, project, f, deploymentSource{}, func(msg string) {
				c.progress(cmd, "  %s\n", msg)
			}); err != nil {
				return err
			}
		}
		app, err := buildNewApp(appName, f)
		if err != nil {
			return err
		}
		srcType = deploymentType(app.DeploymentSource)
		crt, err := a.CreateAppWithResponse(ctx, project, app)
		if err != nil {
			return err
		}
		if err := checkResp(crt.StatusCode(), crt.Body); err != nil {
			return err
		}
		action = "created"
		c.progress(cmd, "Created %q, building...\n", appName)
	}

	if !f.wait {
		return c.result(cmd, res("queued", "app", appName, deployResultKV(f, action)...),
			"Not waiting for build (--wait=false).")
	}

	// A git or upload source builds an image before running, so the build
	// outcome is what we wait on. A docker-image source has no build phase
	// (buildStatus stays empty), so we wait on runtime readiness instead.
	expectBuild := srcType == string(api.Git) || srcType == string(api.UploadSource)

	ctx, cancel := context.WithTimeout(ctx, f.timeout)
	defer cancel()

	var lastBuild api.AppStatusBuildStatus
	var lastRuntime api.AppStatusRuntimeStatus
	final, err := client.PollBuild(ctx, a, project, appName, f.interval, expectBuild, since, func(st *api.AppStatus) {
		if st.BuildStatus != nil && *st.BuildStatus != "" && *st.BuildStatus != lastBuild {
			lastBuild = *st.BuildStatus
			c.progress(cmd, "  build: %s\n", *st.BuildStatus)
		}
		if st.RuntimeStatus != nil && *st.RuntimeStatus != lastRuntime {
			lastRuntime = *st.RuntimeStatus
			c.progress(cmd, "  runtime: %s\n", *st.RuntimeStatus)
		}
	})
	if err != nil {
		if errors.Is(err, client.ErrBuildFailed) {
			return fmt.Errorf("deploy failed: build did not succeed; see `hostim logs %s --build`", appName)
		}
		if errors.Is(err, client.ErrDeployFailed) {
			return fmt.Errorf("deploy failed: app did not become healthy (runtime: %s); see `hostim logs %s` and `hostim events`",
				dash(string(final.RuntimeStatus)), appName)
		}
		if errors.Is(err, context.DeadlineExceeded) {
			what := "build"
			if !expectBuild {
				what = "app to become healthy"
			}
			return fmt.Errorf("timed out after %s waiting for %s; check `hostim status %s` or raise --timeout", f.timeout, what, appName)
		}
		return err
	}
	kv := append(deployResultKV(f, action),
		"buildStatus", string(final.BuildStatus),
		"runtimeStatus", string(final.RuntimeStatus))
	return c.result(cmd, res("ok", "app", appName, kv...),
		"Deploy succeeded (runtime: %s).", dash(string(final.RuntimeStatus)))
}

// deployResultKV is the result spine shared by the waiting and --wait=false
// paths, including the archive hash when this run uploaded a source.
func deployResultKV(f *deployFlags, action string) []any {
	kv := []any{"action", action}
	if f.uploadSha != "" {
		kv = append(kv, "sha256", f.uploadSha)
	}
	return kv
}

// validateSourceFlags rejects flag combinations that would silently pick one
// source over another.
func validateSourceFlags(f *deployFlags) error {
	if f.source != "" && (f.git != "" || f.dockerImage != "") {
		return fmt.Errorf("--source cannot be combined with --git or --docker-image")
	}
	return nil
}

// prepareUpload packs and uploads the --source directory, checking first that
// its Dockerfile exists. cur is the app's current source, so a re-deploy keeps
// its dockerfilepath when --dockerfile is not given.
func prepareUpload(ctx context.Context, a *api.ClientWithResponses, project string, f *deployFlags, cur deploymentSource, progress func(string)) (string, error) {
	dockerfile := f.dockerfile
	if dockerfile == "" && cur.Type == string(api.UploadSource) && cur.Upload != nil {
		dockerfile = str(cur.Upload.Dockerfilepath)
	}
	if dockerfile == "" {
		dockerfile = "Dockerfile"
	}
	if !client.HasDockerfile(f.source, dockerfile) {
		return "", fmt.Errorf("no Dockerfile at %s in %s\n\n"+
			"Write a Dockerfile for the app (a multi-stage Dockerfile is fine), or pass\n"+
			"--dockerfile <path> if it is at a different path inside the directory", dockerfile, f.source)
	}
	return client.Upload(ctx, a, project, f.source, progress)
}

// deploymentSource mirrors the (inline, anonymous) App.DeploymentSource shape so
// we can construct/patch it with named types and copy it back via a JSON
// round-trip, avoiding brittle anonymous-struct type matching.
type deploymentSource struct {
	Type   string        `json:"type"`
	Git    *gitSource    `json:"git,omitempty"`
	Docker *dockSource   `json:"docker,omitempty"`
	Upload *uploadSource `json:"upload,omitempty"`
}

// loadSource copies an App's inline DeploymentSource into the named
// deploymentSource type via a JSON round-trip.
func loadSource(ds any) (deploymentSource, error) {
	var s deploymentSource
	b, err := json.Marshal(ds)
	if err != nil {
		return s, err
	}
	err = json.Unmarshal(b, &s)
	return s, err
}

type gitSource struct {
	Url            string  `json:"url"`
	Branch         *string `json:"branch,omitempty"`
	Dockerfilepath *string `json:"dockerfilepath,omitempty"`
	Token          *string `json:"token,omitempty"`
}

type dockSource struct {
	Image    string  `json:"image"`
	Username *string `json:"username,omitempty"`
	Password *string `json:"password,omitempty"`
}

type uploadSource struct {
	Sha256         string  `json:"sha256"`
	Dockerfilepath *string `json:"dockerfilepath,omitempty"`
}

// deploymentType reads the "type" discriminator ("git" or "docker") out of an
// App's inline DeploymentSource.
func deploymentType(ds any) string {
	s, err := loadSource(ds)
	if err != nil {
		return ""
	}
	return s.Type
}

// dockerImageOf reads the docker image reference out of an App's inline
// DeploymentSource, or "" when the app is not a docker source.
func dockerImageOf(app api.App) string {
	s, err := loadSource(app.DeploymentSource)
	if err != nil || s.Docker == nil {
		return ""
	}
	return s.Docker.Image
}

// applySource patches app.DeploymentSource from any deploy flags that were set,
// returning true if anything changed (so the caller knows to PUT).
func applySource(app *api.App, f *deployFlags) (bool, error) {
	cur, err := loadSource(app.DeploymentSource)
	if err != nil {
		return false, err
	}

	prevType := cur.Type
	changed := false
	if f.git != "" {
		cur.Type = string(api.Git)
		if cur.Git == nil {
			cur.Git = &gitSource{}
		}
		cur.Git.Url = f.git
		cur.Docker = nil
		cur.Upload = nil
		changed = true
	}
	if f.dockerImage != "" {
		cur.Type = string(api.Docker)
		if cur.Docker == nil {
			cur.Docker = &dockSource{}
		}
		cur.Docker.Image = f.dockerImage
		cur.Git = nil
		cur.Upload = nil
		changed = true
	}
	if f.source != "" {
		cur.Type = string(api.UploadSource)
		if cur.Upload == nil {
			cur.Upload = &uploadSource{}
		}
		cur.Upload.Sha256 = f.uploadSha
		cur.Git = nil
		cur.Docker = nil
		changed = true
		// A new upload source defaults to Dockerfile; an existing upload app
		// keeps its dockerfilepath unless --dockerfile overrides it below.
		if f.dockerfile == "" && (prevType != string(api.UploadSource) || cur.Upload.Dockerfilepath == nil) {
			df := "Dockerfile"
			cur.Upload.Dockerfilepath = &df
		}
	}
	// Source options patch the current source on their own, so --branch alone
	// switches the branch of a git app (HOS-694) instead of being dropped.
	if f.branch != "" || f.gitToken != "" {
		if cur.Type != string(api.Git) || cur.Git == nil {
			return false, fmt.Errorf("--branch and --git-token need a git source: pass --git <url>")
		}
		setOpt(&cur.Git.Branch, f.branch)
		setOpt(&cur.Git.Token, f.gitToken)
		changed = true
	}
	if f.dockerfile != "" {
		switch cur.Type {
		case string(api.Git):
			if cur.Git == nil {
				cur.Git = &gitSource{}
			}
			setOpt(&cur.Git.Dockerfilepath, f.dockerfile)
		case string(api.UploadSource):
			if cur.Upload == nil {
				cur.Upload = &uploadSource{}
			}
			setOpt(&cur.Upload.Dockerfilepath, f.dockerfile)
		default:
			return false, fmt.Errorf("--dockerfile needs a git or upload source: pass --git <url> or --source <dir>")
		}
		changed = true
	}
	if f.dockerUser != "" || f.dockerPass != "" {
		if cur.Type != string(api.Docker) || cur.Docker == nil {
			return false, fmt.Errorf("--docker-user and --docker-pass need a docker source: pass --docker-image <image>")
		}
		setOpt(&cur.Docker.Username, f.dockerUser)
		setOpt(&cur.Docker.Password, f.dockerPass)
		changed = true
	}
	if f.healthCheckPathSet {
		app.HealthCheckPath = &f.healthCheckPath
		changed = true
	}
	if f.commandSet {
		app.CommandOverride = &f.command
		changed = true
	}
	if len(f.volumes) > 0 {
		mounts, err := client.ParseVolumeMounts(f.volumes)
		if err != nil {
			return false, err
		}
		app.VolumeMounts = mounts
		changed = true
	}
	updates, err := deployEnv(f)
	if err != nil {
		return false, err
	}
	if len(updates) > 0 {
		var cur []api.EnvVar
		if app.EnvVars != nil {
			cur = *app.EnvVars
		}
		merged := client.MergeVars(cur, updates)
		app.EnvVars = &merged
		changed = true
	}

	if !changed {
		return false, nil
	}

	// Copy the patched source back into the generated App. Reset first, since
	// unmarshalling into an existing struct keeps fields missing from the JSON
	// and would leave the old source type behind.
	nb, err := json.Marshal(cur)
	if err != nil {
		return false, err
	}
	app.DeploymentSource = api.App{}.DeploymentSource
	if err := json.Unmarshal(nb, &app.DeploymentSource); err != nil {
		return false, err
	}
	return true, nil
}

func buildNewApp(name string, f *deployFlags) (api.App, error) {
	if err := checkNewAppFlags(name, f); err != nil {
		return api.App{}, err
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
	app.VolumeMounts = []client.VolumeMount{}
	if f.port > 0 {
		app.HttpPort = &f.port
	}
	if _, err := applySource(&app, f); err != nil {
		return api.App{}, err
	}
	return app, nil
}

// checkNewAppFlags reports everything missing before an app can be created, in
// one error rather than one per re-run. An app is built from a local directory
// uploaded with --source, from a git URL the API can reach, or runs a docker
// image.
func checkNewAppFlags(name string, f *deployFlags) error {
	var missing []string
	var lines []string
	if f.plan == "" {
		missing = append(missing, "--plan")
		lines = append(lines, "  --plan <plan>            list plans with `hostim regions pricing <region>`")
	}
	if f.source == "" && f.git == "" && f.dockerImage == "" {
		missing = append(missing, "--source|--git|--docker-image")
		lines = append(lines, "  --source <dir>, --git <url> or --docker-image <ref>\n"+
			"                           upload a local directory and build its Dockerfile,\n"+
			"                           build from a git URL the API can reach, or run a\n"+
			"                           published docker image")
	}
	if f.public && f.port == 0 {
		missing = append(missing, "--port")
		lines = append(lines, "  --port <port>            the port the app listens on; a worker with no HTTP\n"+
			"                           takes --public=false instead")
	}
	if len(missing) == 0 {
		return nil
	}
	return &missingError{
		Missing: missing,
		msg: fmt.Sprintf("creating app %q needs:\n%s\n\n"+
			"An app that also needs a database or a volume is a template, not a deploy:\n"+
			"  hostim templates apply --id <id> --save stack.yml    a curated stack to copy\n"+
			"  hostim templates apply --compose docker-compose.yml --save stack.yml\n"+
			"  hostim templates validate -f stack.yml               offline check, no API calls\n"+
			"  hostim templates apply -f stack.yml                  create everything, in order",
			name, strings.Join(lines, "\n")),
	}
}

// deployEnv collects the env vars given via --env-file and --env, with --env
// taking precedence over the file.
func deployEnv(f *deployFlags) ([]api.EnvVar, error) {
	var out []api.EnvVar
	if f.envFile != "" {
		fromFile, err := parseEnvFile(f.envFile)
		if err != nil {
			return nil, err
		}
		out = fromFile
	}
	if len(f.env) > 0 {
		kv, err := parseKV(f.env)
		if err != nil {
			return nil, err
		}
		out = client.MergeVars(out, kv)
	}
	return out, nil
}

// setOpt sets *dst to a copy of v when v is non-empty.
func setOpt(dst **string, v string) {
	if v != "" {
		s := v
		*dst = &s
	}
}
