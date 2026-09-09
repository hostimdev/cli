package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/hostimdev/cli/api"
)

// exec runs a command in an app's container by going through the project's
// bastion, which is the only path into a project's network. The bastion image
// carries a `shell` helper that asks its exec-broker sidecar to exec into the
// app's pod, so the CLI's job is to build the right ssh invocation and hand the
// terminal over.
//
// The system ssh binary does the work rather than an ssh library: it already
// knows the user's keys, agent, known_hosts and ~/.ssh/config, and it returns
// the remote command's exit status as its own.
func newExecCmd(c *cli) *cobra.Command {
	var identity string
	var stdin bool
	cmd := &cobra.Command{
		Use:     "exec <app> [-- <command> [args...]]",
		Aliases: []string{"shell", "ssh"},
		Short:   "Run a command or open a shell in an app's container",
		Long: "Run a command in an app's container, or open an interactive shell when no command is given.\n" +
			"The connection goes through the project's SSH bastion, so the project needs your public SSH key.\n\n" +
			"Examples:\n" +
			"  hostim exec chatwoot                      # interactive shell\n" +
			"  hostim exec chatwoot -- rails c           # one-off command\n" +
			"  hostim exec freshrss -- cat /tmp/FreshRSS.log\n" +
			"  hostim exec freshrss -- sh -c 'ls -la /var/www | head'\n" +
			"  cat dump.sql | hostim exec --stdin postgres -- psql app   # pipe data in",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			app, remote := args[0], args[1:]
			// cobra strips the first --, so anything left is the command.
			if len(remote) > 0 && remote[0] == "--" {
				remote = remote[1:]
			}

			sshBin, err := exec.LookPath("ssh")
			if err != nil {
				return fmt.Errorf("ssh not found in PATH; hostim exec needs the OpenSSH client")
			}

			cl, project, err := c.clientAndProject(cmd.Context())
			if err != nil {
				return err
			}
			p, err := getProject(cmd.Context(), cl, project)
			if err != nil {
				return err
			}
			if p.Region == nil || *p.Region == "" {
				return fmt.Errorf("project %s has no region", project)
			}
			if err := ensureSSHKey(cmd, cl, p, identity); err != nil {
				return err
			}
			host, err := bastionHost(cmd.Context(), cl, *p.Region)
			if err != nil {
				return err
			}

			// A command reads stdin only when asked to, or when the user is at a
			// terminal and could type into it. Handing it a stdin that ends right
			// away costs output: the cluster stops the container's output as soon
			// as the exec's stdin stream closes.
			wantStdin := stdin || term.IsTerminal(int(os.Stdin.Fd()))
			sshArgs := buildSSHArgs(host, p.Id, identity, app, remote, wantStdin)
			run := exec.CommandContext(cmd.Context(), sshBin, sshArgs...)
			run.Stdin, run.Stdout, run.Stderr = os.Stdin, cmd.OutOrStdout(), cmd.ErrOrStderr()
			if err := run.Run(); err != nil {
				var ee *exec.ExitError
				if errors.As(err, &ee) {
					// ssh already reported whatever went wrong, and the status is
					// the remote command's own.
					return exitError{code: ee.ExitCode()}
				}
				return err
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&identity, "identity", "i", "", "SSH private key to use (passed to ssh -i)")
	cmd.Flags().BoolVar(&stdin, "stdin", false, "pipe local stdin into the command (implied when stdin is a terminal)")
	return cmd
}

// buildSSHArgs assembles the ssh invocation. The remote side always runs the
// bastion's `shell` helper; a command is quoted so the login shell passes it
// through unchanged.
func buildSSHArgs(host, projectID, identity, app string, remote []string, stdin bool) []string {
	args := []string{}
	if identity != "" {
		args = append(args, "-i", identity)
	}
	if h, port, ok := splitHostPort(host); ok {
		host = h
		args = append(args, "-p", port)
	}
	if len(remote) == 0 {
		// Force a pty: ssh does not allocate one when a command is given, and an
		// interactive shell needs it.
		args = append(args, "-tt")
	} else {
		args = append(args, "-T")
	}
	args = append(args, "-l", projectID, host)

	shellCmd := "shell "
	if stdin && len(remote) > 0 {
		// An interactive session always gets stdin, so the flag is only needed
		// for a command.
		shellCmd += "--stdin "
	}
	shellCmd += shellQuote(app)
	if len(remote) > 0 {
		shellCmd += " --"
		for _, a := range remote {
			shellCmd += " " + shellQuote(a)
		}
	}
	return append(args, shellCmd)
}

// splitHostPort splits "host:port" as configured for a region. A bare hostname
// keeps ssh's default port.
func splitHostPort(host string) (string, string, bool) {
	i := strings.Index(host, ":")
	// One colon and digits after it, so an IPv6 literal is left alone.
	if i <= 0 || i != strings.LastIndex(host, ":") || i == len(host)-1 {
		return host, "", false
	}
	port := host[i+1:]
	if strings.Trim(port, "0123456789") != "" {
		return host, "", false
	}
	return host[:i], port, true
}

// shellQuote wraps s so a POSIX shell hands it on as a single word.
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	safe := true
	for _, r := range s {
		if !strings.ContainsRune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-./:=@", r) {
			safe = false
			break
		}
	}
	if safe {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func getProject(ctx context.Context, cl *api.ClientWithResponses, id string) (*api.Project, error) {
	resp, err := cl.GetProjectWithResponse(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := checkResp(resp.StatusCode(), resp.Body); err != nil {
		return nil, err
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("empty response")
	}
	return resp.JSON200, nil
}

func bastionHost(ctx context.Context, cl *api.ClientWithResponses, region string) (string, error) {
	resp, err := cl.GetRegionWithResponse(ctx, region)
	if err != nil {
		return "", err
	}
	if err := checkResp(resp.StatusCode(), resp.Body); err != nil {
		return "", err
	}
	if resp.JSON200 == nil || str(resp.JSON200.BastionHost) == "" {
		return "", fmt.Errorf("region %s has no bastion host", region)
	}
	return str(resp.JSON200.BastionHost), nil
}

// ensureSSHKey makes sure the project authorizes the local public key. Without
// it ssh fails with a bare "Permission denied", and the CLI has no other way to
// add a key.
func ensureSSHKey(cmd *cobra.Command, cl *api.ClientWithResponses, p *api.Project, identity string) error {
	pub, path, err := localPubKey(identity)
	if err != nil {
		if len(p.SshKeys) > 0 {
			return nil // some key is authorized; let ssh sort out which
		}
		return fmt.Errorf("project %s has no SSH keys and no local public key was found (%v); add one in the project's Bastion tab", p.Id, err)
	}
	if keyAuthorized(pub, p.SshKeys) {
		return nil
	}
	prompt := fmt.Sprintf("Project %s does not authorize %s. Add it to the project's SSH keys?", p.Id, path)
	if err := confirmYesNo(cmd, prompt, false); err != nil {
		return err
	}
	name := str(p.Name)
	if name == "" {
		name = p.Id
	}
	keys := append(append([]string{}, p.SshKeys...), pub)
	resp, err := cl.UpdateProjectWithResponse(cmd.Context(), p.Id, &api.UpdateProjectParams{Name: name}, keys)
	if err != nil {
		return err
	}
	if err := checkResp(resp.StatusCode(), resp.Body); err != nil {
		return err
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "Added %s to project %s. The bastion may need a few seconds to pick it up.\n", path, p.Id)
	return nil
}

// localPubKey returns the public key to authorize: the one next to --identity if
// given, else the first of the usual defaults.
func localPubKey(identity string) (key, path string, err error) {
	var candidates []string
	if identity != "" {
		candidates = []string{identity + ".pub"}
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", "", err
		}
		for _, n := range []string{"id_ed25519.pub", "id_ecdsa.pub", "id_rsa.pub"} {
			candidates = append(candidates, filepath.Join(home, ".ssh", n))
		}
	}
	for _, p := range candidates {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if k := strings.TrimSpace(string(b)); k != "" {
			return k, p, nil
		}
	}
	return "", "", fmt.Errorf("looked in %s", strings.Join(candidates, ", "))
}

// keyAuthorized compares keys by type and material, ignoring the trailing
// comment, which differs per machine and is not part of the key.
func keyAuthorized(key string, authorized []string) bool {
	want := keyMaterial(key)
	if want == "" {
		return false
	}
	for _, a := range authorized {
		if keyMaterial(a) == want {
			return true
		}
	}
	return false
}

func keyMaterial(key string) string {
	f := strings.Fields(strings.TrimSpace(key))
	if len(f) < 2 {
		return ""
	}
	return f[0] + " " + f[1]
}
