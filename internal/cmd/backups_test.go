package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hostimdev/cli/api"
)

func backupClient(t *testing.T, url string) *api.ClientWithResponses {
	t.Helper()
	cl, err := api.NewClientWithResponses(url)
	if err != nil {
		t.Fatal(err)
	}
	return cl
}

func mustOpen(t *testing.T, path string) *os.File {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

func writeFile(path string, b []byte) error { return os.WriteFile(path, b, 0o644) }

func itoa(n int) string { return strconv.Itoa(n) }

func writeJSON(t *testing.T, w http.ResponseWriter, status int, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if status != 0 {
		w.WriteHeader(status)
	}
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Errorf("encode: %v", err)
	}
}

func TestResolveBackupKind(t *testing.T) {
	overview := func(resources string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"enabled":true,"ready":true,"schedule":"0 3 * * *","resources":`+resources+`}`)
		}))
	}

	cases := []struct {
		name      string
		resources string
		kind      string
		want      api.ListBackupsParamsKind
		wantErr   string
	}{
		{"unique", `[{"kind":"postgres","name":"db1"}]`, "", api.ListBackupsParamsKindPostgres, ""},
		{"ambiguous", `[{"kind":"postgres","name":"db1"},{"kind":"mysql","name":"db1"}]`, "", "", "both a postgres and a mysql"},
		{"missing", `[{"kind":"postgres","name":"other"}]`, "", "", "No database or volume named db1."},
		{"explicit", `[{"kind":"postgres","name":"db1"}]`, "volume", api.ListBackupsParamsKindVolume, ""},
		{"badkind", `[]`, "redis", "", "--kind must be"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := overview(tc.resources)
			defer srv.Close()
			got, err := resolveBackupKind(context.Background(), backupClient(t, srv.URL), "hpr-1", "db1", tc.kind)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("kind = %q, want %q", got, tc.want)
			}
		})
	}

	// An explicit kind must not hit the API at all.
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		_, _ = io.WriteString(w, `{}`)
	}))
	defer srv.Close()
	if _, err := resolveBackupKind(context.Background(), backupClient(t, srv.URL), "hpr-1", "db1", "postgres"); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Errorf("explicit --kind made %d API calls, want 0", calls)
	}
}

// backupServer serves the create/poll/download endpoints of one backup.
type backupServer struct {
	*httptest.Server
	fileBody []byte

	mu          sync.Mutex
	creates     int
	polls       int
	urlGets     int
	rangeHeader string
	pollRunning int // number of polls answered Running before Succeeded
	phase       string
	createErr   string
	createCode  int
	ignoreRange bool
}

func newBackupServer(t *testing.T, body []byte) *backupServer {
	t.Helper()
	b := &backupServer{fileBody: body, phase: "Succeeded"}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/projects/hpr-1/backups/bk-1/download", func(w http.ResponseWriter, r *http.Request) {
		b.mu.Lock()
		defer b.mu.Unlock()
		if r.Method == http.MethodPost {
			b.creates++
			if b.createCode != 0 {
				writeJSON(t, w, b.createCode, map[string]string{"message": b.createErr})
				return
			}
			phase := b.phase
			if b.polls == 0 && b.phase == "Succeeded" {
				phase = "Pending"
			}
			size := int64(len(b.fileBody))
			dl := api.BackupDownload{Id: "bk-1", Phase: phase, FileName: "backup.tar", SizeBytes: &size}
			if phase == "Succeeded" {
				url := b.URL + "/dl/bk-1"
				dl.Url = &url
			}
			if phase == "Failed" {
				msg := "prepare failed"
				dl.Error = &msg
			}
			writeJSON(t, w, http.StatusCreated, dl)
			return
		}
		b.polls++
		phase := b.phase
		if b.polls <= b.pollRunning {
			phase = "Running"
		}
		size := int64(len(b.fileBody))
		dl := api.BackupDownload{Id: "bk-1", Phase: phase, FileName: "backup.tar", SizeBytes: &size}
		if phase == "Succeeded" {
			url := b.URL + "/dl/bk-1"
			dl.Url = &url
		}
		writeJSON(t, w, 0, dl)
	})
	mux.HandleFunc("/dl/bk-1", func(w http.ResponseWriter, r *http.Request) {
		b.mu.Lock()
		b.urlGets++
		b.rangeHeader = r.Header.Get("Range")
		ignore := b.ignoreRange
		b.mu.Unlock()
		if ignore {
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write(b.fileBody)
			return
		}
		http.ServeContent(w, r, "backup.tar", time.Time{}, bytes.NewReader(b.fileBody))
	})
	srv := httptest.NewServer(mux)
	b.Server = srv
	t.Cleanup(srv.Close)
	return b
}

func (b *backupServer) counts() (creates, polls, gets int, rng string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.creates, b.polls, b.urlGets, b.rangeHeader
}

func downloadFast(t *testing.T) {
	t.Helper()
	oldPoll, oldBackoff, oldProgress := backupPollInterval, downloadBackoff, downloadProgressInterval
	backupPollInterval = time.Millisecond
	downloadBackoff = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond, time.Millisecond, time.Millisecond}
	downloadProgressInterval = time.Millisecond
	t.Cleanup(func() {
		backupPollInterval, downloadBackoff, downloadProgressInterval = oldPoll, oldBackoff, oldProgress
	})
}

func TestDownloadPollsThenDownloads(t *testing.T) {
	downloadFast(t)
	body := bytes.Repeat([]byte("hostim-backup-data"), 100)
	srv := newBackupServer(t, body)
	srv.pollRunning = 2

	dir := t.TempDir()
	target := dir + "/backup.tar"
	var errW bytes.Buffer
	ctx := context.Background()

	dl, err := prepareDownload(ctx, backupClient(t, srv.URL), "hpr-1", "bk-1", &errW)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fetchToFile(ctx, dl, target, io.Discard, &errW, nil); err != nil {
		t.Fatal(err)
	}

	creates, polls, gets, _ := srv.counts()
	if creates != 1 || polls != 3 || gets != 1 {
		t.Errorf("creates=%d polls=%d gets=%d, want 1/3/1", creates, polls, gets)
	}
	got, _ := io.ReadAll(mustOpen(t, target))
	if !bytes.Equal(got, body) {
		t.Errorf("file holds %d bytes, want %d", len(got), len(body))
	}
}

func TestDownloadResumesWithRange(t *testing.T) {
	downloadFast(t)
	body := bytes.Repeat([]byte("hostim-backup-data"), 100)
	srv := newBackupServer(t, body)

	dir := t.TempDir()
	target := dir + "/backup.tar"
	half := len(body) / 2
	if err := writeFile(target, body[:half]); err != nil {
		t.Fatal(err)
	}

	size := int64(len(body))
	url := srv.URL + "/dl/bk-1"
	dl := &api.BackupDownload{Id: "bk-1", Phase: "Succeeded", FileName: "backup.tar", SizeBytes: &size, Url: &url}

	var errW bytes.Buffer
	if _, err := fetchToFile(context.Background(), dl, target, io.Discard, &errW, nil); err != nil {
		t.Fatal(err)
	}

	_, _, gets, rng := srv.counts()
	if gets != 1 {
		t.Errorf("url gets = %d, want 1", gets)
	}
	if rng != "bytes="+itoa(half)+"-" {
		t.Errorf("Range = %q, want bytes=%d-", rng, half)
	}
	got, _ := io.ReadAll(mustOpen(t, target))
	if !bytes.Equal(got, body) {
		t.Errorf("resumed file holds %d bytes, want %d", len(got), len(body))
	}
}

func TestDownload200RestartsFile(t *testing.T) {
	downloadFast(t)
	body := bytes.Repeat([]byte("hostim-backup-data"), 100)
	srv := newBackupServer(t, body)
	srv.ignoreRange = true

	dir := t.TempDir()
	target := dir + "/backup.tar"
	if err := writeFile(target, []byte("stale-partial-content")); err != nil {
		t.Fatal(err)
	}

	size := int64(len(body))
	url := srv.URL + "/dl/bk-1"
	dl := &api.BackupDownload{Id: "bk-1", Phase: "Succeeded", FileName: "backup.tar", SizeBytes: &size, Url: &url}

	var errW bytes.Buffer
	if _, err := fetchToFile(context.Background(), dl, target, io.Discard, &errW, nil); err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(mustOpen(t, target))
	if !bytes.Equal(got, body) {
		t.Errorf("file was not restarted over the stale bytes")
	}
}

func TestDownloadAlreadyComplete(t *testing.T) {
	downloadFast(t)
	body := []byte("already-here")
	srv := newBackupServer(t, body)

	dir := t.TempDir()
	target := dir + "/backup.tar"
	if err := writeFile(target, body); err != nil {
		t.Fatal(err)
	}

	size := int64(len(body))
	url := srv.URL + "/dl/bk-1"
	dl := &api.BackupDownload{Id: "bk-1", Phase: "Succeeded", FileName: "backup.tar", SizeBytes: &size, Url: &url}

	var errW bytes.Buffer
	if _, err := fetchToFile(context.Background(), dl, target, io.Discard, &errW, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, gets, _ := srv.counts(); gets != 0 {
		t.Errorf("url gets = %d, want 0 for an already complete file", gets)
	}
	if !strings.Contains(errW.String(), "Already downloaded.") {
		t.Errorf("stderr = %q, want the already-downloaded notice", errW.String())
	}
}

func TestDownloadFailedPhase(t *testing.T) {
	downloadFast(t)
	srv := newBackupServer(t, []byte("x"))
	srv.phase = "Failed"

	var errW bytes.Buffer
	_, err := prepareDownload(context.Background(), backupClient(t, srv.URL), "hpr-1", "bk-1", &errW)
	if err == nil || !strings.Contains(err.Error(), "prepare failed") {
		t.Fatalf("err = %v, want the failure reason", err)
	}
}

func TestDownloadConflictMessage(t *testing.T) {
	downloadFast(t)
	srv := newBackupServer(t, []byte("x"))
	srv.createCode = http.StatusConflict
	srv.createErr = "two downloads are already being prepared"

	_, err := prepareDownload(context.Background(), backupClient(t, srv.URL), "hpr-1", "bk-1", io.Discard)
	if err == nil || !strings.Contains(err.Error(), "two downloads are already being prepared") {
		t.Fatalf("err = %v, want the server message", err)
	}
}

func TestRetentionLine(t *testing.T) {
	n := func(v int) *int { return &v }
	got := retentionLine(api.BackupRetention{KeepLast: n(3), KeepDaily: n(7), KeepWeekly: n(4), KeepMonthly: n(6)})
	if got != "Retention: last 3, daily 7, weekly 4, monthly 6" {
		t.Errorf("retentionLine = %q", got)
	}
	if got := retentionLine(api.BackupRetention{}); got != "Retention: none" {
		t.Errorf("empty retentionLine = %q", got)
	}
}

func TestHumanBytes(t *testing.T) {
	cases := map[int64]string{512: "512B", 2048: "2.0KB", 5 * 1024 * 1024: "5.0MB"}
	for in, want := range cases {
		if got := humanBytes(in); got != want {
			t.Errorf("humanBytes(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestUnpackHint(t *testing.T) {
	for name, want := range map[string]string{
		"main-20261001-1020.sql.zst": "Unpack with: zstd -d main-20261001-1020.sql.zst",
		"data-20261001-1020.tar.zst": "Unpack with: tar --zstd -xf data-20261001-1020.tar.zst",
		"old.sql":                    "",
	} {
		if got := unpackHint(name); got != want {
			t.Errorf("unpackHint(%q) = %q, want %q", name, got, want)
		}
	}
}
