package cmd

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/hostimdev/cli/api"
)

// buildLogServer serves one batch of build logs and then nothing, with the
// build sitting at the given terminal status.
func buildLogServer(t *testing.T, buildStatus string) *httptest.Server {
	t.Helper()
	served := false
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/logs"):
			if served {
				_, _ = w.Write([]byte(`[]`))
				return
			}
			served = true
			ts := strconv.FormatInt(time.Now().UnixNano(), 10)
			_, _ = w.Write([]byte(`[{"message":"step 1","timestamp":"` + ts + `","type":"build"}]`))
		case strings.HasSuffix(r.URL.Path, "/status"):
			_, _ = w.Write([]byte(`{"buildStatus":"` + buildStatus + `"}`))
		default:
			t.Errorf("unexpected request to %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

// A build log is a finite stream: --follow must return once the build reaches a
// terminal status instead of polling a stream that will never grow.
func TestFollowLogsStopsWhenBuildEnds(t *testing.T) {
	old := followInterval
	followInterval = time.Millisecond
	defer func() { followInterval = old }()

	cases := []struct {
		status  string
		wantErr bool
	}{
		{"succeeded", false},
		{"failed", true}, // non-zero exit, so CI can gate on it
	}

	for _, tc := range cases {
		t.Run(tc.status, func(t *testing.T) {
			srv := buildLogServer(t, tc.status)
			defer srv.Close()
			cl, err := api.NewClientWithResponses(srv.URL)
			if err != nil {
				t.Fatal(err)
			}

			cmd := &cobra.Command{}
			var out bytes.Buffer
			cmd.SetOut(&out)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cmd.SetContext(ctx)

			err = followLogs(cmd, cl, "hpr-1", "app", api.GetAppLogsParamsLogTypeBuild, 100, "", true)

			if ctx.Err() != nil {
				t.Fatal("followLogs did not return on its own; it hung until the test timeout")
			}
			if tc.wantErr && err == nil {
				t.Error("a failed build must return an error")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("succeeded build returned %v", err)
			}
			if !strings.Contains(out.String(), "step 1") {
				t.Errorf("build line was not printed, got %q", out.String())
			}
		})
	}
}
