package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestJoinEndpointKeyDispatchesToTeamValidation(t *testing.T) {
	home := filepath.Join(t.TempDir(), "not-created")
	t.Setenv("OTOHA_HOME", home)
	for _, args := range [][]string{
		{"join", "--endpoint", "http://invalid.example", "--key", "synthetic-unused-invitation"},
		{"join", "--endpoint=http://invalid.example", "--key=synthetic-unused-invitation"},
	} {
		err := run(args)
		if err == nil || !strings.Contains(err.Error(), "--endpoint must be an HTTPS origin") {
			t.Fatalf("short-key join did not reach the shared team path: %v", err)
		}
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatal("invalid endpoint created enrollment or runtime state")
	}
}
