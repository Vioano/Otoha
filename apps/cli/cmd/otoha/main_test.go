package main

import (
	"encoding/json"
	a "github.com/Vioano/Otoha/apps/cli/internal/otoha"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func commandResult(t *testing.T, args ...string) a.Object {
	t.Helper()
	write, err := os.CreateTemp(t.TempDir(), "command-output-")
	if err != nil {
		t.Fatal(err)
	}
	previous := os.Stdout
	defer func() { os.Stdout = previous; write.Close() }()
	os.Stdout = write
	err = run(args)
	os.Stdout = previous
	data, readErr := os.ReadFile(write.Name())
	if err != nil || readErr != nil {
		t.Fatalf("command %v failed: %v %v", args, err, readErr)
	}
	var result a.Object
	if err = json.Unmarshal(data, &result); err != nil {
		t.Fatalf("command %v did not return JSON: %v %s", args, err, data)
	}
	return result
}

func TestNewMemberHelpAndEmptyStatusAreDiscoverable(t *testing.T) {
	t.Setenv("OTOHA_HOME", t.TempDir())
	for _, args := range [][]string{{"join", "--help"}, {"team", "help"}, {"team", "--help"}, {"team", "join", "--help"}} {
		result := commandResult(t, args...)
		encoded, _ := json.Marshal(result)
		if !strings.Contains(string(encoded), "--invite") {
			t.Fatalf("help %v hid the primary invite entry: %s", args, encoded)
		}
	}
	status := commandResult(t, "status")
	if a.S(status, "state") != "not_joined" || a.S(status, "next") != "otoha join --invite FILE" {
		t.Fatalf("empty status required legacy setup: %#v", status)
	}
}

func TestTelegramHelpNeedsNoTokenAndDoesNotCreateState(t *testing.T) {
	home := filepath.Join(t.TempDir(), "not-created")
	t.Setenv("OTOHA_HOME", home)
	for _, args := range [][]string{{"telegram"}, {"telegram", "help"}, {"telegram", "--help"}, {"telegram", "-h"}, {"telegram", "send", "--token-file", "/nonexistent/private-token", "--help"}} {
		result := commandResult(t, args...)
		encoded, _ := json.Marshal(result)
		for _, word := range []string{"status", "send", "receive", "bridge", "--config", "--token-file"} {
			if !strings.Contains(string(encoded), word) {
				t.Fatalf("Telegram help omitted %q: %s", word, encoded)
			}
		}
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatal("Telegram help read/created private runtime state")
	}
}

func TestStatusKeepsLegacyOnlyFieldsAndPrefersJoinedTeams(t *testing.T) {
	t.Setenv("OTOHA_HOME", t.TempDir())
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Write([]byte(`{"actor":{"id":"legacy-member"},"grants":[]}`))
	}))
	defer server.Close()
	profile := filepath.Join(a.Home(), "legacy-identity.json")
	if err := a.Save(profile, a.Object{"url": server.URL, "actor": "legacy-member", "token": "synthetic-legacy-token"}); err != nil {
		t.Fatal(err)
	}
	if err := a.Save(filepath.Join(a.Home(), "config.json"), a.Object{"profile": profile}); err != nil {
		t.Fatal(err)
	}
	legacy := commandResult(t, "status")
	if a.S(a.O(legacy, "actor"), "id") != "legacy-member" || a.S(legacy, "legacy_identity") != profile || calls.Load() != 1 {
		t.Fatalf("legacy-only status lost existing fields: %#v calls=%d", legacy, calls.Load())
	}
	if err := a.Save(filepath.Join(a.Home(), "teams", "sample", "team.json"), a.Object{"name": "sample", "mode": "member", "url": server.URL, "project": "sample", "channel": "sample-general", "profile": profile}); err != nil {
		t.Fatal(err)
	}
	joined := commandResult(t, "status")
	if a.S(joined, "state") != "joined" || len(a.Array(joined["teams"])) != 1 || calls.Load() != 1 {
		t.Fatalf("joined-team status used the unrelated legacy network path: %#v calls=%d", joined, calls.Load())
	}
}

func TestInvitationUsesRunningReleaseAndKeepsTwoLines(t *testing.T) {
	previous := version
	version = "9.8.7"
	t.Cleanup(func() { version = previous })
	for _, endpoint := range []string{"", "https://verified.example"} {
		key, err := a.Pack(a.Object{"actor": "fixture", "url": "https://verified.example/otoha-api", "https_endpoint": endpoint})
		if err != nil {
			t.Fatal(err)
		}
		for _, platform := range []string{"", "windows"} {
			text := invitationText(key, platform)
			if endpoint == "" && (!strings.Contains(text, "githubusercontent.com/Vioano/Otoha/") && !strings.Contains(text, "github.com/Vioano/Otoha/releases/")) {
				t.Fatalf("invitation does not target the Otoha monorepo: %q", text)
			}
			if strings.Contains(text, "/apps/cli/releases") {
				t.Fatal("release URL incorrectly uses a repository subdirectory")
			}
			if len(strings.Split(strings.TrimSpace(text), "\n")) != 2 || !strings.Contains(text, "join "+key+" --agent current") {
				t.Fatalf("%q invitation does not contain the original key and two-line flow", platform)
			}
			if platform != "windows" || endpoint == "" {
				if !strings.Contains(text, "/v9.8.7/") && !strings.Contains(text, "/tag/v9.8.7 ") {
					t.Fatalf("%q invitation does not install the running published release", platform)
				}
			} else if !strings.Contains(text, endpoint+"/otoha/v9.8.7/install.ps1") || !strings.Contains(text, "OTOHA_DOWNLOAD_BASE='"+endpoint+"/otoha/v9.8.7'") || !strings.Contains(text, "--https "+endpoint) {
				t.Fatal("Windows invitation lost the existing verified HTTPS entry")
			}
		}
	}
	version = "dev"
	key, _ := a.Pack(a.Object{"actor": "fixture", "url": "https://verified.example/otoha-api"})
	if strings.Contains(invitationText(key, "windows"), "/vdev/") || strings.Contains(invitationText(key, ""), "/tag/vdev") {
		t.Fatal("development build emitted a nonexistent public release tag")
	}
}
