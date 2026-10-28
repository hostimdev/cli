package cmd

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hostimdev/cli/api"
)

// --env must override --env-file, and both must merge onto the app's existing
// env rather than replace it.
func TestApplySourceEnv(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, ".env")
	if err := os.WriteFile(file, []byte("FROM_FILE=1\nOVERRIDDEN=file\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	app := api.App{EnvVars: &[]api.EnvVar{{Name: "EXISTING", Value: "keep"}}}
	f := &deployFlags{envFile: file, env: []string{"OVERRIDDEN=flag", "NEW=2"}}

	changed, err := applySource(&app, f)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected changed=true")
	}

	got := map[string]string{}
	for _, v := range *app.EnvVars {
		got[v.Name] = v.Value
	}
	want := map[string]string{
		"EXISTING":   "keep",
		"FROM_FILE":  "1",
		"OVERRIDDEN": "flag",
		"NEW":        "2",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	if len(got) != len(want) {
		t.Errorf("got %d vars, want %d", len(got), len(want))
	}
}

// The three states of --health-check-path/--command: unset leaves the field
// alone, set writes it, set-to-empty clears it.
func TestApplySourceHealthCheckAndCommand(t *testing.T) {
	hcp, cmdOverride := "/old", "old-cmd"

	app := api.App{HealthCheckPath: &hcp, CommandOverride: &cmdOverride}
	if changed, err := applySource(&app, &deployFlags{}); err != nil || changed {
		t.Fatalf("unset flags: changed=%v err=%v", changed, err)
	}
	if str(app.HealthCheckPath) != "/old" || str(app.CommandOverride) != "old-cmd" {
		t.Errorf("unset flags modified the app: %q %q", str(app.HealthCheckPath), str(app.CommandOverride))
	}

	f := &deployFlags{healthCheckPath: "/new", healthCheckPathSet: true}
	if changed, err := applySource(&app, f); err != nil || !changed {
		t.Fatalf("set flag: changed=%v err=%v", changed, err)
	}
	if str(app.HealthCheckPath) != "/new" {
		t.Errorf("healthCheckPath = %q, want /new", str(app.HealthCheckPath))
	}
	if str(app.CommandOverride) != "old-cmd" {
		t.Errorf("commandOverride = %q, want it untouched", str(app.CommandOverride))
	}

	f = &deployFlags{healthCheckPathSet: true, commandSet: true}
	if changed, err := applySource(&app, f); err != nil || !changed {
		t.Fatalf("empty flag: changed=%v err=%v", changed, err)
	}
	if str(app.HealthCheckPath) != "" || str(app.CommandOverride) != "" {
		t.Errorf("expected both cleared, got %q %q", str(app.HealthCheckPath), str(app.CommandOverride))
	}
}

// HOS-694: --branch without --git must patch the branch of a git app and keep
// its URL, and must fail on a docker app instead of being dropped.
func TestApplySourceBranchOnly(t *testing.T) {
	var app api.App
	if err := json.Unmarshal([]byte(`{"deploymentSource":{"type":"git","git":{"url":"https://example.com/r.git","branch":"old","dockerfilepath":"Dockerfile"}}}`), &app); err != nil {
		t.Fatal(err)
	}
	if changed, err := applySource(&app, &deployFlags{branch: "main"}); err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	src, err := loadSource(app.DeploymentSource)
	if err != nil {
		t.Fatal(err)
	}
	if src.Git.Url != "https://example.com/r.git" || str(src.Git.Branch) != "main" || str(src.Git.Dockerfilepath) != "Dockerfile" {
		t.Errorf("git source = %+v", *src.Git)
	}

	var dock api.App
	if err := json.Unmarshal([]byte(`{"deploymentSource":{"type":"docker","docker":{"image":"nginx"}}}`), &dock); err != nil {
		t.Fatal(err)
	}
	if _, err := applySource(&dock, &deployFlags{branch: "main"}); err == nil {
		t.Error("--branch on a docker app: want an error")
	}
}

// An upload source switches away from git/docker, keeps its dockerfilepath on
// a re-deploy, and is cleared when the app switches to another source.
func TestApplySourceUpload(t *testing.T) {
	var app api.App
	if err := json.Unmarshal([]byte(`{"deploymentSource":{"type":"git","git":{"url":"https://example.com/r.git","dockerfilepath":"Dockerfile"}}}`), &app); err != nil {
		t.Fatal(err)
	}
	f := &deployFlags{source: ".", uploadSha: "abc", dockerfile: "docker/Dockerfile"}
	if changed, err := applySource(&app, f); err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	src, err := loadSource(app.DeploymentSource)
	if err != nil {
		t.Fatal(err)
	}
	if src.Type != string(api.UploadSource) || src.Upload == nil || src.Upload.Sha256 != "abc" {
		t.Fatalf("upload source = %+v", src)
	}
	if src.Git != nil || src.Docker != nil {
		t.Errorf("other sources should be cleared: %+v", src)
	}
	if str(src.Upload.Dockerfilepath) != "docker/Dockerfile" {
		t.Errorf("dockerfilepath = %q", str(src.Upload.Dockerfilepath))
	}

	// A re-deploy without --dockerfile keeps the existing path.
	f = &deployFlags{source: ".", uploadSha: "def"}
	if _, err := applySource(&app, f); err != nil {
		t.Fatal(err)
	}
	src, _ = loadSource(app.DeploymentSource)
	if src.Upload.Sha256 != "def" || str(src.Upload.Dockerfilepath) != "docker/Dockerfile" {
		t.Errorf("re-deploy lost the dockerfilepath: %+v", src.Upload)
	}

	// Switching to git clears the upload block.
	if _, err := applySource(&app, &deployFlags{git: "https://example.com/r.git"}); err != nil {
		t.Fatal(err)
	}
	src, _ = loadSource(app.DeploymentSource)
	if src.Upload != nil || src.Git == nil {
		t.Errorf("git switch left upload behind: %+v", src)
	}
}

func TestPrepareUploadNeedsDockerfile(t *testing.T) {
	dir := t.TempDir()
	if _, err := prepareUpload(context.Background(), nil, "p", &deployFlags{source: dir}, deploymentSource{}, nil); err == nil {
		t.Fatal("expected an error for a directory without a Dockerfile")
	} else if !strings.Contains(err.Error(), "Dockerfile") || !strings.Contains(err.Error(), "--dockerfile") {
		t.Errorf("err = %v, want a Dockerfile hint", err)
	}
}

func TestValidateSourceFlags(t *testing.T) {
	if err := validateSourceFlags(&deployFlags{source: ".", git: "https://x"}); err == nil {
		t.Error("--source with --git should fail")
	}
	if err := validateSourceFlags(&deployFlags{source: ".", dockerImage: "nginx"}); err == nil {
		t.Error("--source with --docker-image should fail")
	}
	if err := validateSourceFlags(&deployFlags{source: "."}); err != nil {
		t.Errorf("--source alone: %v", err)
	}
}
