// Command hostim is the command-line interface to the Hostim cloud platform.
package main

import (
	_ "embed"

	"github.com/hostimdev/cli/internal/cmd"
)

// manual is the README, the single source of CLI documentation. `hostim agent`
// prints it, so the manual an agent reads is the one shipped with the binary.
//
//go:embed README.md
var manual string

func main() {
	cmd.Execute(manual)
}
