package cmd

import (
	"testing"
	"time"

	"github.com/hostimdev/cli/api"
)

// --follow tracks the newest event seen, so a poll returning the same rows
// again prints nothing.
func TestLastEventAt(t *testing.T) {
	base := time.Now().Truncate(time.Second)
	events := []api.Event{
		{Timestamp: base},
		{Timestamp: base.Add(2 * time.Minute)},
		{Timestamp: base.Add(time.Minute)},
	}
	if got := lastEventAt(events); !got.Equal(base.Add(2 * time.Minute)) {
		t.Errorf("lastEventAt = %v, want the newest regardless of position", got)
	}
	if !lastEventAt(nil).IsZero() {
		t.Error("no events must yield the zero time so the first poll prints what it finds")
	}
}
