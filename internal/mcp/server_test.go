package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hostimdev/cli/api"
)

// testAPI serves the few Hostim endpoints the tools touch, and records the body
// of the last create-app request so the test can check what was sent.
type testAPI struct {
	*httptest.Server
	lastAppBody    map[string]any
	lastEnvBody    string
	lastBackupBody map[string]any
	lastUploadBody api.UploadRequest
	lastRebuild    bool
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
	mux.HandleFunc("/api/projects/hpr-1/apps/web", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			_, _ = io.WriteString(w, `{"name":"web","plan":"sa-1-1","domains":[],"volumeMounts":[],"deploymentSource":{"type":"upload","upload":{"sha256":"old","dockerfilepath":"Dockerfile"}}}`)
			return
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &ta.lastAppBody)
		_, _ = io.WriteString(w, `{}`)
	})
	mux.HandleFunc("/api/projects/hpr-1/apps/web/rebuild", func(w http.ResponseWriter, _ *http.Request) {
		ta.lastRebuild = true
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{}`)
	})
	mux.HandleFunc("/api/projects/hpr-1/uploads", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&ta.lastUploadBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"sha256":"`+ta.lastUploadBody.Sha256+`","exists":true}`)
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
	mux.HandleFunc("/api/projects/hpr-1/backups", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &ta.lastBackupBody)
			w.WriteHeader(http.StatusAccepted)
			_, _ = io.WriteString(w, `{"name":"run-1","trigger":"manual","phase":"Pending","items":[]}`)
			return
		}
		_, _ = io.WriteString(w, `{"enabled":true,"ready":true,"schedule":"0 3 * * *","resources":[{"kind":"postgres","name":"db1"}],"retention":{"keepLast":3},"runs":[]}`)
	})
	mux.HandleFunc("/api/projects/hpr-1/backups/list", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"items":[{"id":"bk-1","time":"2026-01-01T00:00:00Z","trigger":"schedule","sizeBytes":1024}]}`)
	})
	mux.HandleFunc("/api/projects/hpr-1/backups/bk-1/download", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"bk-1","phase":"Succeeded","fileName":"backup.tar","sizeBytes":1024,"url":"https://example.com/dl","expiresAt":"2026-01-02T00:00:00Z"}`)
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

func TestCreateAppUploadsLocalDirectory(t *testing.T) {
	session, ta := connect(t, true)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := session.CallTool(t.Context(), &sdk.CallToolParams{
		Name: "create_app",
		Arguments: map[string]any{
			"project": "demo",
			"name":    "web",
			"plan":    "sa-1-1",
			"source": map[string]any{
				"type":   "upload",
				"upload": map[string]any{"path": dir, "dockerfilepath": "Dockerfile"},
			},
			"http_port": 3000,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("create_app upload failed: %v", res.Content)
	}
	if ta.lastUploadBody.Sha256 == "" {
		t.Fatal("create_app did not upload the directory")
	}
	src, _ := ta.lastAppBody["deploymentSource"].(map[string]any)
	up, _ := src["upload"].(map[string]any)
	if src["type"] != "upload" || up["sha256"] != ta.lastUploadBody.Sha256 {
		t.Errorf("deploymentSource = %v", src)
	}
	if _, ok := up["path"]; ok {
		t.Errorf("path must not be sent: %v", up)
	}
}

func TestDeployAppSourceUploadsAndRebuilds(t *testing.T) {
	session, ta := connect(t, true)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := session.CallTool(t.Context(), &sdk.CallToolParams{
		Name:      "deploy_app_source",
		Arguments: map[string]any{"project": "demo", "app": "web", "path": dir},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("deploy_app_source failed: %v", res.Content)
	}
	if ta.lastUploadBody.Sha256 == "" {
		t.Fatal("deploy_app_source did not upload the directory")
	}
	src, _ := ta.lastAppBody["deploymentSource"].(map[string]any)
	up, _ := src["upload"].(map[string]any)
	if up["sha256"] != ta.lastUploadBody.Sha256 {
		t.Errorf("upload.sha256 = %v, want %s", up["sha256"], ta.lastUploadBody.Sha256)
	}
	if _, ok := up["path"]; ok {
		t.Errorf("path must not be sent: %v", up)
	}
	if !ta.lastRebuild {
		t.Error("deploy_app_source did not rebuild the app")
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

// list_backups answers the overview with no kind/name, and the resource's backup
// list when both are given.
func TestListBackupsSwitchesBetweenOverviewAndList(t *testing.T) {
	session, _ := connect(t, false)

	res, err := session.CallTool(t.Context(), &sdk.CallToolParams{
		Name:      "list_backups",
		Arguments: map[string]any{"project": "demo"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("list_backups overview failed: %v", res.Content)
	}
	b, _ := json.Marshal(res.StructuredContent)
	if !strings.Contains(string(b), `"schedule"`) || !strings.Contains(string(b), "postgres") {
		t.Errorf("overview payload = %s", b)
	}

	res, err = session.CallTool(t.Context(), &sdk.CallToolParams{
		Name:      "list_backups",
		Arguments: map[string]any{"project": "demo", "kind": "postgres", "name": "db1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("list_backups list failed: %v", res.Content)
	}
	b, _ = json.Marshal(res.StructuredContent)
	if !strings.Contains(string(b), "bk-1") {
		t.Errorf("list payload = %s", b)
	}
}

func TestDownloadBackupReturnsState(t *testing.T) {
	session, _ := connect(t, true)

	res, err := session.CallTool(t.Context(), &sdk.CallToolParams{
		Name:      "download_backup",
		Arguments: map[string]any{"project": "demo", "backup_id": "bk-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("download_backup failed: %v", res.Content)
	}
	b, _ := json.Marshal(res.StructuredContent)
	if !strings.Contains(string(b), "Succeeded") || !strings.Contains(string(b), "backup.tar") {
		t.Errorf("download payload = %s", b)
	}
}

func TestCreateBackupStartsRun(t *testing.T) {
	session, ta := connect(t, true)

	res, err := session.CallTool(t.Context(), &sdk.CallToolParams{
		Name:      "create_backup",
		Arguments: map[string]any{"project": "demo"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("create_backup failed: %v", res.Content)
	}
	b, _ := json.Marshal(res.StructuredContent)
	if !strings.Contains(string(b), "run-1") {
		t.Errorf("create payload = %s", b)
	}
	if len(ta.lastBackupBody) != 0 {
		t.Errorf("whole-project backup body = %v, want empty", ta.lastBackupBody)
	}

	res, err = session.CallTool(t.Context(), &sdk.CallToolParams{
		Name:      "create_backup",
		Arguments: map[string]any{"project": "demo", "kind": "postgres", "name": "db1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("create_backup resource failed: %v", res.Content)
	}
	if ta.lastBackupBody["kind"] != "postgres" || ta.lastBackupBody["name"] != "db1" {
		t.Errorf("resource backup body = %v", ta.lastBackupBody)
	}
}
