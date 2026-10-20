package cmd

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

func TestBuildSSHArgs(t *testing.T) {
	// Interactive: pty forced, no remote command beyond the shell helper.
	got := buildSSHArgs("ssh.eu-center.hostim.dev", "hpr-123", "", "web", nil, true)
	want := "-tt -l hpr-123 ssh.eu-center.hostim.dev shell web"
	if strings.Join(got, " ") != want {
		t.Errorf("interactive args = %q, want %q", got, want)
	}

	// A command is quoted so the remote login shell passes it through whole.
	got = buildSSHArgs("host", "hpr-123", "/k/id", "web", []string{"sh", "-c", "echo 'hi there'"}, false)
	if got[0] != "-i" || got[1] != "/k/id" || got[2] != "-T" {
		t.Errorf("identity/no-tty args wrong: %q", got)
	}
	remote := got[len(got)-1]
	if !strings.HasPrefix(remote, "shell web -- sh -c ") {
		t.Errorf("remote command = %q", remote)
	}
	if !strings.Contains(remote, `'echo '\''hi there'\'''`) {
		t.Errorf("command not quoted for the remote shell: %q", remote)
	}

	// With stdin asked for, the remote helper gets the flag; an interactive
	// session never needs it.
	got = buildSSHArgs("host", "hpr-123", "", "web", []string{"cat"}, true)
	if remote := got[len(got)-1]; remote != "shell --stdin web -- cat" {
		t.Errorf("stdin remote command = %q", remote)
	}
	got = buildSSHArgs("host", "hpr-123", "", "web", nil, true)
	if remote := got[len(got)-1]; remote != "shell web" {
		t.Errorf("interactive remote command = %q", remote)
	}

	// Region hosts may carry a port.
	got = buildSSHArgs("host:2222", "hpr-1", "", "web", nil, false)
	if strings.Join(got, " ") != "-p 2222 -tt -l hpr-1 host shell web" {
		t.Errorf("port args = %q", got)
	}
}

func TestShellQuote(t *testing.T) {
	cases := map[string]string{
		"ls":          "ls",
		"/tmp/a.log":  "/tmp/a.log",
		"":            "''",
		"a b":         "'a b'",
		"it's":        `'it'\''s'`,
		"$(rm -rf /)": `'$(rm -rf /)'`,
		"a;b|c&d`e`":  "'a;b|c&d`e`'",
		"back\\slash": `'back\slash'`,
	}
	for in, want := range cases {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSplitHostPort(t *testing.T) {
	if h, p, ok := splitHostPort("host:2222"); !ok || h != "host" || p != "2222" {
		t.Errorf("host:2222 -> %q %q %v", h, p, ok)
	}
	for _, in := range []string{"host", "host:", ":22", "fe80::1"} {
		if _, _, ok := splitHostPort(in); ok {
			t.Errorf("splitHostPort(%q) should not split", in)
		}
	}
}

func TestKeyAuthorized(t *testing.T) {
	mine := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI0000 pavel@laptop"
	// Same key, different comment, still authorized.
	if !keyAuthorized(mine, []string{"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI0000 pavel@desktop"}) {
		t.Error("comment difference should not matter")
	}
	if keyAuthorized(mine, []string{"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIzzzz other"}) {
		t.Error("different material must not match")
	}
	if keyAuthorized(mine, nil) {
		t.Error("empty key list must not match")
	}
	if keyAuthorized("garbage", []string{"garbage"}) {
		t.Error("a line with no key material must not match")
	}
}

func TestRunSSHRetriesDenial(t *testing.T) {
	// A fake ssh that is refused on the first call, the way sshpiper refuses
	// while the bastion starts, and lets the second one in.
	dir := t.TempDir()
	fake := dir + "/ssh"
	script := "#!/bin/sh\nif [ ! -e " + dir + "/tried ]; then touch " + dir + "/tried; echo 'app@host: Permission denied (publickey).' >&2; exit 255; fi\necho ran\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	sshRetryDelay = 10 * time.Millisecond

	var out, errOut bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	if err := runSSH(cmd, fake, nil); err != nil {
		t.Fatalf("runSSH: %v (stderr %q)", err, errOut.String())
	}
	if out.String() != "ran\n" {
		t.Fatalf("stdout = %q, want the second call's output", out.String())
	}

	// Any other failure is the remote command's own and is not retried.
	if err := os.WriteFile(fake, []byte("#!/bin/sh\necho ran\nexit 255\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := runSSH(cmd, fake, nil); err == nil || out.String() != "ran\n" {
		t.Fatalf("err = %v, stdout = %q; want one failed run", err, out.String())
	}
}
