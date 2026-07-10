// Package config loads and persists the Hostim CLI configuration and resolves
// the effective token, API URL and current project from flags, environment and
// the on-disk config file.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// DefaultAPIURL is the production Hostim API base URL.
const DefaultAPIURL = "https://api.hostim.dev"

// Env var names recognised for overriding on-disk config.
const (
	EnvToken   = "HOSTIM_TOKEN"
	EnvAPIURL  = "HOSTIM_API_URL"
	EnvProject = "HOSTIM_PROJECT"
)

// ErrNoProject is returned when a project-scoped command has no project set via
// flag, environment or config.
var ErrNoProject = errors.New("no project selected: run `hostim use <project>` or pass -p/--project")

// ErrNoToken is returned when no API token can be resolved.
var ErrNoToken = errors.New("not logged in: run `hostim login` or set HOSTIM_TOKEN")

// File is the persisted configuration.
type File struct {
	Token          string `yaml:"token,omitempty"`
	APIURL         string `yaml:"apiUrl,omitempty"`
	CurrentProject string `yaml:"currentProject,omitempty"`
}

// Path returns the config file path, honouring XDG_CONFIG_HOME.
func Path() (string, error) {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "hostim", "config.yml"), nil
}

// Load reads the config file. A missing file yields an empty File, not an error.
func Load() (*File, error) {
	p, err := Path()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return &File{}, nil
		}
		return nil, err
	}
	var f File
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", p, err)
	}
	return &f, nil
}

// Save writes the config file with 0600 permissions, creating parent dirs.
func Save(f *File) error {
	p, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	data, err := yaml.Marshal(f)
	if err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o600)
}

// Resolved is the effective configuration after applying precedence.
type Resolved struct {
	Token   string
	APIURL  string
	Project string
	// TokenSource / ProjectSource describe where each value came from, for
	// diagnostics (e.g. `hostim whoami`).
	TokenSource   string
	ProjectSource string
}

// Overrides holds command-line flag values (empty string means unset).
type Overrides struct {
	Token   string
	APIURL  string
	Project string
}

// Resolve applies precedence flag > env > file for each field. The API URL
// always falls back to DefaultAPIURL. Token and project may be empty; callers
// that require them should check and surface ErrNoToken / ErrNoProject.
func Resolve(f *File, o Overrides) Resolved {
	r := Resolved{}

	switch {
	case o.Token != "":
		r.Token, r.TokenSource = o.Token, "flag"
	case os.Getenv(EnvToken) != "":
		r.Token, r.TokenSource = os.Getenv(EnvToken), "env"
	case f.Token != "":
		r.Token, r.TokenSource = f.Token, "config"
	}

	switch {
	case o.APIURL != "":
		r.APIURL = o.APIURL
	case os.Getenv(EnvAPIURL) != "":
		r.APIURL = os.Getenv(EnvAPIURL)
	case f.APIURL != "":
		r.APIURL = f.APIURL
	default:
		r.APIURL = DefaultAPIURL
	}

	switch {
	case o.Project != "":
		r.Project, r.ProjectSource = o.Project, "flag"
	case os.Getenv(EnvProject) != "":
		r.Project, r.ProjectSource = os.Getenv(EnvProject), "env"
	case f.CurrentProject != "":
		r.Project, r.ProjectSource = f.CurrentProject, "config"
	}

	return r
}

// RequireProject returns the resolved project or ErrNoProject.
func (r Resolved) RequireProject() (string, error) {
	if r.Project == "" {
		return "", ErrNoProject
	}
	return r.Project, nil
}
