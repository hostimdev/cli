package client

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hostimdev/cli/api"
)

type archiveEntry struct {
	mode int64
	link string
	body string
}

func readArchive(t *testing.T, path string) map[string]archiveEntry {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	out := map[string]archiveEntry{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		out[h.Name] = archiveEntry{mode: h.Mode, link: h.Linkname, body: string(body)}
	}
	return out
}

func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestPackExclusions(t *testing.T) {
	tests := []struct {
		name    string
		files   map[string]string
		present []string
		absent  []string
	}{
		{
			name: "git and env files",
			files: map[string]string{
				".git/config":         "secret",
				"node_modules/x/i.js": "dep",
				".env":                "SECRET=1",
				".env.local":          "SECRET=2",
				".env.example":        "SECRET=",
				".env.sample":         "SECRET=",
				".env.template":       "SECRET=",
				"app/main.go":         "package main",
			},
			present: []string{"app/main.go", ".env.example", ".env.sample", ".env.template"},
			absent:  []string{".git/config", "node_modules/x/i.js", ".env", ".env.local"},
		},
		{
			name: "dockerignore keeps Dockerfile and supports re-include",
			files: map[string]string{
				".dockerignore": "drop.log\n!keep.log\nDockerfile\n.dockerignore\n",
				"Dockerfile":    "FROM scratch\n",
				"drop.log":      "drop",
				"keep.log":      "keep",
				"app.txt":       "app",
			},
			present: []string{"Dockerfile", ".dockerignore", "keep.log", "app.txt"},
			absent:  []string{"drop.log"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeTree(t, tc.files)
			path, _, _, err := Pack(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer os.Remove(path)
			got := readArchive(t, path)
			for _, name := range tc.present {
				if _, ok := got[name]; !ok {
					t.Errorf("%s missing from archive", name)
				}
			}
			for _, name := range tc.absent {
				if _, ok := got[name]; ok {
					t.Errorf("%s should not be in archive", name)
				}
			}
		})
	}
}

func TestPackSymlinksAndMode(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "run.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "inside.txt"), []byte("inside"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "nested.txt"), []byte("nested"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("inside.txt", filepath.Join(dir, "link.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "escape.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("sub", filepath.Join(dir, "dirlink")); err != nil {
		t.Fatal(err)
	}

	path, _, _, err := Pack(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)
	got := readArchive(t, path)

	if got["run.sh"].mode != 0o755 {
		t.Errorf("run.sh mode = %o, want 0755", got["run.sh"].mode)
	}
	if got["inside.txt"].mode != 0o644 {
		t.Errorf("inside.txt mode = %o, want 0644", got["inside.txt"].mode)
	}
	if e, ok := got["link.txt"]; !ok || e.link != "inside.txt" {
		t.Errorf("inside symlink not stored: %+v", e)
	}
	if _, ok := got["escape.txt"]; ok {
		t.Error("symlink leaving the directory should be skipped")
	}
	if _, ok := got["dirlink/nested.txt"]; ok {
		t.Error("symlinked directory should not be followed")
	}
}

func TestPackDeterministic(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"Dockerfile": "FROM scratch\n",
		"a.txt":      "a",
		"sub/b.txt":  "b",
	})
	path1, sha1, size1, err := Pack(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path1)

	if err := os.Chtimes(filepath.Join(dir, "a.txt"), testTime(), testTime()); err != nil {
		t.Fatal(err)
	}
	path2, sha2, size2, err := Pack(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path2)

	if sha1 != sha2 || size1 != size2 {
		t.Errorf("packing twice differs: %s/%d vs %s/%d", sha1, size1, sha2, size2)
	}
}

func testTime() (t time.Time) {
	return time.Unix(1234567890, 0)
}

func TestPackGitMode(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	dir := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	runGit("init")
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("ignored.log\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", ".gitignore", "tracked.txt")
	if err := os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("two"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "untracked.txt"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ignored.log"), []byte("no"), 0o644); err != nil {
		t.Fatal(err)
	}

	path, _, _, err := Pack(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)
	got := readArchive(t, path)
	if got["tracked.txt"].body != "two" {
		t.Errorf("dirty tracked file body = %q, want two", got["tracked.txt"].body)
	}
	if _, ok := got["untracked.txt"]; !ok {
		t.Error("untracked file should be included")
	}
	if _, ok := got["ignored.log"]; ok {
		t.Error("ignored file should be excluded")
	}
	if _, ok := got[".git/HEAD"]; ok {
		t.Error(".git should be excluded")
	}
}

func TestUploadPutsArchive(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"Dockerfile": "FROM scratch\n",
		"app.txt":    "hello",
	})

	var putCalls int
	var gotAuth string
	var gotLength int64
	var gotBody int64
	var createBody api.UploadRequest
	var baseURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/projects/proj/uploads":
			if err := json.NewDecoder(r.Body).Decode(&createBody); err != nil {
				t.Errorf("decode create upload: %v", err)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"sha256":"`+createBody.Sha256+`","exists":false,"uploadUrl":"`+baseURL+`/put"}`)
		case "/put":
			putCalls++
			gotAuth = r.Header.Get("Authorization")
			gotLength = r.ContentLength
			n, _ := io.Copy(io.Discard, r.Body)
			gotBody = n
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	baseURL = srv.URL

	c, err := api.NewClientWithResponses(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	sha, err := Upload(context.Background(), c, "proj", dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if sha == "" || sha != createBody.Sha256 {
		t.Errorf("sha = %q, request sha = %q", sha, createBody.Sha256)
	}
	if putCalls != 1 {
		t.Fatalf("PUT calls = %d, want 1", putCalls)
	}
	if gotAuth != "" {
		t.Errorf("PUT sent Authorization %q", gotAuth)
	}
	if gotLength != createBody.Size || gotBody != createBody.Size {
		t.Errorf("Content-Length = %d, body = %d, want %d", gotLength, gotBody, createBody.Size)
	}
}

func TestUploadSkipsPutWhenExists(t *testing.T) {
	dir := writeTree(t, map[string]string{"Dockerfile": "FROM scratch\n"})
	var putCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/put" {
			putCalls++
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"sha256":"abc","exists":true}`)
	}))
	defer srv.Close()

	c, err := api.NewClientWithResponses(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	sha, err := Upload(context.Background(), c, "proj", dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if sha == "" {
		t.Error("sha should not be empty")
	}
	if putCalls != 0 {
		t.Errorf("PUT calls = %d, want 0 when exists is true", putCalls)
	}
}

func TestUploadPutErrorIncludesBody(t *testing.T) {
	dir := writeTree(t, map[string]string{"Dockerfile": "FROM scratch\n"})
	var baseURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/put" {
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, "signature expired")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"sha256":"abc","exists":false,"uploadUrl":"`+baseURL+`/put"}`)
	}))
	defer srv.Close()
	baseURL = srv.URL

	c, err := api.NewClientWithResponses(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Upload(context.Background(), c, "proj", dir, nil); err == nil || !strings.Contains(err.Error(), "signature expired") {
		t.Errorf("err = %v, want the response body", err)
	}
}

func TestPackSizeCap(t *testing.T) {
	old := maxUploadSize
	maxUploadSize = 1024
	defer func() { maxUploadSize = old }()

	dir := t.TempDir()
	big := make([]byte, 8192)
	if _, err := rand.Read(big); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "big.bin"), big, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := Pack(dir); err == nil || !strings.Contains(err.Error(), "upload limit") || !strings.Contains(err.Error(), "big.bin") {
		t.Errorf("err = %v, want the size limit and the biggest file", err)
	}
}
