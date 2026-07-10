package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/hostimdev/cli/api"
	"github.com/hostimdev/cli/internal/client"
)

// projectIDPrefix is the prefix of the system-generated project identifiers used
// as the {projectName} path parameter by the API. Friendly project names are
// resolved to an ID before nested calls.
const projectIDPrefix = "hpr-"

// checkResp maps a generated response's status + body into an error (nil for 2xx).
func checkResp(status int, body []byte) error {
	return client.Check(status, body)
}

// clientAndProject builds an authenticated client and resolves the target
// project (by name or ID) to its API ID, for the many project-scoped commands.
func (c *cli) clientAndProject(ctx context.Context) (*api.ClientWithResponses, string, error) {
	ref, err := c.Project()
	if err != nil {
		return nil, "", err
	}
	cl, err := c.Client()
	if err != nil {
		return nil, "", err
	}
	id, err := resolveProjectID(ctx, cl, ref)
	if err != nil {
		return nil, "", err
	}
	return cl, id, nil
}

// resolveProjectID maps a project reference (friendly name or ID) to the ID the
// API expects in the {projectName} path parameter. An ID (hpr-…) is returned
// as-is; a name is looked up via GetProjects. Callers pass whatever the user
// typed, so both work everywhere a project is referenced.
func resolveProjectID(ctx context.Context, cl *api.ClientWithResponses, ref string) (string, error) {
	if strings.HasPrefix(ref, projectIDPrefix) {
		return ref, nil
	}
	resp, err := cl.GetProjectsWithResponse(ctx)
	if err != nil {
		return "", err
	}
	if err := checkResp(resp.StatusCode(), resp.Body); err != nil {
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

// str dereferences a *string, returning "" for nil.
func str(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// dash returns s, or "-" when empty, for table cells.
func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
