package cmd

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// readmePath is the manual embedded into the binary by main.go.
const readmePath = "../../README.md"

// TestManualDocumentsEveryCommand keeps the single source honest: a command
// added to the tree without a line in the README would leave the README, the
// docs page and `hostim agent` all describing a CLI that no longer exists.
func TestManualDocumentsEveryCommand(t *testing.T) {
	manual, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(manual)

	var missing []string
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			path := sub.CommandPath()
			if sub.Name() == "help" || sub.Hidden {
				continue
			}
			// Group commands ("hostim db postgres") are documented through
			// their leaves, so only require the leaf paths.
			if !sub.HasSubCommands() && !strings.Contains(text, path) {
				missing = append(missing, path)
			}
			walk(sub)
		}
	}
	walk(newRootCmd(&cli{}, ""))

	if len(missing) > 0 {
		t.Errorf("commands missing from %s: %s", readmePath, strings.Join(missing, ", "))
	}
}

func TestAgentPrintsManual(t *testing.T) {
	var out bytes.Buffer
	cmd := newAgentCmd("# hostim CLI\nmanual body")
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "manual body") {
		t.Errorf("agent printed %q, want the manual", out.String())
	}
}
