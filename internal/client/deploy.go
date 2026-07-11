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

// ErrDeployFailed is returned by PollBuild when a build-less (docker image)
// deploy cannot become healthy, e.g. the image can't be pulled.
var ErrDeployFailed = errors.New("deploy failed")

// PollBuild polls an app's status until it reaches a terminal state or the
// context is cancelled. onTick, if non-nil, is called with each observed status
// so callers can render progress.
//
// expectBuild selects what "terminal" means:
//   - true (git source): wait for buildStatus to reach succeeded/failed. A git
//     deploy always runs a build, so the build outcome is the meaningful signal.
//   - false (docker image source): there is no build phase, so buildStatus stays
//     empty and the app goes straight to running. Wait for runtimeStatus instead:
//     running is success, imagePullBackoff is a definitive failure. Transient
//     states (pending/crashing) keep polling until running or timeout.
//
// It returns ErrBuildFailed / ErrDeployFailed (wrapped) on failure so callers
// can exit non-zero.
func PollBuild(
	ctx context.Context,
	c *api.ClientWithResponses,
	project, app string,
	interval time.Duration,
	expectBuild bool,
	onTick func(*api.AppStatus),
) (BuildResult, error) {
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
			if onTick != nil {
				onTick(st)
			}
			if expectBuild {
				if st.BuildStatus != nil {
					switch *st.BuildStatus {
					case api.AppStatusBuildStatusSucceeded:
						return result(st), nil
					case api.AppStatusBuildStatusFailed:
						return result(st), ErrBuildFailed
					}
				}
			} else if st.RuntimeStatus != nil {
				switch *st.RuntimeStatus {
				case api.AppStatusRuntimeStatusRunning:
					return result(st), nil
				case api.AppStatusRuntimeStatusImagePullBackoff:
					return result(st), ErrDeployFailed
				}
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
