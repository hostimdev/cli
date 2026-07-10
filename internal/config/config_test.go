package config

import "testing"

func TestResolvePrecedence(t *testing.T) {
	f := &File{Token: "file-tok", APIURL: "https://file", CurrentProject: "file-proj"}

	t.Run("flags win", func(t *testing.T) {
		t.Setenv(EnvToken, "env-tok")
		t.Setenv(EnvAPIURL, "https://env")
		t.Setenv(EnvProject, "env-proj")
		r := Resolve(f, Overrides{Token: "flag-tok", APIURL: "https://flag", Project: "flag-proj"})
		if r.Token != "flag-tok" || r.TokenSource != "flag" {
			t.Errorf("token = %q (%s), want flag-tok (flag)", r.Token, r.TokenSource)
		}
		if r.APIURL != "https://flag" {
			t.Errorf("apiURL = %q, want https://flag", r.APIURL)
		}
		if r.Project != "flag-proj" || r.ProjectSource != "flag" {
			t.Errorf("project = %q (%s), want flag-proj (flag)", r.Project, r.ProjectSource)
		}
	})

	t.Run("env beats file", func(t *testing.T) {
		t.Setenv(EnvToken, "env-tok")
		t.Setenv(EnvAPIURL, "https://env")
		t.Setenv(EnvProject, "env-proj")
		r := Resolve(f, Overrides{})
		if r.Token != "env-tok" || r.TokenSource != "env" {
			t.Errorf("token = %q (%s), want env-tok (env)", r.Token, r.TokenSource)
		}
		if r.Project != "env-proj" || r.ProjectSource != "env" {
			t.Errorf("project = %q (%s), want env-proj (env)", r.Project, r.ProjectSource)
		}
	})

	t.Run("file when no flag or env", func(t *testing.T) {
		t.Setenv(EnvToken, "")
		t.Setenv(EnvAPIURL, "")
		t.Setenv(EnvProject, "")
		r := Resolve(f, Overrides{})
		if r.Token != "file-tok" || r.TokenSource != "config" {
			t.Errorf("token source = %q, want config", r.TokenSource)
		}
		if r.Project != "file-proj" || r.ProjectSource != "config" {
			t.Errorf("project source = %q, want config", r.ProjectSource)
		}
	})

	t.Run("default api url", func(t *testing.T) {
		t.Setenv(EnvAPIURL, "")
		r := Resolve(&File{}, Overrides{})
		if r.APIURL != DefaultAPIURL {
			t.Errorf("apiURL = %q, want default %q", r.APIURL, DefaultAPIURL)
		}
	})
}

func TestRequireProject(t *testing.T) {
	if _, err := (Resolved{}).RequireProject(); err != ErrNoProject {
		t.Errorf("empty project err = %v, want ErrNoProject", err)
	}
	p, err := Resolved{Project: "x"}.RequireProject()
	if err != nil || p != "x" {
		t.Errorf("RequireProject = %q, %v; want x, nil", p, err)
	}
}
