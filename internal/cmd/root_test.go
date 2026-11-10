package cmd

import (
	"io"
	"strings"
	"testing"
)

func TestUnknownSubcommandFails(t *testing.T) {
	for _, tc := range []struct {
		args []string
		name string
	}{
		{[]string{"env", "bogus"}, "bogus"},
		{[]string{"env", "bogus", "-a", "web"}, "bogus"},
		{[]string{"apps", "bogus"}, "bogus"},
		{[]string{"db", "postgres", "foo"}, "foo"},
	} {
		root := newRootCmd(&cli{}, "")
		root.SetArgs(tc.args)
		root.SetOut(io.Discard)
		err := root.Execute()
		if err == nil || !strings.Contains(err.Error(), `unknown command "`+tc.name+`"`) {
			t.Errorf("%v: got %v, want an unknown command error", tc.args, err)
		}
	}
}

func TestLeafKeepsFlagError(t *testing.T) {
	root := newRootCmd(&cli{}, "")
	root.SetArgs([]string{"env", "set", "FOO=bar", "-z"})
	root.SetOut(io.Discard)
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "unknown shorthand flag") {
		t.Errorf("got %v, want the flag error", err)
	}
}

func TestEnvListAliases(t *testing.T) {
	root := newRootCmd(&cli{}, "")
	for _, alias := range []string{"ls", "list"} {
		c, _, err := root.Find([]string{"env", alias})
		if err != nil || c.Name() != "get" {
			t.Errorf("env %s: got %v, %v; want env get", alias, c, err)
		}
	}
}

func TestConfirmYesNoNamesTheQuestion(t *testing.T) {
	c := newRootCmd(&cli{}, "")
	c.SetIn(strings.NewReader("y\n")) // not an *os.File, so not interactive
	err := confirmYesNo(c, "Add key X to project P?", false)
	if err == nil || !strings.Contains(err.Error(), "Add key X to project P?") || !strings.Contains(err.Error(), "--yes") {
		t.Errorf("got %v, want the question and the --yes hint", err)
	}
}
