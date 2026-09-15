package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hostimdev/cli/api"
)

// testAPI serves the few Hostim endpoints the tools touch, and records the body
// of the last create-app request so the test can check what was sent.
type testAPI struct {
	*httptest.Server
	lastAppBody map[string]any
	lastEnvBody string
}

func (ta *testAPI) register() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/projects", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `[{"id":"hpr-1","name":"demo","region":"eu-1","deployedServices":1,"projectedMonthlyCosts":5.5}]`)
	})
	mux.HandleFunc("/api/projects/hpr-1/apps", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &ta.lastAppBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{}`)
	})
	mux.HandleFunc("/api/projects/hpr-1/apps/web/env", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			_, _ = io.WriteString(w, `[{"name":"ONLY","value":"1"}]`)
			return
		}
		body, _ := io.ReadAll(r.Body)
		ta.lastEnvBody = string(body)
		_, _ = io.WriteString(w, `{"message":"ok"}`)
	})
	return mux
}

// connect builds a server backed by a test API and returns a connected MCP
// client session plus that API, so the test can inspect what was sent.
func connect(t *testing.T, allowWrite bool) (*sdk.ClientSession, *testAPI) {
	t.Helper()
	ta := &testAPI{}
	ta.Server = httptest.NewServer(ta.register())
	t.Cleanup(ta.Close)

	cl, err := api.NewClientWithResponses(ta.URL, api.WithRequestEditorFn(
		func(_ context.Context, req *http.Request) error {
			req.Header.Set("Authorization", "Bearer test-token")
			return nil
		}))
	if err != nil {
		t.Fatal(err)
	}

	s := New(Options{Client: cl, AllowWrite: allowWrite, Version: "test"})
	serverTransport, clientTransport := sdk.NewInMemoryTransports()

	ctx := t.Context()
	if _, err := s.srv.Connect(ctx, serverTransport, nil); err != nil {
		t.Fatal(err)
	}
	client := sdk.NewClient(&sdk.Implementation{Name: "test-client", Version: "test"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { session.Close() })
	return session, ta
}

func toolNames(t *testing.T, session *sdk.ClientSession) map[string]bool {
	t.Helper()
	res, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tool := range res.Tools {
		names[tool.Name] = true
	}
	return names
}

func TestWriteToolsAreGated(t *testing.T) {
	readOnly, _ := connect(t, false)
	names := toolNames(t, readOnly)
	if !names["list_projects"] {
		t.Error("read-only server is missing list_projects")
	}
	if names["create_app"] {
		t.Error("write tool create_app is exposed without allow-write")
	}

	writable, _ := connect(t, true)
	names = toolNames(t, writable)
	if !names["create_app"] || !names["delete_app"] {
		t.Error("write tools are missing with allow-write")
	}
}

func TestListProjectsReturnsPayload(t *testing.T) {
	session, _ := connect(t, false)
	res, err := session.CallTool(t.Context(), &sdk.CallToolParams{Name: "list_projects", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("list_projects failed: %v", res.Content)
	}
	b, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "hpr-1") {
		t.Errorf("payload does not contain the project: %s", b)
	}
}

func TestCreateAppBuildsSourceAndValidates(t *testing.T) {
	session, ta := connect(t, true)

	res, err := session.CallTool(t.Context(), &sdk.CallToolParams{
		Name: "create_app",
		Arguments: map[string]any{
			"project": "demo",
			"name":    "web",
			"plan":    "sa-1-1",
			"source": map[string]any{
				"type": "git",
				"git":  map[string]any{"url": "https://example.com/web.git", "branch": "main"},
			},
			"http_port": 3000,
			"env":       map[string]any{"NODE_ENV": "production"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("create_app failed: %v", res.Content)
	}

	body := ta.lastAppBody
	if body["name"] != "web" || body["plan"] != "sa-1-1" {
		t.Errorf("create_app body = %v", body)
	}
	src, _ := body["deploymentSource"].(map[string]any)
	if src["type"] != "git" {
		t.Errorf("deploymentSource.type = %v, want git", src["type"])
	}
	git, _ := src["git"].(map[string]any)
	if git["url"] != "https://example.com/web.git" {
		t.Errorf("git.url = %v", git["url"])
	}
	if _, ok := body["volumeMounts"].([]any); !ok {
		t.Errorf("volumeMounts should be an empty array, got %T (%v)", body["volumeMounts"], body["volumeMounts"])
	}

	// A bad source type must fail before any request is sent.
	res, err = session.CallTool(t.Context(), &sdk.CallToolParams{
		Name:      "create_app",
		Arguments: map[string]any{"project": "demo", "name": "bad", "plan": "sa-1-1", "source": map[string]any{"type": "ftp"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Error("create_app with an unknown source type should return a tool error")
	}
}

// Removing the last variable must send [], not null: the env request body is a
// required, non-nullable array and the API rejects null.
func TestUnsetLastEnvVarSendsEmptyArray(t *testing.T) {
	session, ta := connect(t, true)

	res, err := session.CallTool(t.Context(), &sdk.CallToolParams{
		Name:      "unset_app_env",
		Arguments: map[string]any{"project": "demo", "app": "web", "keys": []any{"ONLY"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unset_app_env failed: %v", res.Content)
	}
	if strings.TrimSpace(ta.lastEnvBody) != "[]" {
		t.Errorf("env body = %s, want []", ta.lastEnvBody)
	}
}
