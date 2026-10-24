package otoha

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectAccessReusesIdentityWithoutEnrollmentOrHostChange(t *testing.T) {
	t.Setenv("OTOHA_HOME", t.TempDir())
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		switch r.URL.Path {
		case "/v1/manifest":
			if r.Header.Get("Authorization") != "Bearer existing-token" {
				t.Error("existing Otoha token was not reused")
			}
			json.NewEncoder(w).Encode(Object{"actor": Object{"id": "alice"}, "grants": []Object{{"project": "new-project", "role": "member"}}})
		case "/api/v1/repos/captain/new-project":
			u, p, ok := r.BasicAuth()
			if !ok || u != "alice" || p != "existing-password" {
				t.Error("existing Git password was not reused")
			}
			json.NewEncoder(w).Encode(Object{"private": true})
		default:
			t.Error("unexpected API call", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	original := filepath.Join(Home(), "original-identity.json")
	p := Object{"actor": "alice", "token": "existing-token", "url": server.URL, "channel": "old-project-general", "gitea": Object{"url": server.URL, "username": "alice", "password": "existing-password", "repository": "captain/old-project"}}
	if err := Save(original, p); err != nil {
		t.Fatal(err)
	}
	if err := Save(filepath.Join(Home(), "config.json"), Object{"mode": "member", "profile": original}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(original)
	inv := Object{"actor": "alice", "protocol": "otoha-access/1", "url": "https://untrusted.invalid", "https_endpoint": "https://untrusted.invalid", "prefer_https": true, "project": "new-project", "channel": "new-project-general", "gitea": Object{"repository": "captain/new-project", "url": "https://untrusted.invalid"}}
	result, err := AttachExisting(context.Background(), inv)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(original)
	if string(before) != string(after) {
		t.Fatal("original identity was changed")
	}
	updated, err := Load(S(result, "profile"))
	if err != nil {
		t.Fatal(err)
	}
	if S(updated, "token") != "existing-token" || S(updated, "url") != server.URL || S(O(updated, "gitea"), "password") != "existing-password" || S(O(updated, "gitea"), "repository") != "captain/new-project" {
		t.Fatal("new project did not preserve existing authentication and hosts")
	}
	if strings.Join(paths, ",") != "/v1/manifest,/api/v1/repos/captain/new-project" || B(result, "enrollment_called") {
		t.Fatal("project attachment unexpectedly enrolled again")
	}
	inv["actor"] = "bob"
	if _, err = AttachExisting(context.Background(), inv); err == nil {
		t.Fatal("another actor's access key was accepted")
	}
}
