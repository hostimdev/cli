package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/hostimdev/cli/api"
)

func ns(t time.Time) string { return strconv.FormatInt(t.UnixNano(), 10) }

// The API returns newest-first; everything downstream assumes reading order.
func TestOldestFirstAndCursor(t *testing.T) {
	now := time.Now()
	newest := api.Log{Message: "third", Timestamp: ns(now)}
	middle := api.Log{Message: "second", Timestamp: ns(now.Add(-time.Minute))}
	oldest := api.Log{Message: "first", Timestamp: ns(now.Add(-time.Hour))}

	got := oldestFirst([]api.Log{newest, middle, oldest})

	want := []string{"first", "second", "third"}
	for i, w := range want {
		if got[i].Message != w {
			t.Fatalf("position %d = %q, want %q", i, got[i].Message, w)
		}
	}
	// The cursor must be the NEWEST entry, or --follow re-prints old lines
	// forever.
	if c := cursorOf(got); c != newest.Timestamp {
		t.Errorf("cursorOf = %q, want newest %q", c, newest.Timestamp)
	}
	if c := cursorOf(nil); c != "" {
		t.Errorf("cursorOf(nil) = %q, want empty", c)
	}
}

// pagedLogServer serves `total` lines one second apart, ending at `end`, the
// way the API does: newest first, at most `limit`, strictly before `before`.
func pagedLogServer(t *testing.T, end time.Time, total int) (*httptest.Server, *int) {
	t.Helper()
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		q := r.URL.Query()
		limit, _ := strconv.Atoi(q.Get("limit"))
		before := end.UnixNano() + 1
		if b := q.Get("before"); b != "" {
			before, _ = strconv.ParseInt(b, 10, 64)
		}
		var page []api.Log
		for i := 0; i < total && len(page) < limit; i++ {
			ts := end.Add(-time.Duration(i) * time.Second)
			if ts.UnixNano() < before {
				page = append(page, api.Log{Message: strconv.Itoa(i), Timestamp: ns(ts), Type: "stdout"})
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(page)
	}))
	return srv, &requests
}

// A window wider than one API page must page back until it reaches --since,
// with no line lost or repeated at the page boundaries.
func TestFetchWindowPagesBackToSince(t *testing.T) {
	end := time.Now().Add(-time.Minute)
	srv, requests := pagedLogServer(t, end, 2500) // 2500 lines, one per second
	defer srv.Close()
	cl, err := api.NewClientWithResponses(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())

	// The newest 2100 lines: index 0 (newest) to 2099.
	from := end.Add(-2099 * time.Second)
	logs, truncated, err := fetchWindow(cmd, cl, "hpr-1", "app", api.GetAppLogsParamsLogTypeStdout, from, time.Time{}, maxLines)
	if err != nil {
		t.Fatal(err)
	}
	if truncated {
		t.Error("window was fully read, truncated must be false")
	}
	if len(logs) != 2100 {
		t.Fatalf("got %d lines, want 2100", len(logs))
	}
	seen := map[string]bool{}
	for i, l := range logs {
		if seen[l.Message] {
			t.Fatalf("line %s repeated", l.Message)
		}
		seen[l.Message] = true
		if i > 0 && logNanos(logs[i-1]) >= logNanos(l) {
			t.Fatalf("not in reading order at %d", i)
		}
	}
	if *requests != 3 {
		t.Errorf("made %d requests, want 3 pages of up to 1000", *requests)
	}
}

func TestFetchWindowLimitAndUntil(t *testing.T) {
	end := time.Now().Add(-time.Minute)
	srv, _ := pagedLogServer(t, end, 2500)
	defer srv.Close()
	cl, err := api.NewClientWithResponses(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())

	// --until 10s before the newest line, capped at 1500 lines: lines 10..1509.
	logs, truncated, err := fetchWindow(cmd, cl, "hpr-1", "app", api.GetAppLogsParamsLogTypeStdout, time.Time{}, end.Add(-9500*time.Millisecond), 1500)
	if err != nil {
		t.Fatal(err)
	}
	if !truncated {
		t.Error("older lines were left, truncated must be true")
	}
	if len(logs) != 1500 || logs[len(logs)-1].Message != "10" || logs[0].Message != "1509" {
		t.Fatalf("got %d lines from %s to %s, want 1500 from 1509 to 10", len(logs), logs[0].Message, logs[len(logs)-1].Message)
	}

	// A bare call stays one request of `tail` lines, as before.
	logs, _, err = fetchWindow(cmd, cl, "hpr-1", "app", api.GetAppLogsParamsLogTypeStdout, time.Time{}, time.Time{}, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 100 || logs[len(logs)-1].Message != "0" {
		t.Fatalf("bare call got %d lines ending at %s, want the newest 100", len(logs), logs[len(logs)-1].Message)
	}
}

func TestParseLogTime(t *testing.T) {
	now := time.Date(2026, 10, 14, 16, 0, 0, 0, time.Local)
	cases := []struct {
		in   string
		want time.Time
		err  bool
	}{
		{"", time.Time{}, false},
		{"2h", now.Add(-2 * time.Hour), false},
		{"2026-10-14T14:00", time.Date(2026, 10, 14, 14, 0, 0, 0, time.Local), false},
		{"2026-10-14 14:00:30", time.Date(2026, 10, 14, 14, 0, 30, 0, time.Local), false},
		{"2026-10-14T12:00:00Z", time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC), false},
		{"2026-10-13", time.Date(2026, 10, 13, 0, 0, 0, 0, time.Local), false},
		{"-5m", time.Time{}, true},
		{"yesterday", time.Time{}, true},
	}
	for _, tc := range cases {
		got, err := parseLogTime("--since", tc.in, now)
		if (err != nil) != tc.err {
			t.Errorf("%q: err = %v, want error %v", tc.in, err, tc.err)
			continue
		}
		if !got.Equal(tc.want) {
			t.Errorf("%q = %v, want %v", tc.in, got, tc.want)
		}
	}
}
