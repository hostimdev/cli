package cmd

import (
	"testing"

	"github.com/hostimdev/cli/api"
)

func f32(v float32) *float32 { return &v }
func sp(s string) *string    { return &s }

func TestSizeMB(t *testing.T) {
	cases := map[string]struct {
		in   *float32
		want string
	}{
		"nil":      {nil, "-"},
		"zero":     {f32(0), "-"},
		"exact gb": {f32(1024), "1GB"},
		"25 gb":    {f32(25 * 1024), "25GB"},
		"fraction": {f32(200), "0.2GB"},
	}
	for name, c := range cases {
		if got := sizeMB(c.in); got != c.want {
			t.Errorf("%s: sizeMB = %q, want %q", name, got, c.want)
		}
	}
}

func TestDBInfo(t *testing.T) {
	if got := dbInfo(sp("shared"), f32(200)); got != "shared, 0.2GB" {
		t.Errorf("shared+storage = %q", got)
	}
	if got := dbInfo(sp("dedicated"), nil); got != "dedicated" {
		t.Errorf("type only = %q", got)
	}
	if got := dbInfo(nil, f32(1024)); got != "1GB" {
		t.Errorf("storage only = %q", got)
	}
	if got := dbInfo(nil, nil); got != "-" {
		t.Errorf("neither = %q", got)
	}
}

func TestAppInfo(t *testing.T) {
	app := &api.App{Name: "web", Replicas: 3, Public: true}
	app.DeploymentSource.Type = api.Docker
	app.DeploymentSource.Docker = &struct {
		Image    string  `json:"image"`
		Password *string `json:"password,omitempty"`
		Registry *string `json:"registry,omitempty"`
		Username *string `json:"username,omitempty"`
	}{Image: "nginx"}
	got := appInfo(app)
	want := "docker nginx x3 · public"
	if got != want {
		t.Errorf("appInfo = %q, want %q", got, want)
	}

	priv := &api.App{Name: "worker", Replicas: 1, Public: false}
	priv.DeploymentSource.Type = api.Docker
	priv.DeploymentSource.Docker = &struct {
		Image    string  `json:"image"`
		Password *string `json:"password,omitempty"`
		Registry *string `json:"registry,omitempty"`
		Username *string `json:"username,omitempty"`
	}{Image: "busybox"}
	if got := appInfo(priv); got != "docker busybox" {
		t.Errorf("single private app = %q", got)
	}
}

func TestKindOrder(t *testing.T) {
	order := []string{"app", "postgres", "mysql", "redis", "volume"}
	for i := 1; i < len(order); i++ {
		if kindOrder(order[i-1]) >= kindOrder(order[i]) {
			t.Errorf("kind order wrong: %s !< %s", order[i-1], order[i])
		}
	}
	if kindOrder("unknown") <= kindOrder("volume") {
		t.Error("unknown kind should sort last")
	}
}
