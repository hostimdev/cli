package client

import (
	"context"
	"fmt"
	"strings"

	"github.com/hostimdev/cli/api"
)

// ProjectIDPrefix is the prefix of the system-generated project identifiers
// used as the {projectName} path parameter by the API. Friendly project names
// are resolved to an ID before nested calls.
const ProjectIDPrefix = "hpr-"

// ResolveProjectID maps a project reference (friendly name or ID) to the ID the
// API expects in the {projectName} path parameter. An ID (hpr-…) is returned
// as-is; a name is looked up via GetProjects. Callers pass whatever the user
// typed, so both work everywhere a project is referenced.
func ResolveProjectID(ctx context.Context, cl *api.ClientWithResponses, ref string) (string, error) {
	if strings.HasPrefix(ref, ProjectIDPrefix) {
		return ref, nil
	}
	resp, err := cl.GetProjectsWithResponse(ctx)
	if err != nil {
		return "", err
	}
	if err := Check(resp.StatusCode(), resp.Body); err != nil {
		return "", err
	}
	var names []string
	if resp.JSON200 != nil {
		for _, p := range *resp.JSON200 {
			if p.Id == ref || (p.Name != nil && *p.Name == ref) {
				return p.Id, nil
			}
			if p.Name != nil {
				names = append(names, *p.Name)
			}
		}
	}
	return "", fmt.Errorf("project %q not found; available: %s", ref, strings.Join(names, ", "))
}
