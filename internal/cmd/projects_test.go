package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/hostimdev/cli/api"
)

// liveProjectFixture is shaped like the list/get endpoints answer for a live
// project: costs, built-in domain, registry credentials, a git token and real
// env var values all present.
func liveProjectFixture(t *testing.T) *api.Template {
	t.Helper()
	var tpl api.Template
	raw := []byte(`{
  "components": {
    "apps": [
      {
        "name": "web",
        "plan": "sa-1-1",
        "public": true,
        "replicas": 2,
        "httpPort": 3000,
        "builtInDomain": "web-abc123.hostim.app",
        "cost": 9.99,
        "cpu": 1,
        "ram": 1,
        "domains": ["web.example.com"],
        "deploymentSource": {
          "type": "docker",
          "docker": {
            "image": "nginx:1.25",
            "registry": "ghcr.io/me",
            "username": "me",
            "password": "s3cr3t-pass"
          },
          "git": {"url": ""}
        },
        "envVars": [
          {"name": "APP_SECRET", "value": "s3cr3t-1"},
          {"name": "DB_HOST", "value": "$(MAIN_POSTGRES_HOST)"},
          {"name": "DATABASE_URL", "value": "postgresql://$(UMAMI_DB_POSTGRES_USER):$(UMAMI_DB_POSTGRES_PASSWORD)@$(UMAMI_DB_POSTGRES_HOST):$(UMAMI_DB_POSTGRES_PORT)/$(UMAMI_DB_POSTGRES_DATABASE)"},
          {"name": "SESSION", "value": "GENERATE_ME_32"},
          {"name": "EMPTY", "value": ""}
        ],
        "volumeMounts": [{"name": "data", "mountPath": "/var/lib/web"}]
      },
      {
        "name": "api",
        "plan": "sa-1-1",
        "replicas": 1,
        "httpPort": 8080,
        "cost": 4.99,
        "deploymentSource": {
          "type": "git",
          "git": {
            "url": "https://github.com/me/app",
            "branch": "main",
            "dockerfilepath": "Dockerfile",
            "token": "ghp_secret"
          },
          "docker": {"image": "built-artifact:latest"}
        },
        "envVars": [
          {"name": "API_KEY", "value": "k-123"}
        ],
        "volumeMounts": []
      }
    ],
    "volumes": [
      {"name": "data", "plan": "vol-1", "cost": 1.1, "storageMB": 1024}
    ],
    "postgres": [
      {"name": "main", "plan": "sp-1", "cost": 5.5, "ram": 1, "storageMB": 4096, "extensions": ["citext"]}
    ],
    "mysql": [
      {"name": "cache-db", "plan": "sm-1", "cost": 3.3, "ram": 0.5}
    ],
    "redis": [
      {"name": "cache", "plan": "sr-1", "cost": 2.2, "storage": 1}
    ]
  }
}`)
	if err := json.Unmarshal(raw, &tpl); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	return &tpl
}

func TestStripForExport(t *testing.T) {
	tpl := liveProjectFixture(t)
	web := &tpl.Components.Apps[0]
	apiApp := &tpl.Components.Apps[1]

	redacted := stripForExport(tpl)
	if !redacted {
		t.Error("redacted = false, want true (fixture has a registry password and a git token)")
	}

	// Costs, built-in domain and redacted credentials are gone.
	if tpl.Components.Apps[0].Cost != nil || tpl.Components.Apps[1].Cost != nil {
		t.Error("app costs not stripped")
	}
	if tpl.Components.Volumes[0].Cost != nil || tpl.Components.Postgres[0].Cost != nil ||
		tpl.Components.Mysql[0].Cost != nil || tpl.Components.Redis[0].Cost != nil {
		t.Error("database/volume costs not stripped")
	}
	if tpl.Components.Apps[0].BuiltInDomain != nil {
		t.Error("builtInDomain not stripped")
	}
	if web.DeploymentSource.Docker == nil || web.DeploymentSource.Docker.Password != nil {
		t.Errorf("docker password not stripped: %+v", web.DeploymentSource.Docker)
	}
	// Registry username is NOT a secret: private registries need it on re-apply.
	if web.DeploymentSource.Docker == nil || web.DeploymentSource.Docker.Username == nil || *web.DeploymentSource.Docker.Username != "me" {
		t.Errorf("docker username must be kept: %+v", web.DeploymentSource.Docker)
	}
	if web.DeploymentSource.Docker.Registry == nil || *web.DeploymentSource.Docker.Registry != "ghcr.io/me" {
		t.Errorf("docker registry must be kept: %+v", web.DeploymentSource.Docker)
	}
	if web.DeploymentSource.Docker.Image != "nginx:1.25" {
		t.Errorf("docker image must be kept (tag is the pin): %q", web.DeploymentSource.Docker.Image)
	}
	if apiApp.DeploymentSource.Git == nil || apiApp.DeploymentSource.Git.Token != nil {
		t.Errorf("git token not stripped: %+v", apiApp.DeploymentSource.Git)
	}
	if apiApp.DeploymentSource.Git.Url != "https://github.com/me/app" || apiApp.DeploymentSource.Docker != nil {
		t.Errorf("git source must be kept, built docker artifact dropped: %+v", apiApp.DeploymentSource)
	}
	if web.DeploymentSource.Git != nil {
		t.Errorf("empty git block on a docker app must be dropped: %+v", web.DeploymentSource.Git)
	}

	// Desired state (names, plans, sources, ports) survives.
	if web.Name != "web" || web.Plan != "sa-1-1" || web.HttpPort == nil || *web.HttpPort != 3000 || web.Replicas != 2 || !web.Public {
		t.Errorf("app desired state lost: %+v", web)
	}
	if len(web.VolumeMounts) != 1 || *web.VolumeMounts[0].Name != "data" {
		t.Errorf("volumeMounts lost: %+v", web.VolumeMounts)
	}
	if tpl.Components.Postgres[0].Name != "main" || tpl.Components.Postgres[0].Plan != "sp-1" {
		t.Errorf("postgres desired state lost: %+v", tpl.Components.Postgres[0])
	}
	if tpl.Components.Volumes[0].Name != "data" || tpl.Components.Volumes[0].Plan != "vol-1" {
		t.Errorf("volume desired state lost: %+v", tpl.Components.Volumes[0])
	}

	// Env values are exported raw: literals, refs and empty values all come
	// through unchanged (this is why the command warns the file holds secrets).
	ev := *tpl.Components.Apps[0].EnvVars
	wantEnv := map[string]string{
		"APP_SECRET":   "s3cr3t-1",
		"DB_HOST":      "$(MAIN_POSTGRES_HOST)",
		"DATABASE_URL": "postgresql://$(UMAMI_DB_POSTGRES_USER):$(UMAMI_DB_POSTGRES_PASSWORD)@$(UMAMI_DB_POSTGRES_HOST):$(UMAMI_DB_POSTGRES_PORT)/$(UMAMI_DB_POSTGRES_DATABASE)",
		"SESSION":      "GENERATE_ME_32",
		"EMPTY":        "",
	}
	if len(ev) != len(wantEnv) {
		t.Fatalf("env vars lost: %v", ev)
	}
	for _, v := range ev {
		if wantEnv[v.Name] != v.Value {
			t.Errorf("env %s = %q, want %q (raw export)", v.Name, v.Value, wantEnv[v.Name])
		}
	}
	if got := (*tpl.Components.Apps[1].EnvVars)[0].Value; got != "k-123" {
		t.Errorf("API_KEY = %q, want k-123 (raw export)", got)
	}

	// No credentials present -> not reported as redacted.
	bare := liveProjectFixture(t)
	bare.Components.Apps[0].DeploymentSource.Docker.Password = nil
	bare.Components.Apps[1].DeploymentSource.Git.Token = nil
	if stripForExport(bare) {
		t.Error("redacted = true, want false (no password or token present)")
	}
}

func TestExportRoundTrip(t *testing.T) {
	tpl := liveProjectFixture(t)
	stripForExport(tpl)
	tpl.Id = "prod"
	tpl.Name = "prod"
	tpl.Description = "Exported from project prod"

	data, err := templateToYAML(tpl)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "prod.yml")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := loadTemplateFile(path, "")
	if err != nil {
		t.Fatalf("exported YAML did not load back: %v", err)
	}
	if got.Id != "prod" || got.Name != "prod" || got.Description != "Exported from project prod" {
		t.Errorf("template metadata lost: %+v", got)
	}

	apps := map[string]api.App{}
	for _, a := range got.Components.Apps {
		apps[a.Name] = a
	}
	if len(apps) != 2 {
		t.Fatalf("apps lost: %v", apps)
	}
	web := apps["web"]
	if web.Plan != "sa-1-1" || web.DeploymentSource.Type != api.Docker ||
		web.DeploymentSource.Docker == nil || web.DeploymentSource.Docker.Image != "nginx:1.25" {
		t.Errorf("web app lost plan/source: %+v", web)
	}
	if web.DeploymentSource.Docker.Password != nil {
		t.Errorf("web app docker password survived the round trip: %+v", web.DeploymentSource.Docker)
	}
	if web.DeploymentSource.Docker.Username == nil || *web.DeploymentSource.Docker.Username != "me" {
		t.Errorf("web app docker username lost in the round trip: %+v", web.DeploymentSource.Docker)
	}
	if web.DeploymentSource.Git != nil {
		t.Errorf("web app git block survived the round trip: %+v", web.DeploymentSource.Git)
	}
	apiApp := apps["api"]
	if apiApp.Plan != "sa-1-1" || apiApp.DeploymentSource.Type != api.Git ||
		apiApp.DeploymentSource.Git == nil || apiApp.DeploymentSource.Git.Url != "https://github.com/me/app" {
		t.Errorf("api app lost plan/source: %+v", apiApp)
	}
	if apiApp.DeploymentSource.Git.Token != nil {
		t.Errorf("api app git token survived the round trip: %+v", apiApp.DeploymentSource.Git)
	}

	checkDB := func(kind string, name, plan string) {
		t.Helper()
		switch kind {
		case "volume":
			for _, v := range got.Components.Volumes {
				if v.Name == name && v.Plan != plan {
					t.Errorf("%s %s plan = %q, want %q", kind, name, v.Plan, plan)
				}
			}
		case "postgres":
			for _, d := range got.Components.Postgres {
				if d.Name == name && d.Plan != plan {
					t.Errorf("%s %s plan = %q, want %q", kind, name, d.Plan, plan)
				}
			}
		case "mysql":
			for _, d := range got.Components.Mysql {
				if d.Name == name && d.Plan != plan {
					t.Errorf("%s %s plan = %q, want %q", kind, name, d.Plan, plan)
				}
			}
		case "redis":
			for _, d := range got.Components.Redis {
				if d.Name == name && d.Plan != plan {
					t.Errorf("%s %s plan = %q, want %q", kind, name, d.Plan, plan)
				}
			}
		}
	}
	if len(got.Components.Volumes) != 1 || len(got.Components.Postgres) != 1 ||
		len(got.Components.Mysql) != 1 || len(got.Components.Redis) != 1 {
		t.Fatalf("resource counts lost: %+v", got.Components)
	}
	checkDB("volume", "data", "vol-1")
	checkDB("postgres", "main", "sp-1")
	checkDB("mysql", "cache-db", "sm-1")
	checkDB("redis", "cache", "sr-1")

	// Env vars: names and raw values survive the round trip.
	if web.EnvVars == nil {
		t.Fatalf("web env vars lost: %+v", web)
	}
	ev := *web.EnvVars
	want := map[string]string{
		"APP_SECRET":   "s3cr3t-1",
		"DB_HOST":      "$(MAIN_POSTGRES_HOST)",
		"DATABASE_URL": "postgresql://$(UMAMI_DB_POSTGRES_USER):$(UMAMI_DB_POSTGRES_PASSWORD)@$(UMAMI_DB_POSTGRES_HOST):$(UMAMI_DB_POSTGRES_PORT)/$(UMAMI_DB_POSTGRES_DATABASE)",
		"SESSION":      "GENERATE_ME_32",
		"EMPTY":        "",
	}
	if len(ev) != len(want) {
		t.Fatalf("env vars lost: %v", ev)
	}
	for _, v := range ev {
		if want[v.Name] != v.Value {
			t.Errorf("env %s = %q, want %q", v.Name, v.Value, want[v.Name])
		}
	}
}
