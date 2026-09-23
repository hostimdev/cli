package cmd

import (
	"slices"
	"strings"
	"testing"

	"github.com/hostimdev/cli/api"
)

func TestPgImportSSHArgs(t *testing.T) {
	cr := &api.PostgresCredentials{Hostname: "main.hpr-1.svc", Port: "5432", Username: "u", Database: "app", Password: "s3cret"}
	got := pgImportSSHArgs("host:2222", "hpr-1", "/k/id", cr)
	want := "-i /k/id -p 2222 -T -l hpr-1 host " +
		"IFS= read -r PGPASSWORD && export PGPASSWORD && exec psql -X -q -h main.hpr-1.svc -p 5432 -U u -d app"
	if strings.Join(got, " ") != want {
		t.Errorf("args = %q\nwant %q", strings.Join(got, " "), want)
	}
	// The password travels on stdin, never in argv.
	if strings.Contains(strings.Join(got, " "), cr.Password) {
		t.Errorf("password leaked into argv: %q", got)
	}
}

func TestErrorCounter(t *testing.T) {
	var out strings.Builder
	e := &errorCounter{w: &out}
	_, _ = e.Write([]byte("ERROR:  permission denied to create event trigger\n"))
	_, _ = e.Write([]byte("NOTICE:  x\nERROR:  must be owner of extension pg_stat_statements\n"))
	if e.n != 2 || !strings.Contains(out.String(), "NOTICE") {
		t.Errorf("n = %d, out = %q", e.n, out.String())
	}
}

func TestUnion(t *testing.T) {
	// adding keeps what is there and ignores duplicates
	got := union([]string{"postgis", "vector"}, []string{"vector", "citext"})
	want := []string{"postgis", "vector", "citext"}
	if !slices.Equal(got, want) {
		t.Errorf("union = %v, want %v", got, want)
	}

	// the current list is never mutated: it comes from the fetched database
	current := []string{"postgis"}
	union(current, []string{"citext"})
	if !slices.Equal(current, []string{"postgis"}) {
		t.Errorf("union mutated its input: %v", current)
	}
}
