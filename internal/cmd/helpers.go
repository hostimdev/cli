package cmd

import (
	"github.com/hostimdev/cli/api"
	"github.com/hostimdev/cli/internal/client"
)

// checkResp maps a generated response's status + body into an error (nil for 2xx).
func checkResp(status int, body []byte) error {
	return client.Check(status, body)
}

// clientAndProject builds an authenticated client and resolves the target
// project in one step, for the many project-scoped resource commands.
func (c *cli) clientAndProject() (*api.ClientWithResponses, string, error) {
	project, err := c.Project()
	if err != nil {
		return nil, "", err
	}
	api, err := c.Client()
	if err != nil {
		return nil, "", err
	}
	return api, project, nil
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
