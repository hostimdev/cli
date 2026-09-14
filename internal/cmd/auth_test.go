package cmd

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/hostimdev/cli/internal/config"
	"github.com/hostimdev/cli/internal/output"
)

// deviceServer answers the device-login endpoints, approving immediately, plus
// the projects lookup saveValidatedToken uses to check the token.
func deviceServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/users/device/authorize":
			_, _ = w.Write([]byte(`{"deviceCode":"dc-1","userCode":"ABCD-1234","verificationUri":"https://console.hostim.dev/device","expiresIn":60,"interval":1}`))
		case "/api/users/device/token":
			_, _ = w.Write([]byte(`{"status":"approved","token":"hst_device_test"}`))
		case "/api/projects":
			_, _ = w.Write([]byte(`[]`))
		default:
			t.Errorf("unexpected request to %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestDeviceLoginSavesToken(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	srv := deviceServer(t)
	defer srv.Close()

	c := &cli{
		resolved: config.Resolved{APIURL: srv.URL},
		printer:  output.Printer{Out: &bytes.Buffer{}},
	}
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)

	if err := c.deviceLogin(cmd); err != nil {
		t.Fatalf("deviceLogin: %v", err)
	}

	if !strings.Contains(stderr.String(), "ABCD-1234") {
		t.Errorf("the code was not shown to the user, stderr=%q", stderr.String())
	}

	f, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if f.Token != "hst_device_test" {
		t.Errorf("token not saved, got %q", f.Token)
	}
}
