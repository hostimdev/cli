// Package client builds an authenticated Hostim API client from resolved config
// and maps API error responses (including 429 rate limiting) into Go errors.
package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/hostimdev/cli/api"
	"github.com/hostimdev/cli/internal/config"
)

// New returns a ClientWithResponses that injects the Bearer token and
// transparently retries on 429 responses, honouring Retry-After.
func New(r config.Resolved) (*api.ClientWithResponses, error) {
	if r.Token == "" {
		return nil, config.ErrNoToken
	}
	httpClient := &http.Client{
		Timeout: 60 * time.Second,
		Transport: &retryTransport{
			base:       http.DefaultTransport,
			maxRetries: 4,
			maxBackoff: 30 * time.Second,
		},
	}
	auth := api.WithRequestEditorFn(func(_ context.Context, req *http.Request) error {
		req.Header.Set("Authorization", "Bearer "+r.Token)
		return nil
	})
	return api.NewClientWithResponses(r.APIURL, api.WithHTTPClient(httpClient), auth)
}

// APIError is a structured error built from a non-2xx API response.
type APIError struct {
	Status  int
	Code    string // GenericMessage.error, when present
	Message string // GenericMessage.message, when present
}

func (e *APIError) Error() string {
	switch {
	case e.Message != "" && e.Code != "":
		return fmt.Sprintf("%s (%s)", e.Message, e.Code)
	case e.Message != "":
		return e.Message
	case e.Code != "":
		return e.Code
	default:
		return fmt.Sprintf("request failed with status %d", e.Status)
	}
}

// Check returns an *APIError if the response status is >= 400, else nil.
// Generated response structs expose StatusCode() and a Body []byte field, so
// callers use client.Check(resp.StatusCode(), resp.Body).
func Check(status int, body []byte) error {
	if status < 400 {
		return nil
	}
	e := &APIError{Status: status}
	var gm api.GenericMessage
	if len(body) > 0 && json.Unmarshal(body, &gm) == nil {
		e.Code = gm.Error
		e.Message = gm.Message
	}
	return e
}

// retryTransport retries idempotent-safe requests on HTTP 429, waiting for the
// Retry-After interval (falling back to exponential backoff). Non-429 responses
// and network errors pass through unchanged.
type retryTransport struct {
	base       http.RoundTripper
	maxRetries int
	maxBackoff time.Duration
}

func (t *retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	backoff := 500 * time.Millisecond
	for attempt := 0; ; attempt++ {
		resp, err := t.base.RoundTrip(req)
		if err != nil || resp.StatusCode != http.StatusTooManyRequests || attempt >= t.maxRetries {
			return resp, err
		}
		wait := retryAfter(resp, backoff)
		if wait > t.maxBackoff {
			wait = t.maxBackoff
		}
		resp.Body.Close()
		select {
		case <-time.After(wait):
		case <-req.Context().Done():
			return nil, req.Context().Err()
		}
		if backoff < t.maxBackoff {
			backoff *= 2
		}
	}
}

// retryAfter parses the Retry-After header (delta-seconds) or falls back to the
// supplied backoff.
func retryAfter(resp *http.Response, fallback time.Duration) time.Duration {
	if v := resp.Header.Get("Retry-After"); v != "" {
		if secs, err := strconv.Atoi(v); err == nil && secs >= 0 {
			return time.Duration(secs) * time.Second
		}
	}
	return fallback
}
