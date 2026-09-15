package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"

	"github.com/hostimdev/cli/api"
	"github.com/hostimdev/cli/internal/client"
)

type createProjectArgs struct {
	Name   string `json:"name" jsonschema:"project name; lowercase letters, digits and dashes"`
	Region string `json:"region" jsonschema:"region to create the project in; list them with list_regions"`
}

type deleteProjectArgs struct {
	Project string `json:"project" jsonschema:"project name or hpr-... ID; list them with list_projects"`
}

// gitSource and dockerSource mirror the wire shape of an app's deployment
// source. The generated App type holds that shape as an anonymous struct, so
// the source is built here and copied across with a JSON round-trip.
type gitSource struct {
	URL            string `json:"url" jsonschema:"git repository URL the Hostim API can clone"`
	Branch         string `json:"branch,omitempty" jsonschema:"branch to build; defaults to the repository default"`
	DockerfilePath string `json:"dockerfilepath,omitempty" jsonschema:"path to the Dockerfile inside the repository"`
	Token          string `json:"token,omitempty" jsonschema:"access token, for a private repository"`
}

type dockerSource struct {
	Image    string `json:"image" jsonschema:"image reference, e.g. nginx:1.27"`
	Registry string `json:"registry,omitempty" jsonschema:"registry host, if the image is not on Docker Hub"`
	Username string `json:"username,omitempty" jsonschema:"registry username"`
	Password string `json:"password,omitempty" jsonschema:"registry password"`
}

type appSource struct {
	Type   string        `json:"type" jsonschema:"git or docker"`
	Git    *gitSource    `json:"git,omitempty"`
	Docker *dockerSource `json:"docker,omitempty"`
}

type createAppArgs struct {
	Project  string            `json:"project" jsonschema:"project name or hpr-... ID; list them with list_projects"`
	Name     string            `json:"name" jsonschema:"app name"`
	Plan     string            `json:"plan" jsonschema:"app plan; list them with list_region_plans"`
	Source   appSource         `json:"source" jsonschema:"where the app is built from"`
	Replicas int               `json:"replicas,omitempty" jsonschema:"number of replicas; default 1"`
	HTTPPort int               `json:"http_port,omitempty" jsonschema:"container HTTP port to route traffic to"`
	Public   bool              `json:"public,omitempty" jsonschema:"expose the app on the internet; default false"`
	Domains  []string          `json:"domains,omitempty" jsonschema:"custom domains to attach"`
	Env      map[string]string `json:"env,omitempty" jsonschema:"environment variables to set"`
	Volumes  []string          `json:"volumes,omitempty" jsonschema:"volume mounts, each as name:/mount/path"`
}

type updateAppArgs struct {
	Project         string  `json:"project" jsonschema:"project name or hpr-... ID; list them with list_projects"`
	App             string  `json:"app" jsonschema:"app name; list them with list_apps"`
	Plan            string  `json:"plan,omitempty" jsonschema:"new plan; list them with list_region_plans"`
	Replicas        *int    `json:"replicas,omitempty" jsonschema:"number of replicas"`
	HTTPPort        *int    `json:"http_port,omitempty" jsonschema:"container HTTP port to route traffic to"`
	Public          *bool   `json:"public,omitempty" jsonschema:"expose the app on the internet"`
	HealthCheckPath *string `json:"health_check_path,omitempty" jsonschema:"HTTP path for the readiness probe; the empty string disables it"`
	Command         *string `json:"command,omitempty" jsonschema:"container command override; the empty string restores the image default"`
}

type appActionArgs struct {
	Project string `json:"project" jsonschema:"project name or hpr-... ID; list them with list_projects"`
	App     string `json:"app" jsonschema:"app name; list them with list_apps"`
}

type setAppEnvArgs struct {
	Project string            `json:"project" jsonschema:"project name or hpr-... ID; list them with list_projects"`
	App     string            `json:"app" jsonschema:"app name; list them with list_apps"`
	Vars    map[string]string `json:"vars" jsonschema:"environment variables to set or overwrite; existing vars not listed are kept"`
}

type unsetAppEnvArgs struct {
	Project string   `json:"project" jsonschema:"project name or hpr-... ID; list them with list_projects"`
	App     string   `json:"app" jsonschema:"app name; list them with list_apps"`
	Keys    []string `json:"keys" jsonschema:"environment variable names to remove"`
}

type addDomainArgs struct {
	Project string `json:"project" jsonschema:"project name or hpr-... ID; list them with list_projects"`
	App     string `json:"app" jsonschema:"app name; list them with list_apps"`
	Domain  string `json:"domain" jsonschema:"domain to attach, e.g. app.example.com"`
}

type createDatabaseArgs struct {
	Project string `json:"project" jsonschema:"project name or hpr-... ID; list them with list_projects"`
	Kind    string `json:"kind" jsonschema:"database engine: mysql, postgres or redis"`
	Name    string `json:"name" jsonschema:"database name"`
	Plan    string `json:"plan" jsonschema:"database plan; list them with list_region_plans"`
}

type deleteDatabaseArgs struct {
	Project string `json:"project" jsonschema:"project name or hpr-... ID; list them with list_projects"`
	Kind    string `json:"kind" jsonschema:"database engine: mysql, postgres or redis"`
	Name    string `json:"name" jsonschema:"database name; list them with list_databases"`
}

type createVolumeArgs struct {
	Project string `json:"project" jsonschema:"project name or hpr-... ID; list them with list_projects"`
	Name    string `json:"name" jsonschema:"volume name"`
	Plan    string `json:"plan" jsonschema:"volume plan; list them with list_region_plans"`
}

type deleteVolumeArgs struct {
	Project string `json:"project" jsonschema:"project name or hpr-... ID; list them with list_projects"`
	Name    string `json:"name" jsonschema:"volume name; list them with list_volumes"`
}

func (s *Server) registerWriteTools() {
	addWrite(s.srv, "create_project", "Create project",
		"Create a new project in a region. A project is the container every app, database and volume lives in.",
		false, func(ctx context.Context, in createProjectArgs) (any, error) {
			resp, err := s.api.CreateProjectWithResponse(ctx, &api.CreateProjectParams{
				Name:       in.Name,
				RegionName: in.Region,
			})
			if err != nil {
				return nil, err
			}
			return ack("created", "project", in.Name, resp.StatusCode(), resp.Body)
		})

	addWrite(s.srv, "delete_project", "Delete project",
		"Delete a project and everything in it. This cannot be undone.",
		true, func(ctx context.Context, in deleteProjectArgs) (any, error) {
			id, err := s.projectID(ctx, in.Project)
			if err != nil {
				return nil, err
			}
			resp, err := s.api.DeleteProjectWithResponse(ctx, id)
			if err != nil {
				return nil, err
			}
			return ack("deleted", "project", in.Project, resp.StatusCode(), resp.Body)
		})

	addWrite(s.srv, "create_app", "Create app",
		"Create an app in a project and start its first build. The app is built from a git repository the Hostim API clones, or from a published docker image. Poll get_app_status until the build finishes.",
		false, func(ctx context.Context, in createAppArgs) (any, error) {
			id, err := s.projectID(ctx, in.Project)
			if err != nil {
				return nil, err
			}
			app, err := buildApp(in)
			if err != nil {
				return nil, err
			}
			resp, err := s.api.CreateAppWithResponse(ctx, id, app)
			if err != nil {
				return nil, err
			}
			return ack("created", "app", in.Name, resp.StatusCode(), resp.Body)
		})

	addWrite(s.srv, "update_app", "Update app",
		"Change an app's plan, replica count, HTTP port, visibility, health-check path or command override. Fields left out are unchanged; the deployment source cannot be changed here.",
		false, func(ctx context.Context, in updateAppArgs) (any, error) {
			id, err := s.projectID(ctx, in.Project)
			if err != nil {
				return nil, err
			}
			cur, err := s.api.GetAppWithResponse(ctx, id, in.App)
			if err != nil {
				return nil, err
			}
			if err := client.Check(cur.StatusCode(), cur.Body); err != nil {
				return nil, err
			}
			app := cur.JSON200
			if app == nil {
				return nil, client.ErrEmptyResponse
			}
			if in.Plan != "" {
				app.Plan = in.Plan
			}
			if in.Replicas != nil {
				app.Replicas = *in.Replicas
			}
			if in.HTTPPort != nil {
				app.HttpPort = in.HTTPPort
			}
			if in.Public != nil {
				app.Public = *in.Public
			}
			if in.HealthCheckPath != nil {
				app.HealthCheckPath = in.HealthCheckPath
			}
			if in.Command != nil {
				app.CommandOverride = in.Command
			}
			client.NormalizeApp(app)
			resp, err := s.api.UpdateAppWithResponse(ctx, id, in.App, *app)
			if err != nil {
				return nil, err
			}
			return ack("updated", "app", in.App, resp.StatusCode(), resp.Body)
		})

	addWrite(s.srv, "delete_app", "Delete app",
		"Delete an app. This cannot be undone.",
		true, func(ctx context.Context, in appActionArgs) (any, error) {
			id, err := s.projectID(ctx, in.Project)
			if err != nil {
				return nil, err
			}
			resp, err := s.api.DeleteAppWithResponse(ctx, id, in.App)
			if err != nil {
				return nil, err
			}
			return ack("deleted", "app", in.App, resp.StatusCode(), resp.Body)
		})

	addWrite(s.srv, "restart_app", "Restart app",
		"Restart a running app without rebuilding it.",
		false, func(ctx context.Context, in appActionArgs) (any, error) {
			id, err := s.projectID(ctx, in.Project)
			if err != nil {
				return nil, err
			}
			resp, err := s.api.RestartAppWithResponse(ctx, id, in.App)
			if err != nil {
				return nil, err
			}
			return ack("restarted", "app", in.App, resp.StatusCode(), resp.Body)
		})

	addWrite(s.srv, "rebuild_app", "Rebuild app",
		"Trigger a new build of an app from its deployment source. Poll get_app_status until it finishes.",
		false, func(ctx context.Context, in appActionArgs) (any, error) {
			id, err := s.projectID(ctx, in.Project)
			if err != nil {
				return nil, err
			}
			resp, err := s.api.RebuildAppWithResponse(ctx, id, in.App)
			if err != nil {
				return nil, err
			}
			return ack("rebuilding", "app", in.App, resp.StatusCode(), resp.Body)
		})

	addWrite(s.srv, "set_app_env", "Set app environment variables",
		"Set or overwrite app environment variables. Variables not listed are kept. The app needs a rebuild or restart to pick them up.",
		false, func(ctx context.Context, in setAppEnvArgs) (any, error) {
			id, err := s.projectID(ctx, in.Project)
			if err != nil {
				return nil, err
			}
			cur, err := s.api.GetAppEnvWithResponse(ctx, id, in.App)
			if err != nil {
				return nil, err
			}
			if err := client.Check(cur.StatusCode(), cur.Body); err != nil {
				return nil, err
			}
			var base []api.EnvVar
			if cur.JSON200 != nil {
				base = *cur.JSON200
			}
			updates := make([]api.EnvVar, 0, len(in.Vars))
			for _, k := range slices.Sorted(maps.Keys(in.Vars)) {
				updates = append(updates, api.EnvVar{Name: k, Value: in.Vars[k]})
			}
			vars := client.MergeVars(base, updates)
			resp, err := s.api.SetAppEnvWithResponse(ctx, id, in.App, vars)
			if err != nil {
				return nil, err
			}
			return ack("updated", "env", in.App, resp.StatusCode(), resp.Body)
		})

	addWrite(s.srv, "unset_app_env", "Unset app environment variables",
		"Remove app environment variables by name. The app needs a rebuild or restart to pick the change up.",
		true, func(ctx context.Context, in unsetAppEnvArgs) (any, error) {
			id, err := s.projectID(ctx, in.Project)
			if err != nil {
				return nil, err
			}
			cur, err := s.api.GetAppEnvWithResponse(ctx, id, in.App)
			if err != nil {
				return nil, err
			}
			if err := client.Check(cur.StatusCode(), cur.Body); err != nil {
				return nil, err
			}
			remove := map[string]bool{}
			for _, k := range in.Keys {
				remove[k] = true
			}
			kept := []api.EnvVar{}
			if cur.JSON200 != nil {
				for _, v := range *cur.JSON200 {
					if !remove[v.Name] {
						kept = append(kept, v)
					}
				}
			}
			resp, err := s.api.SetAppEnvWithResponse(ctx, id, in.App, kept)
			if err != nil {
				return nil, err
			}
			return ack("updated", "env", in.App, resp.StatusCode(), resp.Body)
		})

	addWrite(s.srv, "add_app_domain", "Add app domain",
		"Attach a custom domain to an app. The domain then needs a DNS record pointing at the region ingress; check it with the CLI or the console.",
		false, func(ctx context.Context, in addDomainArgs) (any, error) {
			id, err := s.projectID(ctx, in.Project)
			if err != nil {
				return nil, err
			}
			resp, err := s.api.AddDomainWithResponse(ctx, id, in.App, in.Domain)
			if err != nil {
				return nil, err
			}
			return ack("added", "domain", in.Domain, resp.StatusCode(), resp.Body)
		})

	addWrite(s.srv, "create_database", "Create database",
		"Create a MySQL, Postgres or Redis database in a project.",
		false, func(ctx context.Context, in createDatabaseArgs) (any, error) {
			id, err := s.projectID(ctx, in.Project)
			if err != nil {
				return nil, err
			}
			switch in.Kind {
			case "mysql":
				resp, err := s.api.CreateMysqlWithResponse(ctx, id, api.MySQL{Name: in.Name, Plan: in.Plan})
				if err != nil {
					return nil, err
				}
				return ack("created", "mysql", in.Name, resp.StatusCode(), resp.Body)
			case "postgres":
				resp, err := s.api.CreatePostgresWithResponse(ctx, id, api.Postgres{Name: in.Name, Plan: in.Plan})
				if err != nil {
					return nil, err
				}
				return ack("created", "postgres", in.Name, resp.StatusCode(), resp.Body)
			case "redis":
				resp, err := s.api.CreateRedisWithResponse(ctx, id, api.Redis{Name: in.Name, Plan: in.Plan})
				if err != nil {
					return nil, err
				}
				return ack("created", "redis", in.Name, resp.StatusCode(), resp.Body)
			default:
				return nil, fmt.Errorf("kind must be \"mysql\", \"postgres\" or \"redis\", got %q", in.Kind)
			}
		})

	addWrite(s.srv, "delete_database", "Delete database",
		"Delete a MySQL, Postgres or Redis database. The data is destroyed and cannot be recovered.",
		true, func(ctx context.Context, in deleteDatabaseArgs) (any, error) {
			id, err := s.projectID(ctx, in.Project)
			if err != nil {
				return nil, err
			}
			switch in.Kind {
			case "mysql":
				resp, err := s.api.DeleteMysqlWithResponse(ctx, id, in.Name)
				if err != nil {
					return nil, err
				}
				return ack("deleted", "mysql", in.Name, resp.StatusCode(), resp.Body)
			case "postgres":
				resp, err := s.api.DeletePostgresWithResponse(ctx, id, in.Name)
				if err != nil {
					return nil, err
				}
				return ack("deleted", "postgres", in.Name, resp.StatusCode(), resp.Body)
			case "redis":
				resp, err := s.api.DeleteRedisWithResponse(ctx, id, in.Name)
				if err != nil {
					return nil, err
				}
				return ack("deleted", "redis", in.Name, resp.StatusCode(), resp.Body)
			default:
				return nil, fmt.Errorf("kind must be \"mysql\", \"postgres\" or \"redis\", got %q", in.Kind)
			}
		})

	addWrite(s.srv, "create_volume", "Create volume",
		"Create a storage volume in a project, to mount into an app.",
		false, func(ctx context.Context, in createVolumeArgs) (any, error) {
			id, err := s.projectID(ctx, in.Project)
			if err != nil {
				return nil, err
			}
			resp, err := s.api.CreateVolumeWithResponse(ctx, id, api.Volume{Name: in.Name, Plan: in.Plan})
			if err != nil {
				return nil, err
			}
			return ack("created", "volume", in.Name, resp.StatusCode(), resp.Body)
		})

	addWrite(s.srv, "delete_volume", "Delete volume",
		"Delete a storage volume. The data is destroyed and cannot be recovered.",
		true, func(ctx context.Context, in deleteVolumeArgs) (any, error) {
			id, err := s.projectID(ctx, in.Project)
			if err != nil {
				return nil, err
			}
			resp, err := s.api.DeleteVolumeWithResponse(ctx, id, in.Name)
			if err != nil {
				return nil, err
			}
			return ack("deleted", "volume", in.Name, resp.StatusCode(), resp.Body)
		})
}

// buildApp assembles the create-app request body from the tool input.
func buildApp(in createAppArgs) (api.App, error) {
	switch in.Source.Type {
	case string(api.Git):
		if in.Source.Git == nil || in.Source.Git.URL == "" {
			return api.App{}, fmt.Errorf(`source.type is "git", so source.git.url is required`)
		}
	case string(api.Docker):
		if in.Source.Docker == nil || in.Source.Docker.Image == "" {
			return api.App{}, fmt.Errorf(`source.type is "docker", so source.docker.image is required`)
		}
	default:
		return api.App{}, fmt.Errorf(`source.type must be "git" or "docker", got %q`, in.Source.Type)
	}

	replicas := in.Replicas
	if replicas <= 0 {
		replicas = 1
	}
	app := api.App{
		Name:         in.Name,
		Plan:         in.Plan,
		Replicas:     replicas,
		Public:       in.Public,
		Domains:      append([]string{}, in.Domains...),
		VolumeMounts: []client.VolumeMount{},
	}
	if in.HTTPPort > 0 {
		port := in.HTTPPort
		app.HttpPort = &port
	}
	if len(in.Env) > 0 {
		vars := make([]api.EnvVar, 0, len(in.Env))
		for k, v := range in.Env {
			vars = append(vars, api.EnvVar{Name: k, Value: v})
		}
		app.EnvVars = &vars
	}
	mounts, err := client.ParseVolumeMounts(in.Volumes)
	if err != nil {
		return api.App{}, err
	}
	app.VolumeMounts = mounts

	b, err := json.Marshal(in.Source)
	if err != nil {
		return api.App{}, err
	}
	if err := json.Unmarshal(b, &app.DeploymentSource); err != nil {
		return api.App{}, err
	}
	return app, nil
}
