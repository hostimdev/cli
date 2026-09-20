package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hostimdev/cli/internal/config"
)

// Both constructors must stamp the User-Agent: the backend access log uses it
// to tell CLI traffic apart from the console.
func TestUserAgentHeader(t *testing.T) {
	UserAgent = "hostim-cli/test"

	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.UserAgent()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	cl, err := New(config.Resolved{APIURL: srv.URL, Token: "t"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cl.GetProjectsWithResponse(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got != "hostim-cli/test" {
		t.Fatalf("authenticated client User-Agent = %q, want hostim-cli/test", got)
	}

	got = ""
	anon, err := NewAnonymous(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := anon.GetProjectsWithResponse(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got != "hostim-cli/test" {
		t.Fatalf("anonymous client User-Agent = %q, want hostim-cli/test", got)
	}
}
