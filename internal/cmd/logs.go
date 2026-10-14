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

// maxLines caps how far one `hostim logs` call pages back. The API serves at
// most pageSize lines per request, so this is up to 10 requests.
const (
	pageSize = 1000
	maxLines = 10000
)

func newLogsCmd(c *cli) *cobra.Command {
	var (
		follow       bool
		tail         int
		since, until string
		build        bool
	)

	cmd := &cobra.Command{
		Use:   "logs <app>",
		Short: "Show logs for an app",
		Long: "Show an app's logs, oldest line first.\n" +
			"Covers every container the app ran in the last 7 days, so lines from before a\n" +
			"redeploy or a crash are still there. Defaults to stdout and stderr; use --build\n" +
			"for build logs.\n\n" +
			"--since and --until take a duration (2h) or a time (2026-10-14T14:00, RFC 3339 or\n" +
			"local time). With either set, every line in the window is shown, up to 10000.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			now := time.Now()
			from, err := parseLogTime("--since", since, now)
			if err != nil {
				return err
			}
			to, err := parseLogTime("--until", until, now)
			if err != nil {
				return err
			}
			if !from.IsZero() && !to.IsZero() && !from.Before(to) {
				return fmt.Errorf("--since must be before --until")
			}
			// A window means "show me all of it"; only a bare call keeps the
			// short default.
			if !cmd.Flags().Changed("tail") && (since != "" || until != "") {
				tail = maxLines
			}
			if tail < 1 || tail > maxLines {
				return fmt.Errorf("--tail must be between 1 and %d", maxLines)
			}
			if follow && c.printer.Format == output.FormatJSON {
				return fmt.Errorf("--follow cannot be combined with -o json")
			}
			if follow && until != "" {
				return fmt.Errorf("--follow cannot be combined with --until")
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

			logs, truncated, err := fetchWindow(cmd, cl, project, args[0], logType, from, to, tail)
			if err != nil {
				return err
			}
			if truncated && (since != "" || until != "") {
				fmt.Fprintf(cmd.ErrOrStderr(), "Showing the newest %d lines of the window; raise --tail (up to 10000) or narrow --since/--until to see older ones.\n", len(logs))
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
			return followLogs(cmd, cl, project, args[0], logType, min(tail, pageSize), cursorOf(logs), build)
		},
	}
	f := cmd.Flags()
	f.BoolVarP(&follow, "follow", "f", false, "stream new logs as they arrive")
	f.IntVarP(&tail, "tail", "n", 100, "number of lines to show, newest kept (1-10000; all of the window up to 10000 with --since/--until)")
	f.StringVar(&since, "since", "", "only lines after this: a duration (2h) or a time (2026-10-14T14:00)")
	f.StringVar(&until, "until", "", "only lines before this: a duration (30m) or a time (2026-10-14T15:00)")
	f.BoolVar(&build, "build", false, "show build logs instead of the container's output")
	return cmd
}

// parseLogTime reads a --since/--until value: a duration back from now, an
// RFC 3339 time, or a local date and time without a zone. Empty means unset.
func parseLogTime(flag, v string, now time.Time) (time.Time, error) {
	if v == "" {
		return time.Time{}, nil
	}
	if d, err := time.ParseDuration(v); err == nil {
		if d <= 0 {
			return time.Time{}, fmt.Errorf("%s must be a positive duration, e.g. 2h", flag)
		}
		return now.Add(-d), nil
	}
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t, nil
	}
	for _, layout := range []string{"2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02 15:04:05", "2006-01-02 15:04", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, v, time.Local); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("%s %q is neither a duration (2h) nor a time (2026-10-14T14:00)", flag, v)
}

// fetchWindow pages back from `to` (now when zero) until it reaches `from`,
// runs out of logs, or holds `limit` lines. It returns reading order, and
// truncated when it stopped on the limit with older lines left in the window.
//
// ponytail: the API's `before` cursor is exclusive, so lines sharing the exact
// nanosecond of a page boundary can drop out; switch to an offset cursor if
// that ever shows up in practice.
func fetchWindow(cmd *cobra.Command, cl *api.ClientWithResponses, project, app string, logType api.GetAppLogsParamsLogType, from, to time.Time, limit int) ([]api.Log, bool, error) {
	var out []api.Log
	var before string
	if !to.IsZero() {
		before = strconv.FormatInt(to.UnixNano(), 10)
	}
	cutoff := int64(0)
	if !from.IsZero() {
		cutoff = from.UnixNano()
	}
	for len(out) < limit {
		params := api.GetAppLogsParams{LogType: logType, Limit: min(pageSize, limit-len(out))}
		if before != "" {
			params.Before = &before
		}
		page, err := fetchLogs(cmd, cl, project, app, params)
		if err != nil {
			return nil, false, err
		}
		page = oldestFirst(page)
		for i := len(page) - 1; i >= 0 && len(out) < limit; i-- {
			if logNanos(page[i]) < cutoff {
				return oldestFirst(out), false, nil
			}
			out = append(out, page[i])
		}
		if len(page) < params.Limit {
			return oldestFirst(out), false, nil
		}
		before = page[0].Timestamp
	}
	return oldestFirst(out), true, nil
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
			return fmt.Errorf("build failed; the reason is in the build log above")
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
		return nil, fmt.Errorf("app %q not found in this project; list apps with `hostim apps ls`", app)
	}
	if err := checkResp(resp.StatusCode(), resp.Body); err != nil {
		return nil, err
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("app %q not found in this project; list apps with `hostim apps ls`", app)
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
