package cmd

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hostimdev/cli/api"
)

func TestAppSourceIncomplete(t *testing.T) {
	img := "nginx"
	ok := &api.App{}
	ok.DeploymentSource.Type = api.Docker
	ok.DeploymentSource.Docker = &struct {
		Image    string  `json:"image"`
		Password *string `json:"password,omitempty"`
		Registry *string `json:"registry,omitempty"`
		Username *string `json:"username,omitempty"`
	}{Image: img}
	if appSourceIncomplete(ok) {
		t.Error("docker app with image should be complete")
	}

	empty := &api.App{}
	empty.DeploymentSource.Type = api.Git // git with nil source
	if !appSourceIncomplete(empty) {
		t.Error("git app with no url should be incomplete")
	}

	none := &api.App{} // no type at all
	if !appSourceIncomplete(none) {
		t.Error("app with no type should be incomplete")
	}
}

func TestPromptAppSource(t *testing.T) {
	// docker path: type, image, decline private creds.
	app := &api.App{}
	r := bufio.NewReader(strings.NewReader("docker\nnginx:latest\nn\n"))
	if err := promptAppSource(io.Discard, r, app); err != nil {
		t.Fatal(err)
	}
	if appSourceIncomplete(app) || app.DeploymentSource.Type != api.Docker || app.DeploymentSource.Docker.Image != "nginx:latest" {
		t.Errorf("docker source not filled in: %+v", app.DeploymentSource)
	}

	// git path: type, url, default branch + dockerfile, decline token.
	gapp := &api.App{}
	r = bufio.NewReader(strings.NewReader("git\ngit@github.com:u/r.git\n\n\nn\n"))
	if err := promptAppSource(io.Discard, r, gapp); err != nil {
		t.Fatal(err)
	}
	if appSourceIncomplete(gapp) || gapp.DeploymentSource.Type != api.Git {
		t.Fatalf("git source not filled in: %+v", gapp.DeploymentSource)
	}
	g := gapp.DeploymentSource.Git
	if g.Url != "git@github.com:u/r.git" || g.Branch == nil || *g.Branch != "main" || g.Dockerfilepath == nil || *g.Dockerfilepath != "Dockerfile" {
		t.Errorf("git defaults wrong: %+v", g)
	}

	// EOF before required value aborts.
	bad := &api.App{}
	r = bufio.NewReader(strings.NewReader("docker\n"))
	if err := promptAppSource(io.Discard, r, bad); err == nil {
		t.Error("expected abort on EOF before required image")
	}
}

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
	_ = os.WriteFile(path, []byte(`
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
	_ = os.WriteFile(listPath, []byte("- name: a\n- name: b\n"), 0o600)
	if _, err := loadTemplateFile(listPath); err == nil || !strings.Contains(err.Error(), "list of templates") {
		t.Errorf("list file error = %v, want 'list of templates'", err)
	}

	// Missing file surfaces an error.
	if _, err := loadTemplateFile(filepath.Join(dir, "nope.yml")); err == nil {
		t.Error("expected error for missing file")
	}
}

func TestFindConflicts(t *testing.T) {
	tmpl := &api.Template{}
	tmpl.Components.Apps = []api.App{{Name: "web"}, {Name: "worker"}}
	tmpl.Components.Postgres = []api.Postgres{{Name: "db"}}
	tmpl.Components.Volumes = []api.Volume{{Name: "data"}}

	// Nothing exists yet -> no conflicts.
	empty := &existingResources{
		apps: map[string]bool{}, postgres: map[string]bool{}, mysql: map[string]bool{},
		redis: map[string]bool{}, volumes: map[string]bool{},
	}
	if got := findConflicts(tmpl, empty); len(got) != 0 {
		t.Errorf("expected no conflicts, got %v", got)
	}

	// Some overlap -> reported in creation order (volume, postgres, then apps).
	e := &existingResources{
		apps:     map[string]bool{"web": true},
		postgres: map[string]bool{"db": true},
		mysql:    map[string]bool{},
		redis:    map[string]bool{},
		volumes:  map[string]bool{"data": true},
	}
	got := findConflicts(tmpl, e)
	want := []string{"volume data", "postgres db", "app web"}
	if len(got) != len(want) {
		t.Fatalf("conflicts = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("conflict[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestTemplateToYAMLRoundTrip(t *testing.T) {
	orig := &api.Template{Id: "demo", Name: "Demo", Description: "d"}
	port := 3000
	orig.Components.Apps = []api.App{{
		Name:     "web",
		Plan:     "sa-1-1",
		HttpPort: &port,
		EnvVars:  &[]api.EnvVar{{Name: "APP_SECRET", Value: "GENERATE_ME_32"}},
	}}
	orig.Components.Volumes = []api.Volume{{Name: "data", Plan: "vol-1"}}

	data, err := templateToYAML(orig)
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "t.yml")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := loadTemplateFile(path)
	if err != nil {
		t.Fatalf("saved YAML did not round-trip: %v", err)
	}
	if got.Name != "Demo" || len(got.Components.Apps) != 1 || got.Components.Apps[0].HttpPort == nil || *got.Components.Apps[0].HttpPort != 3000 {
		t.Fatalf("round-trip lost data: %+v", got)
	}
	// The GENERATE_ME placeholder must survive so apply regenerates the secret.
	if (*got.Components.Apps[0].EnvVars)[0].Value != "GENERATE_ME_32" {
		t.Errorf("placeholder not preserved: %q", (*got.Components.Apps[0].EnvVars)[0].Value)
	}
}

func TestValidateNames(t *testing.T) {
	ok := &api.Template{}
	ok.Components.Apps = []api.App{{Name: "web-1"}}
	ok.Components.Volumes = []api.Volume{{Name: "data"}}
	if err := validateNames(ok); err != nil {
		t.Errorf("valid names rejected: %v", err)
	}

	bad := &api.Template{}
	bad.Components.Volumes = []api.Volume{{Name: "minio-isb-client-config"}} // 23 chars
	bad.Components.Apps = []api.App{{Name: "Bad_Name"}}                      // invalid chars
	err := validateNames(bad)
	if err == nil {
		t.Fatal("expected error for invalid names")
	}
	if !strings.Contains(err.Error(), "minio-isb-client-config") || !strings.Contains(err.Error(), "max 20") {
		t.Errorf("error missing length violation: %v", err)
	}
	if !strings.Contains(err.Error(), "Bad_Name") {
		t.Errorf("error missing char violation: %v", err)
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
