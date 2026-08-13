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
