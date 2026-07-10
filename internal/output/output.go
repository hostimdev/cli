// Package output renders command results either as aligned text tables (default)
// or as JSON (-o json) for scripting.
package output

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
)

// Format is the selected output format.
type Format string

const (
	FormatTable Format = "table"
	FormatJSON  Format = "json"
)

// Parse validates a --output value.
func Parse(s string) (Format, error) {
	switch Format(s) {
	case FormatTable, FormatJSON:
		return Format(s), nil
	default:
		return "", fmt.Errorf("invalid output format %q (want table or json)", s)
	}
}

// Printer writes results in the chosen format.
type Printer struct {
	Format Format
	Out    io.Writer
}

// JSON writes v as indented JSON. Used directly by read commands under -o json.
func (p Printer) JSON(v any) error {
	enc := json.NewEncoder(p.Out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// Table writes a header + rows as an aligned table.
func (p Printer) Table(headers []string, rows [][]string) error {
	tw := tabwriter.NewWriter(p.Out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, strings.Join(headers, "\t"))
	for _, row := range rows {
		fmt.Fprintln(tw, strings.Join(row, "\t"))
	}
	return tw.Flush()
}

// Render prints v as JSON when Format is JSON, otherwise calls table with the
// supplied header/row builder. This keeps every read command a one-liner.
func (p Printer) Render(v any, headers []string, rows [][]string) error {
	if p.Format == FormatJSON {
		return p.JSON(v)
	}
	if len(rows) == 0 {
		fmt.Fprintln(p.Out, "No resources found.")
		return nil
	}
	return p.Table(headers, rows)
}
