package cmd

import (
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/hostimdev/cli/api"
	"github.com/hostimdev/cli/internal/output"
)

// followInterval is how often --follow asks for logs newer than the last one
// seen. Loki ingestion lags a little anyway, so polling faster just burns rate
// limit budget.
var followInterval = 2 * time.Second

func newLogsCmd(c *cli) *cobra.Command {
	var (
		follow bool
		tail   int
		since  time.Duration
		build  bool
	)

	cmd := &cobra.Command{
		Use:   "logs <app>",
		Short: "Show logs for an app",
		Long: "Show an app's logs, oldest line first.\n" +
			"Defaults to the running container's stdout; use --build for build logs.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if tail < 1 || tail > 1000 {
				return fmt.Errorf("--tail must be between 1 and 1000")
			}
			if follow && c.printer.Format == output.FormatJSON {
				return fmt.Errorf("--follow cannot be combined with -o json")
			}

			cl, project, err := c.clientAndProject(cmd.Context())
			if err != nil {
				return err
			}

			// The logs endpoint answers 200 with [] for an app that does not
			// exist — Loki simply has no stream under that label — so a typo
			// would otherwise look exactly like a quiet app. Resolve the app
			// first and fail loudly instead.
			app, err := getApp(cmd, cl, project, args[0])
			if err != nil {
				return err
			}

			logType := api.GetAppLogsParamsLogTypeStdout
			if build {
				if app.DeploymentSource.Type == api.Docker {
					return fmt.Errorf("app %q deploys a prebuilt docker image, so it has no build logs", args[0])
				}
				logType = api.GetAppLogsParamsLogTypeBuild
			}

			// No cursor on the first call: the API returns the newest `tail`
			// entries, newest first.
			logs, err := fetchLogs(cmd, cl, project, args[0], api.GetAppLogsParams{
				LogType: logType,
				Limit:   tail,
			})
			if err != nil {
				return err
			}
			logs = oldestFirst(logs)
			if since > 0 {
				logs = newerThan(logs, time.Now().Add(-since))
			}

			if !follow {
				// Render's empty-state line says "No resources found.", which
				// reads wrong for logs.
				if len(logs) == 0 && c.printer.Format == output.FormatTable {
					fmt.Fprintln(cmd.OutOrStdout(), "No logs found.")
					return nil
				}
				return c.printer.Render(logs, []string{"TIME", "MESSAGE"}, logRows(logs))
			}

			for _, l := range logs {
				fmt.Fprintln(cmd.OutOrStdout(), logLine(l))
			}
			return followLogs(cmd, cl, project, args[0], logType, tail, cursorOf(logs), build)
		},
	}

	f := cmd.Flags()
	f.BoolVarP(&follow, "follow", "f", false, "stream new logs as they arrive")
	f.IntVarP(&tail, "tail", "n", 100, "number of lines to fetch (1-1000)")
	f.DurationVar(&since, "since", 0, "only show logs newer than a duration, e.g. 1h or 15m")
	f.BoolVar(&build, "build", false, "show build logs instead of the container's stdout")
	return cmd
}

// followLogs polls for entries newer than cursor until the process is
// interrupted. An empty cursor means the app had no logs yet, so the next poll
// falls back to asking for the latest batch again.
//
// When untilBuildEnds is set, the stream is a build log, which is finite: stop
// once the build reaches a terminal status instead of hanging on a stream that
// will never produce another line. A failed build exits non-zero, so
// `hostim logs <app> --build -f` can gate a pipeline the way `deploy` does.
func followLogs(cmd *cobra.Command, cl *api.ClientWithResponses, project, app string, logType api.GetAppLogsParamsLogType, tail int, cursor string, untilBuildEnds bool) error {
	ticker := time.NewTicker(followInterval)
	defer ticker.Stop()
	for {
		select {
		case <-cmd.Context().Done():
			return nil
		case <-ticker.C:
		}

		next, err := pollAndPrint(cmd, cl, project, app, logType, tail, cursor)
		if err != nil {
			return err
		}
		cursor = next

		if !untilBuildEnds {
			continue
		}
		status, err := buildStatus(cmd, cl, project, app)
		if err != nil {
			return err
		}
		if status != api.AppStatusBuildStatusSucceeded && status != api.AppStatusBuildStatusFailed {
			continue
		}
		// The build is over, but Loki ingestion lags the status flip by a
		// moment; poll once more so the last lines are not cut off.
		time.Sleep(followInterval)
		if _, err := pollAndPrint(cmd, cl, project, app, logType, tail, cursor); err != nil {
			return err
		}
		if status == api.AppStatusBuildStatusFailed {
			return fmt.Errorf("build failed")
		}
		return nil
	}
}

// pollAndPrint fetches everything newer than cursor, prints it in reading
// order, and returns the cursor for the next poll (unchanged when nothing
// arrived).
func pollAndPrint(cmd *cobra.Command, cl *api.ClientWithResponses, project, app string, logType api.GetAppLogsParamsLogType, tail int, cursor string) (string, error) {
	params := api.GetAppLogsParams{LogType: logType, Limit: tail}
	if cursor != "" {
		params.After = &cursor
	}
	logs, err := fetchLogs(cmd, cl, project, app, params)
	if err != nil {
		return cursor, err
	}
	logs = oldestFirst(logs)
	for _, l := range logs {
		fmt.Fprintln(cmd.OutOrStdout(), logLine(l))
	}
	if c := cursorOf(logs); c != "" {
		return c, nil
	}
	return cursor, nil
}

// buildStatus reports the app's current build status, or "" when the API does
// not give one (a docker-source app has no build phase).
func buildStatus(cmd *cobra.Command, cl *api.ClientWithResponses, project, app string) (api.AppStatusBuildStatus, error) {
	resp, err := cl.GetAppStatusWithResponse(cmd.Context(), project, app)
	if err != nil {
		return "", err
	}
	if err := checkResp(resp.StatusCode(), resp.Body); err != nil {
		return "", err
	}
	if resp.JSON200 == nil || resp.JSON200.BuildStatus == nil {
		return "", nil
	}
	return *resp.JSON200.BuildStatus, nil
}

// getApp fetches an app so a missing one fails before we ask for its logs.
func getApp(cmd *cobra.Command, cl *api.ClientWithResponses, project, app string) (*api.App, error) {
	resp, err := cl.GetAppWithResponse(cmd.Context(), project, app)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode() == 404 {
		return nil, fmt.Errorf("app %q not found in this project", app)
	}
	if err := checkResp(resp.StatusCode(), resp.Body); err != nil {
		return nil, err
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("app %q not found in this project", app)
	}
	return resp.JSON200, nil
}

func fetchLogs(cmd *cobra.Command, cl *api.ClientWithResponses, project, app string, params api.GetAppLogsParams) ([]api.Log, error) {
	resp, err := cl.GetAppLogsWithResponse(cmd.Context(), project, app, &params)
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

// oldestFirst reverses the API's newest-first ordering into reading order.
// It sorts rather than reverses because the backend merges several Loki
// streams, and only its own final sort guarantees the order we invert.
func oldestFirst(logs []api.Log) []api.Log {
	out := make([]api.Log, len(logs))
	copy(out, logs)
	sort.SliceStable(out, func(i, j int) bool {
		return logNanos(out[i]) < logNanos(out[j])
	})
	return out
}

// newerThan drops entries older than t. The API has no `since` parameter — it
// serves a fixed retention window — so --since is applied here.
func newerThan(logs []api.Log, t time.Time) []api.Log {
	cutoff := t.UnixNano()
	out := make([]api.Log, 0, len(logs))
	for _, l := range logs {
		if logNanos(l) >= cutoff {
			out = append(out, l)
		}
	}
	return out
}

// cursorOf returns the timestamp of the newest entry, for the next poll.
func cursorOf(logs []api.Log) string {
	if len(logs) == 0 {
		return ""
	}
	return logs[len(logs)-1].Timestamp
}

// logNanos parses a log's nanosecond timestamp, returning 0 when it is
// unparseable so a malformed entry sorts first instead of dropping out.
func logNanos(l api.Log) int64 {
	n, err := strconv.ParseInt(l.Timestamp, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

func logTime(l api.Log) string {
	n := logNanos(l)
	if n == 0 {
		return "-"
	}
	return time.Unix(0, n).Format("2006-01-02 15:04:05")
}

func logLine(l api.Log) string {
	return logTime(l) + "  " + l.Message
}

func logRows(logs []api.Log) [][]string {
	rows := make([][]string, 0, len(logs))
	for _, l := range logs {
		rows = append(rows, []string{logTime(l), l.Message})
	}
	return rows
}
