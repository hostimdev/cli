package cmd

import (
	"slices"
	"testing"
)

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
