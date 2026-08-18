package cmd

import (
	"os"
	"path/filepath"
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
