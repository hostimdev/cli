package cmd

import (
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"

	mcpserver "github.com/hostimdev/cli/internal/mcp"
)

func newMCPCmd(c *cli) *cobra.Command {
	var allowWrite bool
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Run a Model Context Protocol server on stdio",
		Long: "Run a Model Context Protocol server on stdin/stdout, so a coding agent\n" +
			"(Claude Desktop, Cursor, ...) can inspect and provision Hostim resources\n" +
			"as tools.\n\n" +
			"Read-only tools are always available. Tools that create, change or delete\n" +
			"resources are only registered with --allow-write.\n\n" +
			"Authentication uses the same token as every other command: --token,\n" +
			"HOSTIM_TOKEN, or the token saved by `hostim login`.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := c.Client()
			if err != nil {
				return err
			}
			server := mcpserver.New(mcpserver.Options{
				Client:     client,
				AllowWrite: allowWrite,
				Version:    version,
			})
			return server.Run(cmd.Context(), &sdk.StdioTransport{})
		},
	}
	cmd.Flags().BoolVar(&allowWrite, "allow-write", false, "also expose tools that create, change and delete resources")
	return cmd
}
