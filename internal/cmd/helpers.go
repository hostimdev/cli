package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hostimdev/cli/api"
	"github.com/hostimdev/cli/internal/client"
)

// errAborted is returned when the user declines a confirmation prompt. The
// helper already printed a human-readable "Aborted." line, so root's Execute
// exits non-zero without prefixing it with "error:".
var errAborted = errors.New("aborted")

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

// isInteractive reports whether stdin is a terminal, so we can prompt the user.
func isInteractive(cmd *cobra.Command) bool {
	f, ok := cmd.InOrStdin().(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// confirmByName guards a destructive action by forcing the user to type the
// resource's name back. It is a no-op when skip is true (e.g. --yes). In a
// non-interactive session it refuses rather than deleting silently.
func confirmByName(cmd *cobra.Command, kind, name string, skip bool) error {
	if skip {
		return nil
	}
	if !isInteractive(cmd) {
		return fmt.Errorf("refusing to delete %s %q in a non-interactive session; re-run with --yes to confirm", kind, name)
	}
	fmt.Fprintf(cmd.OutOrStderr(),
		"This permanently deletes %s %q and everything it contains. This cannot be undone.\n"+
			"Type the %s name (%q) to confirm: ", kind, name, kind, name)
	line, _ := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	if strings.TrimSpace(line) != name {
		fmt.Fprintln(cmd.OutOrStderr(), "Name did not match; aborted.")
		return errAborted
	}
	return nil
}

// confirmYesNo asks a yes/no question, defaulting to no. It is a no-op when skip
// is true. In a non-interactive session it refuses unless skip is set.
func confirmYesNo(cmd *cobra.Command, prompt string, skip bool) error {
	if skip {
		return nil
	}
	if !isInteractive(cmd) {
		return fmt.Errorf("refusing to proceed in a non-interactive session; re-run with --yes to confirm")
	}
	fmt.Fprintf(cmd.OutOrStderr(), "%s [y/N]: ", prompt)
	line, _ := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return nil
	default:
		fmt.Fprintln(cmd.OutOrStderr(), "Aborted.")
		return errAborted
	}
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
