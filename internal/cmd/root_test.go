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
		{[]string{"env", "ls"}, "ls"},
		{[]string{"env", "ls", "-a", "web"}, "ls"},
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
