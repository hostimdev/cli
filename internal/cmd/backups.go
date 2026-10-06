package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/hostimdev/cli/api"
	"github.com/hostimdev/cli/internal/client"
)

// backupPollInterval is how often `backups download` asks whether the prepared
// file is ready. Preparing a large backup takes minutes, so a tight loop would
// only burn rate limit budget.
var backupPollInterval = 3 * time.Second

// downloadProgressInterval is how often a non-terminal stderr gets a progress
// line. A terminal gets an overwritten line on every poll instead.
var downloadProgressInterval = 30 * time.Second

// downloadBackoff is the wait before each retry of an interrupted transfer. One
// wait per retry: five retries after the first attempt.
var downloadBackoff = []time.Duration{
	1 * time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second,
}

// errDownloadInterrupted is returned when the user stops the wait with Ctrl-C.
// The download stays prepared on the server, so the same command can be run
// again.
var errDownloadInterrupted = errors.New("stopped waiting for the download; run the same command again later to resume")

// errExpiredURL marks a 403 from the pre-signed URL, which means it expired and
// a fresh one is needed.
type errExpiredURL struct{}

func (errExpiredURL) Error() string { return "the download URL expired" }

// permanentDownloadError wraps a failure that retrying cannot fix.
type permanentDownloadError struct{ err error }

func (e permanentDownloadError) Error() string { return e.err.Error() }
func (e permanentDownloadError) Unwrap() error { return e.err }

func newBackupsCmd(c *cli) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "backups",
		Short: "List a project's backups and download one",
	}
	cmd.AddCommand(
		backupsListCmd(c),
		backupsDownloadCmd(c),
	)
	// Bare `hostim backups` shows the overview.
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		return backupsOverview(cmd, c)
	}
	return cmd
}

// backupsOverview prints the backup overview: one row per database or volume,
// then the schedule and retention it runs under.
func backupsOverview(cmd *cobra.Command, c *cli) error {
	cl, project, err := c.clientAndProject(cmd.Context())
	if err != nil {
		return err
	}
	resp, err := cl.GetBackupOverviewWithResponse(cmd.Context(), project)
	if err != nil {
		return err
	}
	if err := checkResp(resp.StatusCode(), resp.Body); err != nil {
		return err
	}
	ov := resp.JSON200
	if ov == nil {
		return client.ErrEmptyResponse
	}
	if c.jsonOut() {
		p := c.printer
		p.Out = cmd.OutOrStdout()
		return p.JSON(ov)
	}
	if !ov.Enabled {
		fmt.Fprintln(cmd.OutOrStdout(), "Backups are not available for this project yet.")
		return nil
	}
	out := cmd.OutOrStdout()
	rows := make([][]string, 0, len(ov.Resources))
	for _, r := range ov.Resources {
		rows = append(rows, []string{r.Kind, r.Name, backupTime(r.LastBackupTime)})
	}
	if len(rows) == 0 {
		fmt.Fprintln(out, "Nothing to back up yet. Add a database or volume, and the next run backs it up.")
	} else if err := c.printer.Table([]string{"KIND", "NAME", "LAST BACKUP (" + localZone() + ")"}, rows); err != nil {
		return err
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, scheduleLine(ov.Schedule, ov.NextRunTime))
	if keep := retentionLine(ov.Retention); keep != "" {
		fmt.Fprintln(out, keep)
	}
	if len(rows) > 0 {
		fmt.Fprintln(out, "List backups: hostim backups ls <name>")
	}
	return nil
}

func backupsListCmd(c *cli) *cobra.Command {
	var kind string
	cmd := &cobra.Command{
		Use:     "ls <resource>",
		Aliases: []string{"list"},
		Short:   "List the backups of one database or volume",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, project, err := c.clientAndProject(cmd.Context())
			if err != nil {
				return err
			}
			items, err := listResourceBackups(cmd.Context(), cl, project, args[0], kind)
			if err != nil {
				return err
			}
			rows := make([][]string, 0, len(items))
			for _, b := range items {
				// Backups made before the operator recorded sizes have none.
				size := "-"
				if b.SizeBytes != nil {
					size = humanBytes(*b.SizeBytes)
				}
				rows = append(rows, []string{b.Id, backupTime(&b.Time), b.Trigger, size})
			}
			// The size is the data before compression, not the download size.
			return c.printer.Render(items, []string{"ID", "TIME (" + localZone() + ")", "TRIGGER", "DATA SIZE"}, rows)
		},
	}
	cmd.Flags().StringVar(&kind, "kind", "", "resource kind: postgres, mysql or volume (skips the lookup)")
	return cmd
}

// listResourceBackups returns the backups of one database or volume, newest
// first.
func listResourceBackups(ctx context.Context, cl *api.ClientWithResponses, project, name, kind string) ([]api.Backup, error) {
	k, err := resolveBackupKind(ctx, cl, project, name, kind)
	if err != nil {
		return nil, err
	}
	resp, err := cl.ListBackupsWithResponse(ctx, project, &api.ListBackupsParams{Kind: k, Name: name})
	if err != nil {
		return nil, err
	}
	if err := checkResp(resp.StatusCode(), resp.Body); err != nil {
		return nil, err
	}
	var items []api.Backup
	if resp.JSON200 != nil {
		items = resp.JSON200.Items
	}
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].Time.After(items[j].Time)
	})
	return items, nil
}

// resolveBackupKind finds the kind of a database or volume by name. An explicit
// kind is validated and used as-is; otherwise the overview is searched, and an
// ambiguous or unknown name is an error.
func resolveBackupKind(ctx context.Context, cl *api.ClientWithResponses, project, name, kind string) (api.ListBackupsParamsKind, error) {
	if kind != "" {
		k := api.ListBackupsParamsKind(kind)
		if !k.Valid() {
			return "", fmt.Errorf("--kind must be postgres, mysql or volume, got %q", kind)
		}
		return k, nil
	}
	resp, err := cl.GetBackupOverviewWithResponse(ctx, project)
	if err != nil {
		return "", err
	}
	if err := checkResp(resp.StatusCode(), resp.Body); err != nil {
		return "", err
	}
	if resp.JSON200 == nil {
		return "", client.ErrEmptyResponse
	}
	var kinds []string
	for _, r := range resp.JSON200.Resources {
		if r.Name == name {
			kinds = append(kinds, r.Kind)
		}
	}
	switch len(kinds) {
	case 0:
		return "", userErr("No database or volume named %s.", name)
	case 1:
		return api.ListBackupsParamsKind(kinds[0]), nil
	case 2:
		return "", userErr("%s is both a %s and a %s. Pass --kind.", name, kinds[0], kinds[1])
	default:
		return "", userErr("%s is a %s. Pass --kind.", name, strings.Join(kinds, ", a "))
	}
}

// userErr builds a user-facing error: a proper sentence, capitalized and ending
// in a period, which Go's error-string convention otherwise avoids. The CLI
// prints it after "error: ".
func userErr(format string, args ...any) error { return fmt.Errorf(format, args...) }

func backupsDownloadCmd(c *cli) *cobra.Command {
	var file, kind string
	var latest bool
	cmd := &cobra.Command{
		Use:   "download <id> | --latest <resource>",
		Short: "Download one backup file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if kind != "" && !latest {
				return fmt.Errorf("--kind only works with --latest")
			}
			cl, project, err := c.clientAndProject(cmd.Context())
			if err != nil {
				return err
			}
			id := args[0]
			if latest {
				items, err := listResourceBackups(cmd.Context(), cl, project, args[0], kind)
				if err != nil {
					return err
				}
				if len(items) == 0 {
					return userErr("%s has no backups yet.", args[0])
				}
				id = items[0].Id
			}

			// Ctrl-C stops the wait without killing the process, so the
			// command can report that the download is still prepared.
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
			defer stop()

			errW := cmd.ErrOrStderr()
			dl, err := prepareDownload(ctx, cl, project, id, errW)
			if err != nil {
				return err
			}
			target := file
			if target == "" {
				target = dl.FileName
			}
			if target == "" {
				target = id
			}
			if target == "-" && c.jsonOut() {
				return fmt.Errorf("-f - cannot be combined with -o json")
			}
			already := target != "-" && backupComplete(dl, target)
			refresh := func() (*api.BackupDownload, error) {
				return prepareDownload(ctx, cl, project, id, errW)
			}
			final, err := fetchToFile(ctx, dl, target, cmd.OutOrStdout(), errW, refresh)
			if err != nil {
				return err
			}
			if c.jsonOut() {
				p := c.printer
				p.Out = cmd.OutOrStdout()
				return p.JSON(final)
			}
			if target != "-" {
				if already {
					fmt.Fprintf(cmd.OutOrStdout(), "Already downloaded %s.\n", target)
				} else {
					fmt.Fprintf(cmd.OutOrStdout(), "Downloaded %s.\n", target)
				}
				if hint := unpackHint(target); hint != "" {
					fmt.Fprintln(cmd.OutOrStdout(), hint)
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "", "write to this file (default: the server file name; - for stdout)")
	cmd.Flags().BoolVar(&latest, "latest", false, "download the newest backup of the named database or volume")
	cmd.Flags().StringVar(&kind, "kind", "", "with --latest: resource kind postgres, mysql or volume (skips the lookup)")
	return cmd
}

// prepareDownload creates (or reuses) the prepared download and polls it until
// it leaves Pending/Running.
func prepareDownload(ctx context.Context, cl *api.ClientWithResponses, project, backupID string, errW io.Writer) (*api.BackupDownload, error) {
	dl, err := createDownload(ctx, cl, project, backupID)
	if err != nil {
		return nil, err
	}
	return awaitDownload(ctx, cl, project, backupID, dl, errW)
}

func createDownload(ctx context.Context, cl *api.ClientWithResponses, project, backupID string) (*api.BackupDownload, error) {
	resp, err := cl.CreateBackupDownloadWithResponse(ctx, project, backupID)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode() == http.StatusNotFound {
		return nil, userErr("No backup %s. List them with: hostim backups ls <resource>", backupID)
	}
	if err := checkResp(resp.StatusCode(), resp.Body); err != nil {
		return nil, err
	}
	out := resp.JSON200
	if resp.JSON201 != nil {
		out = resp.JSON201
	}
	if out == nil {
		return nil, client.ErrEmptyResponse
	}
	return out, nil
}

// awaitDownload polls until the download succeeds, fails, or the wait is
// interrupted. One progress line goes to stderr: overwritten in place on a
// terminal, and repeated every downloadProgressInterval otherwise.
func awaitDownload(ctx context.Context, cl *api.ClientWithResponses, project, backupID string, dl *api.BackupDownload, errW io.Writer) (*api.BackupDownload, error) {
	term := isTerminalWriter(errW)
	start := time.Now()
	var last time.Time
	for {
		switch dl.Phase {
		case "", "Pending", "Running":
			// keep waiting
		case "Failed":
			msg := str(dl.Error)
			if msg == "" {
				msg = "the download failed"
			}
			return nil, errors.New(msg)
		default:
			if term {
				fmt.Fprintln(errW)
			}
			return dl, nil
		}

		elapsed := time.Since(start).Truncate(time.Second)
		if term {
			fmt.Fprintf(errW, "\rPreparing %s… (%s)", dl.FileName, elapsed)
		} else if last.IsZero() || time.Since(last) >= downloadProgressInterval {
			fmt.Fprintf(errW, "Preparing %s… (%s)\n", dl.FileName, elapsed)
			last = time.Now()
		}

		select {
		case <-ctx.Done():
			if term {
				fmt.Fprintln(errW)
			}
			return nil, errDownloadInterrupted
		case <-time.After(backupPollInterval):
		}

		resp, err := cl.GetBackupDownloadWithResponse(ctx, project, backupID)
		if err != nil {
			return nil, err
		}
		if err := checkResp(resp.StatusCode(), resp.Body); err != nil {
			return nil, err
		}
		if resp.JSON200 == nil {
			return nil, client.ErrEmptyResponse
		}
		dl = resp.JSON200
	}
}

// fetchToFile downloads the prepared file to target, resuming an interrupted
// transfer and retrying network and 5xx failures. A nil refresh means an 403
// simply fails.
func fetchToFile(ctx context.Context, dl *api.BackupDownload, target string, stdout, errW io.Writer, refresh func() (*api.BackupDownload, error)) (*api.BackupDownload, error) {
	if target == "-" {
		if err := streamToStdout(ctx, dl, stdout, errW); err != nil {
			return nil, err
		}
		return dl, nil
	}

	refreshed := false
	for attempt := 0; ; attempt++ {
		if backupComplete(dl, target) {
			return dl, nil
		}

		err := fetchOnce(ctx, dl, target, errW)
		if err == nil {
			if dl.SizeBytes == nil || fileSize(target) == *dl.SizeBytes {
				return dl, nil
			}
			err = fmt.Errorf("download incomplete: got %d of %d bytes", fileSize(target), *dl.SizeBytes)
		}

		var expired errExpiredURL
		if errors.As(err, &expired) && refresh != nil && !refreshed {
			fresh, rerr := refresh()
			if rerr != nil {
				return nil, rerr
			}
			dl, refreshed = fresh, true
			continue
		}
		var perm permanentDownloadError
		if errors.As(err, &perm) {
			return nil, perm.err
		}
		if attempt >= len(downloadBackoff) {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, errDownloadInterrupted
		case <-time.After(downloadBackoff[attempt]):
		}
	}
}

// fetchOnce performs one HTTP GET, resuming from the current file size.
func fetchOnce(ctx context.Context, dl *api.BackupDownload, target string, errW io.Writer) error {
	url := str(dl.Url)
	if url == "" {
		return permanentDownloadError{errors.New("the server did not provide a download URL")}
	}
	var total int64
	if dl.SizeBytes != nil {
		total = *dl.SizeBytes
	}
	offset := fileSize(target)
	if total <= 0 || offset > total {
		offset = 0 // unknown total, or a stale file larger than the backup: start over
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return permanentDownloadError{err}
	}
	flags := os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	if offset > 0 {
		flags = os.O_CREATE | os.O_WRONLY | os.O_APPEND
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	f, err := os.OpenFile(target, flags, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusPartialContent:
	case http.StatusOK:
		// The server ignored the Range header: restart from the beginning.
		if offset > 0 {
			if err := f.Truncate(0); err != nil {
				return err
			}
			if _, err := f.Seek(0, io.SeekStart); err != nil {
				return err
			}
			offset = 0
		}
	case http.StatusForbidden:
		return errExpiredURL{}
	default:
		if resp.StatusCode >= 500 {
			return fmt.Errorf("download failed: %s", resp.Status)
		}
		return permanentDownloadError{fmt.Errorf("download failed: %s", resp.Status)}
	}

	pw := &progressWriter{w: f, errW: errW, total: total, done: offset, term: isTerminalWriter(errW)}
	if _, err := io.Copy(pw, resp.Body); err != nil {
		return err
	}
	pw.finish()
	return nil
}

// streamToStdout writes the file to stdout with no resume.
func streamToStdout(ctx context.Context, dl *api.BackupDownload, stdout, errW io.Writer) error {
	url := str(dl.Url)
	if url == "" {
		return errors.New("the server did not provide a download URL")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return fmt.Errorf("download failed: %s", resp.Status)
	}
	var total int64
	if dl.SizeBytes != nil {
		total = *dl.SizeBytes
	}
	pw := &progressWriter{w: stdout, errW: errW, total: total, term: isTerminalWriter(errW)}
	if _, err := io.Copy(pw, resp.Body); err != nil {
		return err
	}
	pw.finish()
	return nil
}

// progressWriter reports transferred bytes to stderr while writing the file.
type progressWriter struct {
	w     io.Writer
	errW  io.Writer
	total int64
	done  int64
	term  bool
	last  time.Time
	// reported is the byte count of the last line, so finish does not repeat it.
	reported int64
}

func (p *progressWriter) Write(b []byte) (int, error) {
	n, err := p.w.Write(b)
	p.done += int64(n)
	now := time.Now()
	switch {
	case p.term && (p.last.IsZero() || now.Sub(p.last) >= 200*time.Millisecond || err != nil):
		p.report("\r", now)
	case !p.term && (p.last.IsZero() || now.Sub(p.last) >= downloadProgressInterval):
		p.report("", now)
	}
	return n, err
}

// finish ends the progress output: a newline after the overwritten terminal
// line, or a last line at the final count, so a log does not end mid-transfer.
func (p *progressWriter) finish() {
	if p.term {
		fmt.Fprintln(p.errW)
		return
	}
	if p.last.IsZero() || p.reported != p.done {
		p.report("", time.Now())
	}
}

func (p *progressWriter) report(prefix string, now time.Time) {
	p.last = now
	p.reported = p.done
	total := ""
	if p.total > 0 {
		total = fmt.Sprintf(" / %s (%d%%)", humanBytes(p.total), p.done*100/p.total)
	}
	if prefix == "\r" {
		fmt.Fprintf(p.errW, "\r%s%s", humanBytes(p.done), total)
		return
	}
	fmt.Fprintf(p.errW, "%s%s\n", humanBytes(p.done), total)
}

// unpackHint tells how to unpack a zstd-compressed backup file, or returns ""
// for any other name.
func unpackHint(name string) string {
	switch {
	case strings.HasSuffix(name, ".tar.zst"):
		return "Unpack with: tar --zstd -xf " + name + " (it unpacks into a folder named after the volume)"
	case strings.HasSuffix(name, ".zst"):
		return "Unpack with: zstd -d " + name
	}
	return ""
}

// backupComplete reports whether target already holds the whole backup.
func backupComplete(dl *api.BackupDownload, target string) bool {
	return dl.SizeBytes != nil && fileSize(target) == *dl.SizeBytes
}

// fileSize returns the size of path, or 0 when it does not exist.
func fileSize(path string) int64 {
	fi, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return fi.Size()
}

// isTerminalWriter reports whether w is a character device, so progress can
// overwrite a line in place instead of appending.
func isTerminalWriter(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// backupTime renders a backup timestamp in local time, or "-" when unknown.
// Table headers name the zone with localZone.
func backupTime(t *time.Time) string {
	if t == nil || t.IsZero() {
		return "-"
	}
	return t.Local().Format("2006-01-02 15:04")
}

// localZone is the abbreviation of the local time zone, such as CEST.
func localZone() string {
	return time.Now().Format("MST")
}

// scheduleLine renders the backup schedule. A daily cron reads as local and
// UTC time; any other schedule is shown as its raw UTC cron line.
func scheduleLine(schedule string, next *time.Time) string {
	line := fmt.Sprintf("Schedule: %s (UTC).", schedule)
	if f := strings.Fields(schedule); len(f) == 5 && f[2] == "*" && f[3] == "*" && f[4] == "*" {
		m, errM := strconv.Atoi(f[0])
		h, errH := strconv.Atoi(f[1])
		if errM == nil && errH == nil {
			now := time.Now().UTC()
			at := time.Date(now.Year(), now.Month(), now.Day(), h, m, 0, 0, time.UTC)
			line = fmt.Sprintf("Backed up daily at %s (%s UTC).", at.Local().Format("15:04 MST"), at.Format("15:04"))
		}
	}
	if next != nil {
		line += " Next run: " + next.Local().Format("2006-01-02 15:04 MST") + "."
	}
	return line
}

// retentionLine renders the restic forget policy in the console's words, or ""
// when there is none.
func retentionLine(r api.BackupRetention) string {
	if r.KeepDaily != nil && r.KeepLast == nil && r.KeepWeekly == nil && r.KeepMonthly == nil {
		return fmt.Sprintf("We keep %d days.", *r.KeepDaily)
	}
	var parts []string
	add := func(label string, v *int) {
		if v != nil {
			parts = append(parts, fmt.Sprintf("%s %d", label, *v))
		}
	}
	add("last", r.KeepLast)
	add("daily", r.KeepDaily)
	add("weekly", r.KeepWeekly)
	add("monthly", r.KeepMonthly)
	if len(parts) == 0 {
		return ""
	}
	return "We keep: " + strings.Join(parts, ", ") + "."
}

// humanBytes renders a byte count with a binary unit.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%cB", float64(n)/float64(div), "KMGTPE"[exp])
}
