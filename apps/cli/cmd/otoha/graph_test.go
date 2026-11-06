package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGraphHelpIsDiscoverableWithoutIdentityOrRuntime(t *testing.T) {
	home := filepath.Join(t.TempDir(), "not-created")
	t.Setenv("OTOHA_HOME", home)
	for _, args := range [][]string{{"graph"}, {"graph", "help"}, {"graph", "--help"}, {"graph", "mutate", "--help"}} {
		result := commandResult(t, args...)
		data, _ := json.Marshal(result)
		for _, word := range []string{"manifest", "query", "mutate", "result", "request_id", "expected_revision"} {
			if !strings.Contains(string(data), word) {
				t.Fatalf("graph help hid %q: %s", word, data)
			}
		}
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatal("graph help created private state")
	}
}
