package client

import (
	"errors"
	"fmt"
	"strings"

	"github.com/hostimdev/cli/api"
)

// ErrEmptyResponse covers a 2xx answer with no payload: nothing the caller did
// causes it and no command can continue past it.
var ErrEmptyResponse = errors.New("the API returned an empty response; retry, and report it if it keeps happening")

// VolumeMount matches the anonymous struct type of App.VolumeMounts, so it can
// be assigned directly.
type VolumeMount = struct {
	MountPath *string `json:"mountPath,omitempty"`
	Name      *string `json:"name,omitempty"`
}

// NormalizeApp replaces nil required-array fields with empty slices so they
// serialize as [] rather than null, which the API schema rejects.
func NormalizeApp(app *api.App) {
	if app.Domains == nil {
		app.Domains = []string{}
	}
	if app.VolumeMounts == nil {
		app.VolumeMounts = []VolumeMount{}
	}
}

// ParseVolumeMounts turns "name:/mount/path" values into app volume mounts.
func ParseVolumeMounts(specs []string) ([]VolumeMount, error) {
	out := make([]VolumeMount, 0, len(specs))
	for _, s := range specs {
		name, path, found := strings.Cut(s, ":")
		if !found || name == "" || path == "" {
			return nil, fmt.Errorf("invalid volume %q (want name:/mount/path)", s)
		}
		n, p := name, path
		out = append(out, VolumeMount{MountPath: &p, Name: &n})
	}
	return out, nil
}

// MergeVars overlays updates onto base, replacing by name and appending new
// keys. The result is never nil, since the API rejects a null env array.
func MergeVars(base, updates []api.EnvVar) []api.EnvVar {
	idx := map[string]int{}
	out := make([]api.EnvVar, len(base), len(base)+len(updates))
	copy(out, base)
	for i, v := range out {
		idx[v.Name] = i
	}
	for _, u := range updates {
		if i, ok := idx[u.Name]; ok {
			out[i].Value = u.Value
		} else {
			idx[u.Name] = len(out)
			out = append(out, u)
		}
	}
	return out
}
