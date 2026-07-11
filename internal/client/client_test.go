package client

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hostimdev/cli/api"
	"github.com/hostimdev/cli/internal/config"
)

func TestCheck(t *testing.T) {
	if err := Check(200, nil); err != nil {
		t.Errorf("200 should be nil, got %v", err)
	}
	if err := Check(204, []byte("")); err != nil {
		t.Errorf("204 should be nil, got %v", err)
	}

	err := Check(404, []byte(`{"error":"not_found","message":"app not found"}`))
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if apiErr.Status != 404 || apiErr.Code != "not_found" {
		t.Errorf("got %+v", apiErr)
	}
	if apiErr.Error() != "app not found (not_found)" {
		t.Errorf("message = %q", apiErr.Error())
	}

	// Non-JSON body falls back to a status-based message.
	err = Check(500, []byte("boom"))
	if err == nil || err.Error() != "request failed with status 500" {
		t.Errorf("fallback message = %v", err)
	}
}

func TestNewRequiresToken(t *testing.T) {
	if _, err := New(config.Resolved{APIURL: "https://x"}); err != config.ErrNoToken {
		t.Errorf("err = %v, want ErrNoToken", err)
	}
}

func TestBearerHeaderInjected(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(200)
		w.Write([]byte("[]"))
	}))
	defer srv.Close()

	c, err := New(config.Resolved{Token: "sekret", APIURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetProjectsWithResponse(context.Background()); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer sekret" {
		t.Errorf("Authorization = %q, want Bearer sekret", gotAuth)
	}
}

func TestRetryOn429RespectsRetryAfter(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.Header().Set("Retry-After", "0") // don't slow the test
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error":"rate_limited"}`))
			return
		}
		w.WriteHeader(200)
		w.Write([]byte("[]"))
	}))
	defer srv.Close()

	c, err := New(config.Resolved{Token: "t", APIURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.GetProjectsWithResponse(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode() != 200 {
		t.Errorf("final status = %d, want 200 after retry", resp.StatusCode())
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Errorf("server calls = %d, want 2 (one 429 + one success)", got)
	}
}

func TestRetryGivesUpAfterMax(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":"rate_limited"}`))
	}))
	defer srv.Close()

	c, err := New(config.Resolved{Token: "t", APIURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.GetProjectsWithResponse(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode() != http.StatusTooManyRequests {
		t.Errorf("status = %d, want 429 after exhausting retries", resp.StatusCode())
	}
	// initial + 4 retries = 5 attempts
	if got := atomic.LoadInt32(&calls); got != 5 {
		t.Errorf("calls = %d, want 5", got)
	}
}

func TestPollBuild(t *testing.T) {
	tests := []struct {
		name     string
		statuses []api.AppStatusBuildStatus
		wantErr  error
		wantEnd  api.AppStatusBuildStatus
	}{
		{"succeeds after running", []api.AppStatusBuildStatus{
			api.AppStatusBuildStatusRunning, api.AppStatusBuildStatusSucceeded,
		}, nil, api.AppStatusBuildStatusSucceeded},
		{"fails", []api.AppStatusBuildStatus{
			api.AppStatusBuildStatusRunning, api.AppStatusBuildStatusFailed,
		}, ErrBuildFailed, api.AppStatusBuildStatusFailed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var idx int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				i := atomic.AddInt32(&idx, 1) - 1
				if int(i) >= len(tc.statuses) {
					i = int32(len(tc.statuses) - 1)
				}
				bs := tc.statuses[i]
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(200)
				w.Write([]byte(`{"buildStatus":"` + string(bs) + `"}`))
			}))
			defer srv.Close()

			c, err := New(config.Resolved{Token: "t", APIURL: srv.URL})
			if err != nil {
				t.Fatal(err)
			}
			res, err := PollBuild(context.Background(), c, "proj", "app", time.Millisecond, true, nil)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if res.BuildStatus != tc.wantEnd {
				t.Errorf("end build status = %q, want %q", res.BuildStatus, tc.wantEnd)
			}
		})
	}
}

// TestPollBuildDocker covers a build-less (docker image) deploy: buildStatus
// stays empty and the poll terminates on runtimeStatus instead.
func TestPollBuildDocker(t *testing.T) {
	tests := []struct {
		name     string
		statuses []api.AppStatusRuntimeStatus
		wantErr  error
		wantEnd  api.AppStatusRuntimeStatus
	}{
		{"running", []api.AppStatusRuntimeStatus{
			api.AppStatusRuntimeStatusPending, api.AppStatusRuntimeStatusRunning,
		}, nil, api.AppStatusRuntimeStatusRunning},
		{"imagePullBackoff", []api.AppStatusRuntimeStatus{
			api.AppStatusRuntimeStatusPending, api.AppStatusRuntimeStatusImagePullBackoff,
		}, ErrDeployFailed, api.AppStatusRuntimeStatusImagePullBackoff},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var idx int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				i := atomic.AddInt32(&idx, 1) - 1
				if int(i) >= len(tc.statuses) {
					i = int32(len(tc.statuses) - 1)
				}
				rs := tc.statuses[i]
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(200)
				w.Write([]byte(`{"buildStatus":"","runtimeStatus":"` + string(rs) + `"}`))
			}))
			defer srv.Close()

			c, err := New(config.Resolved{Token: "t", APIURL: srv.URL})
			if err != nil {
				t.Fatal(err)
			}
			res, err := PollBuild(context.Background(), c, "proj", "app", time.Millisecond, false, nil)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if res.RuntimeStatus != tc.wantEnd {
				t.Errorf("end runtime status = %q, want %q", res.RuntimeStatus, tc.wantEnd)
			}
		})
	}
}

func TestPollBuildCancels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte(`{"buildStatus":"running"}`))
	}))
	defer srv.Close()

	c, err := New(config.Resolved{Token: "t", APIURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := PollBuild(ctx, c, "proj", "app", 5*time.Millisecond, true, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want DeadlineExceeded", err)
	}
}
