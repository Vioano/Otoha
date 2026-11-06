package otoha

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestJoinEndpointAllowsCleanBasePathAndRejectsAmbiguousPaths(t *testing.T) {
	for raw, want := range map[string]string{
		"https://TEAM.example":          "https://team.example",
		"https://TEAM.example/":         "https://team.example",
		"https://TEAM.example/t/route":  "https://team.example/t/route",
		"https://TEAM.example/t/route/": "https://team.example/t/route",
	} {
		got, err := joinEndpoint(raw)
		if err != nil || got != want {
			t.Fatal("valid service base was changed or rejected", raw, got, err)
		}
	}
	for _, raw := range []string{
		"https://team.example/t/../other", "https://team.example/t//route", "https://team.example/t/route//",
		"https://team.example//", "https://team.example/t/route/.", "https://team.example/t/%2e%2e/other",
		"https://team.example/t/route?project=other", "https://team.example/t/route#other", "https://name:secret@team.example/t/route",
	} {
		if _, err := joinEndpoint(raw); err == nil {
			t.Fatal("ambiguous or authority-changing endpoint accepted", raw)
		}
	}
}

func TestJoinKeyRefusesInvitationForDifferentBasePath(t *testing.T) {
	t.Setenv("OTOHA_HOME", filepath.Join(t.TempDir(), "not-created"))
	var calls atomic.Int32
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/t/original/v1/invitation-info" {
			t.Error("lookup lost the requested service base", r.URL.Path)
		}
		json.NewEncoder(w).Encode(Object{"protocol": "otoha-collaboration/1", "url": server.URL + "/t/another", "actor": "member", "project": "sample", "channel": "sample-general", "grants": Object{"sample": "member"}})
	}))
	defer server.Close()
	_, err := resolveJoinKey(context.Background(), server.URL+"/t/original", "synthetic-unused-invitation", joinKeyCA(t, server))
	if err == nil || !strings.Contains(err.Error(), "authority differs") || calls.Load() != 1 {
		t.Fatal("a path-changing invitation was accepted", err, calls.Load())
	}
	if _, err := os.Stat(Home()); !os.IsNotExist(err) {
		t.Fatal("rejected path change created enrollment state", err)
	}
}

func TestLocalCenterProbeUsesLoopbackWithoutChangingPublicProfile(t *testing.T) {
	t.Setenv("OTOHA_HOME", t.TempDir())
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		switch r.URL.Path {
		case "/health":
			json.NewEncoder(w).Encode(Object{"status": "ok"})
		case "/v1/manifest":
			if r.Header.Get("Authorization") != "Bearer synthetic-owner-token" {
				t.Error("probe lost original owner authentication")
			}
			json.NewEncoder(w).Encode(Object{"protocol": "otoha-collaboration/1", "actor": Object{"id": "captain", "owner": true}})
		default:
			t.Error("probe contacted an unexpected local endpoint", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	public := "https://team.example/t/route"
	profile := filepath.Join(Home(), "owner.json")
	if err := Save(profile, Object{"url": public, "actor": "captain", "token": "synthetic-owner-token"}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(profile)
	cfg := TeamConfig{Name: "sample", URL: public, LocalURL: server.URL, Profile: profile}
	reachable, err := probeLocalCenter(context.Background(), cfg)
	if !reachable || err != nil || calls.Load() != 2 {
		t.Fatal("public-route owner could not probe its local authority", reachable, err, calls.Load())
	}
	after, _ := os.ReadFile(profile)
	if string(before) != string(after) || cfg.URL != public {
		t.Fatal("local probe rewrote the saved public authority")
	}
}
