package client

import (
	"context"
	"errors"
	"time"

	"github.com/hostimdev/cli/api"
)

// BuildResult is the terminal outcome of a build poll.
type BuildResult struct {
	BuildStatus   api.AppStatusBuildStatus
	RuntimeStatus api.AppStatusRuntimeStatus
	Status        *api.AppStatus
}

// ErrBuildFailed is returned by PollBuild when the build reaches "failed".
var ErrBuildFailed = errors.New("build failed")

// PollBuild polls an app's status until buildStatus reaches a terminal state
// (succeeded/failed) or the context is cancelled. onTick, if non-nil, is called
// with each observed status so callers can render progress. It returns
// ErrBuildFailed (wrapped) on a failed build so callers can exit non-zero.
func PollBuild(
	ctx context.Context,
	c *api.ClientWithResponses,
	project, app string,
	interval time.Duration,
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
			if st.BuildStatus != nil {
				switch *st.BuildStatus {
				case api.AppStatusBuildStatusSucceeded:
					return result(st), nil
				case api.AppStatusBuildStatusFailed:
					return result(st), ErrBuildFailed
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
