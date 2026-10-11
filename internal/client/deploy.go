package client

import (
	"context"
	"errors"
	"time"

	"github.com/hostimdev/cli/api"
)

// BuildResult is the terminal outcome of a deploy poll.
type BuildResult struct {
	BuildStatus   api.AppStatusBuildStatus
	RuntimeStatus api.AppStatusRuntimeStatus
	Status        *api.AppStatus
}

// ErrBuildFailed is returned by PollBuild when the build reaches "failed".
var ErrBuildFailed = errors.New("build failed")

// ErrDeployFailed is returned by PollBuild when the new rollout cannot become
// healthy: the image can't be pulled, or the app keeps crashing.
var ErrDeployFailed = errors.New("deploy failed")

// Baseline is an existing app's state captured before a deploy changes it. The
// operator acts on a rebuild request asynchronously, so for a few seconds after
// the request the status still describes the previous build and rollout. The
// baseline tells those stale values apart from the ones this deploy produces.
type Baseline struct {
	BuildStatus    api.AppStatusBuildStatus
	LastBuiltAt    *time.Time
	LastDeployedAt *time.Time
	// failedEvents holds the BuildFailed events present before the deploy, when
	// the previous build failed: a build that fails again before any poll sees
	// it running leaves the status unchanged, so only a new event shows it.
	failedEvents map[string]bool
}

// NewBaseline reads app's current status (and, after a failed build, its
// BuildFailed events) for PollBuild.
func NewBaseline(ctx context.Context, c *api.ClientWithResponses, project, app string) (*Baseline, error) {
	resp, err := c.GetAppStatusWithResponse(ctx, project, app)
	if err != nil {
		return nil, err
	}
	if err := Check(resp.StatusCode(), resp.Body); err != nil {
		return nil, err
	}
	b := &Baseline{}
	if st := resp.JSON200; st != nil {
		if st.BuildStatus != nil {
			b.BuildStatus = *st.BuildStatus
		}
		b.LastBuiltAt = st.LastBuiltAt
		b.LastDeployedAt = st.LastDeployedAt
	}
	if b.BuildStatus == api.AppStatusBuildStatusFailed {
		if b.failedEvents, err = buildFailedEvents(ctx, c, project, app); err != nil {
			return nil, err
		}
	}
	return b, nil
}

// buildFailedEvents returns the keys of app's recent BuildFailed events. The
// API stores an event once per (reason, message, timestamp), so the pair of
// timestamp and message identifies it.
func buildFailedEvents(ctx context.Context, c *api.ClientWithResponses, project, app string) (map[string]bool, error) {
	limit := 100
	resp, err := c.GetAppEventsWithResponse(ctx, project, app, &api.GetAppEventsParams{Limit: &limit})
	if err != nil {
		return nil, err
	}
	if err := Check(resp.StatusCode(), resp.Body); err != nil {
		return nil, err
	}
	keys := map[string]bool{}
	if resp.JSON200 != nil {
		for _, e := range *resp.JSON200 {
			if e.Reason == "BuildFailed" {
				keys[e.Timestamp.UTC().Format(time.RFC3339Nano)+"|"+e.Message] = true
			}
		}
	}
	return keys, nil
}

// newBuildFailure reports whether a BuildFailed event exists that was not
// there when the baseline was taken.
func (b *Baseline) newBuildFailure(ctx context.Context, c *api.ClientWithResponses, project, app string) (bool, error) {
	now, err := buildFailedEvents(ctx, c, project, app)
	if err != nil {
		return false, err
	}
	for k := range now {
		if !b.failedEvents[k] {
			return true, nil
		}
	}
	return false, nil
}

// after reports whether t is later than base. Without a base there is nothing
// stale to rule out, so anything counts.
func after(t, base *time.Time) bool {
	return base == nil || (t != nil && t.After(*base))
}

// PollBuild polls an app's status until the deploy reaches a terminal state or
// the context is cancelled. onTick, if non-nil, is called with each observed
// status so callers can render progress.
//
// A git source (expectBuild) first waits for its build: succeeded moves on to
// the rollout, failed returns ErrBuildFailed. Then every deploy waits for the
// new rollout: running is success, imagePullBackoff is a definitive failure,
// and a crash seen on 3 consecutive polls is treated as one too; both return
// ErrDeployFailed. Other transient states (pending) keep polling.
//
// since is the app's state before an update (nil for a create, which has no
// previous build or rollout to mistake for this one). With it set, a succeeded
// build counts only once lastBuiltAt moves past since, a failed one only once it
// is shown to be new, and the runtime status only once lastDeployedAt moves past
// since. lastDeployedAt is the creation time of the Deployment's newest
// ReplicaSet; the operator stamps hostim.dev/restarted-at into the pod template
// on every rebuild, so every deploy creates a new one.
func PollBuild(
	ctx context.Context,
	c *api.ClientWithResponses,
	project, app string,
	interval time.Duration,
	expectBuild bool,
	since *Baseline,
	onTick func(*api.AppStatus),
) (BuildResult, error) {
	if since == nil {
		since = &Baseline{}
	}
	built := !expectBuild
	seenRunning := false
	crashStreak := 0
	for {
		resp, err := c.GetAppStatusWithResponse(ctx, project, app)
		if err != nil {
			return BuildResult{}, err
		}
		if err := Check(resp.StatusCode(), resp.Body); err != nil {
			return BuildResult{}, err
		}
		st := resp.JSON200
		if st != nil {
			// view is st with the values that still describe the previous build
			// or rollout blanked out, so onTick never reports them as this
			// deploy's progress.
			view := *st
			var done bool
			var doneErr error
			if !built && st.BuildStatus != nil {
				switch *st.BuildStatus {
				case api.AppStatusBuildStatusRunning:
					seenRunning = true
				case api.AppStatusBuildStatusSucceeded:
					built = after(st.LastBuiltAt, since.LastBuiltAt)
				case api.AppStatusBuildStatusFailed:
					failed := seenRunning || since.BuildStatus != api.AppStatusBuildStatusFailed
					if !failed {
						if failed, err = since.newBuildFailure(ctx, c, project, app); err != nil {
							return BuildResult{}, err
						}
					}
					done, doneErr = failed, ErrBuildFailed
				}
				if !built && !done && *st.BuildStatus != api.AppStatusBuildStatusRunning {
					view.BuildStatus = nil
				}
			}
			if done || !built || !after(st.LastDeployedAt, since.LastDeployedAt) {
				// Build failed, still building, or the new rollout has not been
				// observed yet: any runtime status belongs to the previous
				// deployment.
				view.RuntimeStatus = nil
				crashStreak = 0
			} else if st.RuntimeStatus != nil {
				switch *st.RuntimeStatus {
				case api.AppStatusRuntimeStatusRunning, api.AppStatusRuntimeStatusStopped:
					// A stopped (scaled to zero) app has nothing to wait for.
					done = true
				case api.AppStatusRuntimeStatusImagePullBackoff:
					done, doneErr = true, ErrDeployFailed
				case api.AppStatusRuntimeStatusCrashing:
					crashStreak++
					if crashStreak >= 3 {
						done, doneErr = true, ErrDeployFailed
					}
				default:
					crashStreak = 0
				}
			} else {
				crashStreak = 0
			}
			if onTick != nil {
				onTick(&view)
			}
			if done {
				return result(st), doneErr
			}
		}
		select {
		case <-ctx.Done():
			return BuildResult{Status: st}, ctx.Err()
		case <-time.After(interval):
		}
	}
}

func result(st *api.AppStatus) BuildResult {
	r := BuildResult{Status: st}
	if st.BuildStatus != nil {
		r.BuildStatus = *st.BuildStatus
	}
	if st.RuntimeStatus != nil {
		r.RuntimeStatus = *st.RuntimeStatus
	}
	return r
}
