package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/hostimdev/cli/internal/output"
)

func jsonCLI(format output.Format) (*cli, *cobra.Command, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	c := &cli{printer: output.Printer{Format: format}}
	cmd := &cobra.Command{}
	cmd.SetOut(buf)
	return c, cmd, buf
}

// A write command answers with one object under -o json, and with the human
// sentence otherwise.
func TestResult(t *testing.T) {
	c, cmd, buf := jsonCLI(output.FormatJSON)
	if err := c.result(cmd, res("created", "app", "web", "plan", "small"), "Created app %q.", "web"); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("not JSON: %v (%s)", err, buf)
	}
	for k, want := range map[string]string{"status": "created", "kind": "app", "name": "web", "plan": "small"} {
		if got[k] != want {
			t.Errorf("%s = %v, want %q", k, got[k], want)
		}
	}

	c, cmd, buf = jsonCLI(output.FormatTable)
	if err := c.result(cmd, res("created", "app", "web"), "Created app %q.", "web"); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "Created app \"web\".\n" {
		t.Errorf("table output = %q", buf)
	}
}

// Progress commentary would sit in front of the result object, so -o json drops it.
func TestProgressSilentUnderJSON(t *testing.T) {
	c, cmd, buf := jsonCLI(output.FormatJSON)
	c.progress(cmd, "Rebuilding %q...\n", "web")
	if buf.Len() != 0 {
		t.Errorf("json progress = %q, want nothing", buf)
	}
	c, cmd, buf = jsonCLI(output.FormatTable)
	c.progress(cmd, "Rebuilding %q...\n", "web")
	if buf.String() != "Rebuilding \"web\"...\n" {
		t.Errorf("table progress = %q", buf)
	}
}

// A failure is machine-readable under -o json, including the flags it is short of.
func TestReportError(t *testing.T) {
	err := checkNewAppFlags("web", &deployFlags{})
	var me *missingError
	if !errors.As(err, &me) {
		t.Fatalf("want *missingError, got %T", err)
	}
	if len(me.Missing) != 2 {
		t.Errorf("missing = %v, want both --plan and a source", me.Missing)
	}
	if !strings.Contains(err.Error(), "templates apply") {
		t.Errorf("error should point at templates for multi-resource stacks: %v", err)
	}

	c, _, _ := jsonCLI(output.FormatJSON)
	buf := &bytes.Buffer{}
	reportError(c, buf, err)
	var got map[string]any
	if jerr := json.Unmarshal(buf.Bytes(), &got); jerr != nil {
		t.Fatalf("not JSON: %v (%s)", jerr, buf)
	}
	if got["status"] != "error" {
		t.Errorf("status = %v", got["status"])
	}
	if len(got["missing"].([]any)) != 2 {
		t.Errorf("missing = %v", got["missing"])
	}

	// Plain text keeps the prefix every existing command already prints.
	c, _, _ = jsonCLI(output.FormatTable)
	buf.Reset()
	reportError(c, buf, errors.New("boom"))
	if buf.String() != "error: boom\n" {
		t.Errorf("text error = %q", buf)
	}
}

// A missing plan alone must not report the source flags as missing too.
func TestCheckNewAppFlagsPartial(t *testing.T) {
	err := checkNewAppFlags("web", &deployFlags{git: "https://example.com/x.git"})
	var me *missingError
	if !errors.As(err, &me) {
		t.Fatalf("want *missingError, got %v", err)
	}
	if len(me.Missing) != 1 || me.Missing[0] != "--plan" {
		t.Errorf("missing = %v, want [--plan]", me.Missing)
	}
	if checkNewAppFlags("web", &deployFlags{plan: "small", dockerImage: "nginx"}) != nil {
		t.Error("a plan plus a docker image is complete")
	}
}
