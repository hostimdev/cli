package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/hostimdev/cli/internal/client"
	"github.com/hostimdev/cli/internal/output"
)

// A write command answers with one human sentence by default. Under -o json it
// answers with a single object instead, so a script (or an agent) learns what
// happened without parsing English. Read commands already do this via
// Printer.Render; these helpers are the write-side equivalent.

// jsonOut reports whether -o json is in effect.
func (c *cli) jsonOut() bool { return c.printer.Format == output.FormatJSON }

// res builds a result payload: a status/kind/name spine plus any extra
// key/value pairs the command wants to report.
func res(status, kind, name string, kv ...any) map[string]any {
	m := map[string]any{"status": status}
	if kind != "" {
		m["kind"] = kind
	}
	if name != "" {
		m["name"] = name
	}
	for i := 0; i+1 < len(kv); i += 2 {
		k, ok := kv[i].(string)
		if !ok {
			continue
		}
		m[k] = kv[i+1]
	}
	return m
}

// result reports a finished write, as JSON under -o json and as the human
// sentence otherwise.
func (c *cli) result(cmd *cobra.Command, payload map[string]any, human string, args ...any) error {
	if c.jsonOut() {
		p := c.printer
		p.Out = cmd.OutOrStdout()
		return p.JSON(payload)
	}
	// An empty sentence means the command already printed its own human output.
	if human != "" {
		fmt.Fprintf(cmd.OutOrStdout(), human+"\n", args...)
	}
	return nil
}

// missingError reports every requirement a command is short of at once, so a
// caller learns all of them in one run instead of one per failed retry. Under
// -o json they are listed as a "missing" array.
type missingError struct {
	Missing []string
	msg     string
}

func (e *missingError) Error() string { return e.msg }

// reportError writes a failed command to stderr: a JSON object under -o json,
// so a caller that reads stdout as JSON does not have to parse English to find
// out what went wrong, and the usual "error: ..." line otherwise.
func reportError(c *cli, w io.Writer, err error) {
	if !c.jsonOut() {
		fmt.Fprintln(w, "error: "+err.Error())
		return
	}
	payload := map[string]any{"status": "error", "error": err.Error()}
	var me *missingError
	if errors.As(err, &me) && len(me.Missing) > 0 {
		payload["missing"] = me.Missing
	}
	var ae *client.APIError
	if errors.As(err, &ae) {
		payload["httpStatus"] = ae.Status
		if ae.Code != "" {
			payload["code"] = ae.Code
		}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(payload)
}

// progress prints a running-commentary line, and nothing under -o json, where
// the result object must be the only thing on stdout.
func (c *cli) progress(cmd *cobra.Command, format string, args ...any) {
	if c.jsonOut() {
		return
	}
	fmt.Fprintf(cmd.OutOrStdout(), format, args...)
}
