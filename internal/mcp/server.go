// Package mcp implements a Model Context Protocol server that exposes the
// Hostim public REST API as tools, so a coding agent can list, inspect and —
// only when explicitly allowed — provision Hostim resources.
//
// Read tools are always registered. Write tools are registered only when the
// caller opts in (hostim mcp --allow-write), so a server started without the
// flag physically cannot mutate anything.
package mcp

import (
	"context"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hostimdev/cli/api"
	"github.com/hostimdev/cli/internal/client"
)

// Options configures the server.
type Options struct {
	// Client is the authenticated Hostim API client. Required.
	Client *api.ClientWithResponses
	// AllowWrite registers the create/update/delete tools. Off by default.
	AllowWrite bool
	// Version is reported to the MCP client as the server version.
	Version string
}

// Server is a Hostim MCP server.
type Server struct {
	api        *api.ClientWithResponses
	srv        *sdk.Server
	allowWrite bool
}

// New builds the server and registers its tools.
func New(o Options) *Server {
	s := &Server{api: o.Client, allowWrite: o.AllowWrite}
	s.srv = sdk.NewServer(&sdk.Implementation{
		Name:    "hostim",
		Title:   "Hostim",
		Version: o.Version,
	}, &sdk.ServerOptions{Instructions: instructions(o.AllowWrite)})
	s.registerReadTools()
	if o.AllowWrite {
		s.registerWriteTools()
	}
	return s
}

// Run serves MCP on t until ctx is cancelled. It returns when the client
// disconnects, which for stdio means the parent process closed the pipe.
func (s *Server) Run(ctx context.Context, t sdk.Transport) error {
	return s.srv.Run(ctx, t)
}

const instructionsBase = `Hostim is a cloud hosting platform for containerised apps on Kubernetes.

Use list_projects to see the projects this token can access, then list_apps,
get_app_status, get_app_logs and get_app_events to inspect what is running.
Project-scoped tools take a "project" argument that accepts the project name or
its hpr-... ID; list_projects returns both.

Creating an app needs a plan name; list_region_plans returns the valid ones for
a region, and list_regions lists the regions. An app is deployed from a git URL
(which the Hostim API clones itself) or a published docker image.

`

const instructionsReadOnly = `This server is READ-ONLY: no tool can create, change or delete anything. To let an agent provision resources, restart it with --allow-write.`

const instructionsWritable = `This server exposes write tools. Creating or changing a resource spends the account's money, so confirm with the user before calling a tool that creates, updates or deletes anything, and never delete a resource without an explicit instruction.`

func instructions(allowWrite bool) string {
	if allowWrite {
		return instructionsBase + instructionsWritable
	}
	return instructionsBase + instructionsReadOnly
}

// add registers a tool. The typed handler returns a payload that the SDK
// serialises into the tool result.
func add[In any](srv *sdk.Server, name, title, desc string, ann *sdk.ToolAnnotations, h func(context.Context, In) (any, error)) {
	sdk.AddTool(srv, &sdk.Tool{Name: name, Title: title, Description: desc, Annotations: ann},
		func(ctx context.Context, _ *sdk.CallToolRequest, in In) (*sdk.CallToolResult, any, error) {
			out, err := h(ctx, in)
			if err != nil {
				return nil, nil, err
			}
			return nil, out, nil
		})
}

// addRead registers a read-only tool.
func addRead[In any](srv *sdk.Server, name, title, desc string, h func(context.Context, In) (any, error)) {
	add(srv, name, title, desc, &sdk.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}, h)
}

// addWrite registers a mutating tool. destructive marks deletes and anything
// else that cannot be undone.
func addWrite[In any](srv *sdk.Server, name, title, desc string, destructive bool, h func(context.Context, In) (any, error)) {
	add(srv, name, title, desc, &sdk.ToolAnnotations{DestructiveHint: &destructive}, h)
}

// ok checks a generated response's status and returns its typed payload. The
// caller must have checked the transport error first, since a failed call
// returns a nil response.
func ok[T any](payload *T, status int, body []byte) (any, error) {
	if err := client.Check(status, body); err != nil {
		return nil, err
	}
	if payload == nil {
		return nil, client.ErrEmptyResponse
	}
	return *payload, nil
}

// ack reports a successful create/update/delete whose response carries no body.
func ack(action, kind, name string, status int, body []byte) (any, error) {
	if err := client.Check(status, body); err != nil {
		return nil, err
	}
	return map[string]any{"status": action, "kind": kind, "name": name}, nil
}

// projectID resolves a project name or hpr-... ID to the ID the API expects.
func (s *Server) projectID(ctx context.Context, ref string) (string, error) {
	return client.ResolveProjectID(ctx, s.api, ref)
}

// deref unwraps an optional generated response payload for aggregation; a nil
// payload is reported as null rather than dropped.
func deref[T any](p *T) any {
	if p == nil {
		return nil
	}
	return *p
}
