package cmd

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/hostimdev/cli/api"
	"github.com/hostimdev/cli/internal/output"
)

func newEventsCmd(c *cli) *cobra.Command {
	var (
		follow bool
		limit  int
	)

	cmd := &cobra.Command{
		Use:   "events <app>",
		Short: "Show status events for an app",
		Long: "Show an app's status events, oldest first: what the platform did and\n" +
			"why it changed state. `status` shows only the current value, so this is\n" +
			"where you look when an app went unhealthy and came back.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if limit < 1 || limit > 1000 {
				return fmt.Errorf("--limit must be between 1 and 1000")
			}
			if follow && c.printer.Format == output.FormatJSON {
				return fmt.Errorf("--follow cannot be combined with -o json")
			}

			cl, project, err := c.clientAndProject(cmd.Context())
			if err != nil {
				return err
			}

			// The events endpoint answers 200 with [] for an app that does not
			// exist, so a typo would look like a quiet app. Resolve it first.
			if _, err := getApp(cmd, cl, project, args[0]); err != nil {
				return err
			}

			events, err := fetchEvents(cmd, cl, project, args[0], limit)
			if err != nil {
				return err
			}

			if !follow {
				if len(events) == 0 && c.printer.Format == output.FormatTable {
					fmt.Fprintln(cmd.OutOrStdout(), "No events found.")
					return nil
				}
				return c.printer.Render(events, []string{"TIME", "TYPE", "REASON", "MESSAGE"}, eventRows(events))
			}

			for _, e := range events {
				fmt.Fprintln(cmd.OutOrStdout(), eventLine(e))
			}
			return followEvents(cmd, cl, project, args[0], limit, lastEventAt(events))
		},
	}

	f := cmd.Flags()
	f.BoolVarP(&follow, "follow", "f", false, "keep printing events as they arrive")
	f.IntVarP(&limit, "limit", "n", 100, "number of events to fetch (1-1000)")
	return cmd
}

// followEvents polls for events newer than `since` until interrupted. The API
// also has an SSE stream, but it carries the same rows on the same 1s server
// poll, so polling here keeps one code path instead of a second transport.
func followEvents(cmd *cobra.Command, cl *api.ClientWithResponses, project, app string, limit int, since time.Time) error {
	ticker := time.NewTicker(followInterval)
	defer ticker.Stop()
	for {
		select {
		case <-cmd.Context().Done():
			return nil
		case <-ticker.C:
		}

		events, err := fetchEvents(cmd, cl, project, app, limit)
		if err != nil {
			return err
		}
		for _, e := range events {
			if !e.Timestamp.After(since) {
				continue
			}
			fmt.Fprintln(cmd.OutOrStdout(), eventLine(e))
		}
		if t := lastEventAt(events); t.After(since) {
			since = t
		}
	}
}

// fetchEvents returns the newest `limit` events, oldest first (the order the
// API serves them in).
func fetchEvents(cmd *cobra.Command, cl *api.ClientWithResponses, project, app string, limit int) ([]api.Event, error) {
	resp, err := cl.GetAppEventsWithResponse(cmd.Context(), project, app, &api.GetAppEventsParams{Limit: &limit})
	if err != nil {
		return nil, err
	}
	if err := checkResp(resp.StatusCode(), resp.Body); err != nil {
		return nil, err
	}
	if resp.JSON200 == nil {
		return nil, nil
	}
	return *resp.JSON200, nil
}

// lastEventAt is the timestamp of the newest event, or the zero time when there
// are none — which makes the first --follow poll print everything it finds.
func lastEventAt(events []api.Event) time.Time {
	var last time.Time
	for _, e := range events {
		if e.Timestamp.After(last) {
			last = e.Timestamp
		}
	}
	return last
}

func eventTime(e api.Event) string {
	return e.Timestamp.Local().Format(time.RFC3339)
}

func eventLine(e api.Event) string {
	return fmt.Sprintf("%s  %-7s %-24s %s", eventTime(e), e.Type, e.Reason, e.Message)
}

func eventRows(events []api.Event) [][]string {
	rows := make([][]string, 0, len(events))
	for _, e := range events {
		rows = append(rows, []string{eventTime(e), string(e.Type), e.Reason, e.Message})
	}
	return rows
}
