package cmd

import (
	"strconv"
	"testing"
	"time"

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

func TestNewerThan(t *testing.T) {
	now := time.Now()
	logs := []api.Log{
		{Message: "old", Timestamp: ns(now.Add(-2 * time.Hour))},
		{Message: "recent", Timestamp: ns(now.Add(-10 * time.Minute))},
	}

	got := newerThan(logs, now.Add(-time.Hour))

	if len(got) != 1 || got[0].Message != "recent" {
		t.Fatalf("newerThan = %v, want only the recent entry", got)
	}
}
