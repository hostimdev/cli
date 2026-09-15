package mcp

import (
	"context"
	"fmt"

	"github.com/hostimdev/cli/api"
	"github.com/hostimdev/cli/internal/client"
)

// noArgs is the input type of a tool that takes no parameters.
type noArgs struct{}

type projectArgs struct {
	Project string `json:"project" jsonschema:"project name or hpr-... ID; list them with list_projects"`
}

type appArgs struct {
	Project string `json:"project" jsonschema:"project name or hpr-... ID; list them with list_projects"`
	App     string `json:"app" jsonschema:"app name; list them with list_apps"`
}

type appLogsArgs struct {
	Project string `json:"project" jsonschema:"project name or hpr-... ID; list them with list_projects"`
	App     string `json:"app" jsonschema:"app name; list them with list_apps"`
	LogType string `json:"log_type,omitempty" jsonschema:"which stream to read: stdout (default) or build"`
	Limit   int    `json:"limit,omitempty" jsonschema:"maximum number of log lines (default 100)"`
}

type appEventsArgs struct {
	Project string `json:"project" jsonschema:"project name or hpr-... ID; list them with list_projects"`
	App     string `json:"app" jsonschema:"app name; list them with list_apps"`
	Limit   int    `json:"limit,omitempty" jsonschema:"maximum number of events, most recent first (default 100)"`
}

type credentialsArgs struct {
	Project string `json:"project" jsonschema:"project name or hpr-... ID; list them with list_projects"`
	Kind    string `json:"kind" jsonschema:"database engine: mysql, postgres or redis"`
	Name    string `json:"name" jsonschema:"database name; list them with list_databases"`
}

type regionArgs struct {
	Region string `json:"region" jsonschema:"region name; list them with list_regions"`
}

func (s *Server) registerReadTools() {
	addRead(s.srv, "list_projects", "List projects",
		"List every Hostim project in the account, with its ID, region, deployed service count and projected monthly cost.",
		func(ctx context.Context, _ noArgs) (any, error) {
			resp, err := s.api.GetProjectsWithResponse(ctx)
			if err != nil {
				return nil, err
			}
			return ok(resp.JSON200, resp.StatusCode(), resp.Body)
		})

	addRead(s.srv, "list_apps", "List apps",
		"List the apps in a project.",
		func(ctx context.Context, in projectArgs) (any, error) {
			id, err := s.projectID(ctx, in.Project)
			if err != nil {
				return nil, err
			}
			resp, err := s.api.GetAppsWithResponse(ctx, id)
			if err != nil {
				return nil, err
			}
			return ok(resp.JSON200, resp.StatusCode(), resp.Body)
		})

	addRead(s.srv, "get_app", "Get app",
		"Show one app's configuration: plan, replicas, port, domains, env vars, volumes and deployment source.",
		func(ctx context.Context, in appArgs) (any, error) {
			id, err := s.projectID(ctx, in.Project)
			if err != nil {
				return nil, err
			}
			resp, err := s.api.GetAppWithResponse(ctx, id, in.App)
			if err != nil {
				return nil, err
			}
			return ok(resp.JSON200, resp.StatusCode(), resp.Body)
		})

	addRead(s.srv, "get_app_status", "Get app status",
		"Show an app's build status, runtime status and internal DNS name. Poll this after creating or rebuilding an app.",
		func(ctx context.Context, in appArgs) (any, error) {
			id, err := s.projectID(ctx, in.Project)
			if err != nil {
				return nil, err
			}
			resp, err := s.api.GetAppStatusWithResponse(ctx, id, in.App)
			if err != nil {
				return nil, err
			}
			return ok(resp.JSON200, resp.StatusCode(), resp.Body)
		})

	addRead(s.srv, "get_app_logs", "Get app logs",
		"Read recent logs for an app: the stdout stream for runtime output, or the build stream to debug a failed build.",
		func(ctx context.Context, in appLogsArgs) (any, error) {
			logType := api.GetAppLogsParamsLogType(in.LogType)
			if in.LogType == "" {
				logType = api.GetAppLogsParamsLogTypeStdout
			}
			if !logType.Valid() {
				return nil, fmt.Errorf("log_type must be \"stdout\" or \"build\", got %q", in.LogType)
			}
			limit := in.Limit
			if limit <= 0 {
				limit = 100
			}
			id, err := s.projectID(ctx, in.Project)
			if err != nil {
				return nil, err
			}
			resp, err := s.api.GetAppLogsWithResponse(ctx, id, in.App, &api.GetAppLogsParams{
				LogType: logType,
				Limit:   limit,
			})
			if err != nil {
				return nil, err
			}
			return ok(resp.JSON200, resp.StatusCode(), resp.Body)
		})

	addRead(s.srv, "get_app_events", "Get app events",
		"Read Kubernetes events for an app, most recent first. Use this to find out why an app is not starting.",
		func(ctx context.Context, in appEventsArgs) (any, error) {
			id, err := s.projectID(ctx, in.Project)
			if err != nil {
				return nil, err
			}
			var params *api.GetAppEventsParams
			if in.Limit > 0 {
				params = &api.GetAppEventsParams{Limit: &in.Limit}
			}
			resp, err := s.api.GetAppEventsWithResponse(ctx, id, in.App, params)
			if err != nil {
				return nil, err
			}
			return ok(resp.JSON200, resp.StatusCode(), resp.Body)
		})

	addRead(s.srv, "list_databases", "List databases",
		"List the MySQL, Postgres and Redis databases in a project.",
		func(ctx context.Context, in projectArgs) (any, error) {
			id, err := s.projectID(ctx, in.Project)
			if err != nil {
				return nil, err
			}
			mysql, err := s.api.GetMysqlsWithResponse(ctx, id)
			if err != nil {
				return nil, err
			}
			if err := client.Check(mysql.StatusCode(), mysql.Body); err != nil {
				return nil, err
			}
			postgres, err := s.api.GetPostgresWithResponse(ctx, id)
			if err != nil {
				return nil, err
			}
			if err := client.Check(postgres.StatusCode(), postgres.Body); err != nil {
				return nil, err
			}
			redis, err := s.api.GetRedisesWithResponse(ctx, id)
			if err != nil {
				return nil, err
			}
			if err := client.Check(redis.StatusCode(), redis.Body); err != nil {
				return nil, err
			}
			return map[string]any{
				"mysql":    deref(mysql.JSON200),
				"postgres": deref(postgres.JSON200),
				"redis":    deref(redis.JSON200),
			}, nil
		})

	addRead(s.srv, "get_database_credentials", "Get database credentials",
		"Show the connection credentials for a database: host, port, user, password and database name.",
		func(ctx context.Context, in credentialsArgs) (any, error) {
			id, err := s.projectID(ctx, in.Project)
			if err != nil {
				return nil, err
			}
			switch in.Kind {
			case "mysql":
				resp, err := s.api.GetMysqlCredentialsWithResponse(ctx, id, in.Name)
				if err != nil {
					return nil, err
				}
				return ok(resp.JSON200, resp.StatusCode(), resp.Body)
			case "postgres":
				resp, err := s.api.GetPostgresCredentialsWithResponse(ctx, id, in.Name)
				if err != nil {
					return nil, err
				}
				return ok(resp.JSON200, resp.StatusCode(), resp.Body)
			case "redis":
				resp, err := s.api.GetRedisCredentialsWithResponse(ctx, id, in.Name)
				if err != nil {
					return nil, err
				}
				return ok(resp.JSON200, resp.StatusCode(), resp.Body)
			default:
				return nil, fmt.Errorf("kind must be \"mysql\", \"postgres\" or \"redis\", got %q", in.Kind)
			}
		})

	addRead(s.srv, "list_volumes", "List volumes",
		"List the storage volumes in a project.",
		func(ctx context.Context, in projectArgs) (any, error) {
			id, err := s.projectID(ctx, in.Project)
			if err != nil {
				return nil, err
			}
			resp, err := s.api.GetVolumesWithResponse(ctx, id)
			if err != nil {
				return nil, err
			}
			return ok(resp.JSON200, resp.StatusCode(), resp.Body)
		})

	addRead(s.srv, "list_regions", "List regions",
		"List the region names apps and databases can be deployed to.",
		func(ctx context.Context, _ noArgs) (any, error) {
			resp, err := s.api.GetRegionsWithResponse(ctx)
			if err != nil {
				return nil, err
			}
			return ok(resp.JSON200, resp.StatusCode(), resp.Body)
		})

	addRead(s.srv, "list_templates", "List templates",
		"List the curated deployment templates (app + databases + volumes) that can be applied to a project.",
		func(ctx context.Context, _ noArgs) (any, error) {
			resp, err := s.api.GetTemplatesWithResponse(ctx)
			if err != nil {
				return nil, err
			}
			return ok(resp.JSON200, resp.StatusCode(), resp.Body)
		})

	addRead(s.srv, "list_region_plans", "List region plans",
		"List the valid plan names and prices in a region for apps, volumes, mysql, postgres and redis. Use a plan name from here when creating a resource.",
		func(ctx context.Context, in regionArgs) (any, error) {
			apps, err := s.api.GetRegionAppPricingWithResponse(ctx, in.Region)
			if err != nil {
				return nil, err
			}
			if err := client.Check(apps.StatusCode(), apps.Body); err != nil {
				return nil, err
			}
			volumes, err := s.api.GetRegionVolumePricingWithResponse(ctx, in.Region)
			if err != nil {
				return nil, err
			}
			if err := client.Check(volumes.StatusCode(), volumes.Body); err != nil {
				return nil, err
			}
			mysql, err := s.api.GetRegionMySQLPricingWithResponse(ctx, in.Region)
			if err != nil {
				return nil, err
			}
			if err := client.Check(mysql.StatusCode(), mysql.Body); err != nil {
				return nil, err
			}
			postgres, err := s.api.GetRegionPostgresPricingWithResponse(ctx, in.Region)
			if err != nil {
				return nil, err
			}
			if err := client.Check(postgres.StatusCode(), postgres.Body); err != nil {
				return nil, err
			}
			redis, err := s.api.GetRegionRedisPricingWithResponse(ctx, in.Region)
			if err != nil {
				return nil, err
			}
			if err := client.Check(redis.StatusCode(), redis.Body); err != nil {
				return nil, err
			}
			return map[string]any{
				"apps":     deref(apps.JSON200),
				"volumes":  deref(volumes.JSON200),
				"mysql":    deref(mysql.JSON200),
				"postgres": deref(postgres.JSON200),
				"redis":    deref(redis.JSON200),
			}, nil
		})
}
