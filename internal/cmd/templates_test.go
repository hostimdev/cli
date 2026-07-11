package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hostimdev/cli/api"
)

func TestSecureRandom(t *testing.T) {
	s, err := secureRandom(24)
	if err != nil {
		t.Fatal(err)
	}
	if len(s) != 24 {
		t.Errorf("len = %d, want 24", len(s))
	}
	for _, r := range s {
		if !strings.ContainsRune(randAlphabet, r) {
			t.Errorf("char %q not in alphabet", r)
		}
	}
	if z, _ := secureRandom(0); z != "" {
		t.Errorf("secureRandom(0) = %q, want empty", z)
	}
}

func TestExpandGenerateMe(t *testing.T) {
	app := api.App{EnvVars: &[]api.EnvVar{
		{Name: "SECRET", Value: "GENERATE_ME_16"},
		{Name: "PLAIN", Value: "keepme"},
		{Name: "REF", Value: "$(DB_POSTGRES_HOST)"},
		{Name: "NOTAMATCH", Value: "GENERATE_ME_"},
	}}
	if err := expandGenerateMe(&app); err != nil {
		t.Fatal(err)
	}
	ev := *app.EnvVars
	if ev[0].Value == "GENERATE_ME_16" || len(ev[0].Value) != 16 {
		t.Errorf("SECRET not expanded to 16 chars: %q", ev[0].Value)
	}
	if ev[1].Value != "keepme" {
		t.Errorf("PLAIN mutated: %q", ev[1].Value)
	}
	if ev[2].Value != "$(DB_POSTGRES_HOST)" {
		t.Errorf("REF mutated: %q", ev[2].Value)
	}
	if ev[3].Value != "GENERATE_ME_" {
		t.Errorf("partial match mutated: %q", ev[3].Value)
	}
}

func TestCheckPlans(t *testing.T) {
	ok := &api.Template{}
	ok.Components.Apps = []api.App{{Name: "a", Plan: "sa-1-1"}}
	ok.Components.Postgres = []api.Postgres{{Name: "db", Plan: "sp-1"}}
	if err := checkPlans(ok); err != nil {
		t.Errorf("valid template rejected: %v", err)
	}

	bad := &api.Template{}
	bad.Components.Apps = []api.App{{Name: "a", Plan: ""}}
	bad.Components.Redis = []api.Redis{{Name: "r", Plan: ""}}
	err := checkPlans(bad)
	if err == nil {
		t.Fatal("expected error for missing plans")
	}
	if !strings.Contains(err.Error(), "app a") || !strings.Contains(err.Error(), "redis r") {
		t.Errorf("error missing resource names: %v", err)
	}
}

func TestLoadTemplateFile(t *testing.T) {
	dir := t.TempDir()

	// A single template with camelCase keys must decode via json tags.
	path := filepath.Join(dir, "tpl.yml")
	os.WriteFile(path, []byte(`
id: demo
name: Demo
description: test
components:
  apps:
    - name: web
      httpPort: 3000
      plan: sa-1-1
      public: true
      deploymentSource:
        type: docker
        docker:
          image: nginx
      volumeMounts: []
      envVars:
        - name: FOO
          value: bar
  volumes: []
  postgres: []
  mysql: []
  redis: []
`), 0o600)

	tmpl, err := loadTemplateFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if tmpl.Name != "Demo" || len(tmpl.Components.Apps) != 1 {
		t.Fatalf("bad parse: %+v", tmpl)
	}
	app := tmpl.Components.Apps[0]
	if app.HttpPort == nil || *app.HttpPort != 3000 {
		t.Errorf("httpPort not decoded: %+v", app.HttpPort)
	}
	if app.DeploymentSource.Type != api.Docker || app.DeploymentSource.Docker == nil {
		t.Errorf("deploymentSource not decoded: %+v", app.DeploymentSource)
	}

	// A list of templates is a common mistake and must be rejected clearly.
	listPath := filepath.Join(dir, "list.yml")
	os.WriteFile(listPath, []byte("- name: a\n- name: b\n"), 0o600)
	if _, err := loadTemplateFile(listPath); err == nil || !strings.Contains(err.Error(), "list of templates") {
		t.Errorf("list file error = %v, want 'list of templates'", err)
	}

	// Missing file surfaces an error.
	if _, err := loadTemplateFile(filepath.Join(dir, "nope.yml")); err == nil {
		t.Error("expected error for missing file")
	}
}

func TestSummarize(t *testing.T) {
	tmpl := &api.Template{}
	tmpl.Components.Apps = []api.App{{Name: "a"}}
	tmpl.Components.Postgres = []api.Postgres{{Name: "p"}}
	tmpl.Components.Volumes = []api.Volume{{Name: "v"}}
	got := summarize(tmpl)
	if !strings.Contains(got, "1 app") || !strings.Contains(got, "1 postgres") || !strings.Contains(got, "1 volume") {
		t.Errorf("summarize = %q", got)
	}
	if summarize(&api.Template{}) != "-" {
		t.Errorf("empty summarize should be '-'")
	}
}
