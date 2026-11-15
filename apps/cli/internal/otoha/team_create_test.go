package otoha

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type teamCreatePostScenario struct {
	status       int
	commit       bool
	malformed    bool
	wrongProject string
}

type teamCreateFixture struct {
	t             *testing.T
	server        *httptest.Server
	mu            sync.Mutex
	owner         bool
	actor         string
	protocol      string
	projects      map[string]Object
	channels      map[string]Object
	posts         map[string]int
	requests      int
	projectPost   teamCreatePostScenario
	channelPost   teamCreatePostScenario
	profilePath   string
	profileBefore []byte
	sourceBefore  []byte
}

func newTeamCreateFixture(t *testing.T) *teamCreateFixture {
	t.Helper()
	t.Setenv("OTOHA_HOME", t.TempDir())
	f := &teamCreateFixture{t: t, owner: true, actor: "captain", protocol: "otoha-collaboration/1",
		projects: map[string]Object{}, channels: map[string]Object{}, posts: map[string]int{},
		projectPost: teamCreatePostScenario{commit: true}, channelPost: teamCreatePostScenario{commit: true}}
	f.server = httptest.NewTLSServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	f.profilePath = filepath.Join(Home(), "identity", "owner.json")
	profile := Object{"protocol": "otoha-collaboration/1", "url": f.server.URL, "actor": "captain",
		"token": "synthetic-create-owner-token", "mode": "owner",
		"ca_pem": string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: f.server.Certificate().Raw}))}
	if err := Save(f.profilePath, profile); err != nil {
		t.Fatal(err)
	}
	source := TeamConfig{Protocol: "otoha-team/1", Name: "source", Mode: "local", URL: f.server.URL,
		LocalURL: "http://127.0.0.1:9999", Project: "old-project", Channel: "old-channel",
		Profile: f.profilePath, State: "/existing/private/state", ManageCenter: true,
		ServerPID: 4242, ServerLog: "/existing/server.log", ServerScript: "/existing/server.py",
		Python: "/existing/python", Checkout: "/existing/checkout", RepositoryURL: "https://git.example/old.git",
		CreatedAt: "2026-01-02T03:04:05Z"}
	if err := saveTeamConfig(source); err != nil {
		t.Fatal(err)
	}
	f.profileBefore, _ = os.ReadFile(f.profilePath)
	f.sourceBefore, _ = os.ReadFile(teamConfigPath("source"))
	return f
}

func (f *teamCreateFixture) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests++
	w.Header().Set("Content-Type", "application/json")
	if r.Header.Get("Authorization") != "Bearer synthetic-create-owner-token" {
		f.t.Error("source identity authentication was not reused")
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	if r.URL.Path == "/v1/manifest" && r.Method == http.MethodGet {
		json.NewEncoder(w).Encode(Object{"protocol": f.protocol, "actor": Object{"id": f.actor, "owner": f.owner}})
		return
	}
	collection := strings.TrimPrefix(r.URL.Path, "/v1/")
	if collection != "projects" && collection != "channels" {
		f.t.Error("team create used an unrelated endpoint", r.URL.Path)
		http.NotFound(w, r)
		return
	}
	rows := f.projects
	if collection == "channels" {
		rows = f.channels
	}
	if r.Method == http.MethodGet {
		items := []Object{}
		for _, item := range rows {
			items = append(items, item)
		}
		json.NewEncoder(w).Encode(Object{collection: items})
		return
	}
	if r.Method != http.MethodPost {
		f.t.Error("unexpected registration method", r.Method)
		http.Error(w, "wrong method", http.StatusMethodNotAllowed)
		return
	}
	f.posts[collection]++
	var body Object
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		f.t.Error(err)
	}
	scenario := f.projectPost
	if collection == "channels" {
		scenario = f.channelPost
	}
	id := S(body, "id")
	if scenario.commit {
		item := Object{"id": id, "name": S(body, "name")}
		if collection == "channels" {
			item["project"] = S(body, "project")
			if scenario.wrongProject != "" {
				item["project"] = scenario.wrongProject
			}
		}
		rows[id] = item
	}
	if scenario.status != 0 {
		w.WriteHeader(scenario.status)
		json.NewEncoder(w).Encode(Object{"error": "synthetic_post_failure"})
		return
	}
	if scenario.malformed {
		w.Write([]byte("response interrupted before valid JSON"))
		return
	}
	json.NewEncoder(w).Encode(Object{"id": id})
}

func (f *teamCreateFixture) create(extra ...string) (Object, error) {
	args := append([]string{"new-team", "--from-team", "source"}, extra...)
	return teamCreateCommand(context.Background(), args)
}

func (f *teamCreateFixture) assertOriginalBytes(t *testing.T) {
	t.Helper()
	profile, err := os.ReadFile(f.profilePath)
	if err != nil || string(profile) != string(f.profileBefore) {
		t.Fatal("source profile bytes changed", err)
	}
	source, err := os.ReadFile(teamConfigPath("source"))
	if err != nil || string(source) != string(f.sourceBefore) {
		t.Fatal("source team metadata bytes changed", err)
	}
}

func (f *teamCreateFixture) assertNoTarget(t *testing.T) {
	t.Helper()
	if _, err := os.Stat(teamConfigPath("new-team")); !os.IsNotExist(err) {
		t.Fatal("unconfirmed target configuration was saved", err)
	}
}

func TestTeamCreateReusesTLSIdentityWithoutCenterState(t *testing.T) {
	f := newTeamCreateFixture(t)
	result, err := f.create()
	if err != nil {
		t.Fatal(err)
	}
	if !B(result, "created") || !B(result, "registered") || !B(result, "identity_reused") ||
		S(result, "project") != "new-team" || S(result, "channel") != "new-team-general" {
		t.Fatal("registration result is incorrect", result)
	}
	cfg, err := loadTeamConfig("new-team")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Mode != "member" || cfg.State != "ready" || cfg.ManageCenter || cfg.ServerPID != 0 ||
		cfg.LocalURL != "" || cfg.ServerLog != "" || cfg.ServerScript != "" || cfg.Python != "" ||
		cfg.Checkout != "" || cfg.RepositoryURL != "" || cfg.Profile != f.profilePath || !filepath.IsAbs(cfg.Profile) {
		t.Fatal("new alias inherited source management or repository state", cfg)
	}
	raw, _ := json.Marshal(result)
	teamBytes, _ := os.ReadFile(teamConfigPath("new-team"))
	if strings.Contains(string(raw)+string(teamBytes), "synthetic-create-owner-token") {
		t.Fatal("registration output or team metadata contains a credential")
	}
	f.assertOriginalBytes(t)
}

func TestTeamCreateRequiresManifestOwnerActorAndProtocol(t *testing.T) {
	for _, scenario := range []string{"non-owner", "actor-mismatch", "protocol-mismatch"} {
		t.Run(scenario, func(t *testing.T) {
			f := newTeamCreateFixture(t)
			f.mu.Lock()
			switch scenario {
			case "non-owner":
				f.owner = false
			case "actor-mismatch":
				f.actor = "another-owner"
			case "protocol-mismatch":
				f.protocol = "unrelated/1"
			}
			f.mu.Unlock()
			if _, err := f.create(); err == nil {
				t.Fatal("unverified owner was accepted")
			}
			f.mu.Lock()
			writes := f.posts["projects"] + f.posts["channels"]
			f.mu.Unlock()
			if writes != 0 {
				t.Fatal("owner rejection still performed writes")
			}
			f.assertNoTarget(t)
			f.assertOriginalBytes(t)
		})
	}
}

func TestTeamCreateRepeatPreservesTargetAndIdentityBytes(t *testing.T) {
	f := newTeamCreateFixture(t)
	if _, err := f.create(); err != nil {
		t.Fatal(err)
	}
	// A normal later repository binding belongs to this alias and must survive
	// repeated registration, including fields unknown to TeamConfig.
	target, err := Load(teamConfigPath("new-team"))
	if err != nil {
		t.Fatal(err)
	}
	target["checkout"], target["repository_url"] = "/existing/new-checkout", "https://git.example/new.git"
	target["repository"], target["future_option"] = Object{"transport": "otoha"}, "preserve-me"
	target["state"], target["server_pid"] = "existing-state", 4321
	if err := Save(teamConfigPath("new-team"), target); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(teamConfigPath("new-team"))
	result, err := f.create()
	if err != nil || B(result, "created") || !B(result, "registered") {
		t.Fatal("repeat did not reuse registration", result, err)
	}
	after, _ := os.ReadFile(teamConfigPath("new-team"))
	if string(before) != string(after) {
		t.Fatal("repeat create rewrote existing target metadata")
	}
	f.mu.Lock()
	projectPosts, channelPosts := f.posts["projects"], f.posts["channels"]
	f.mu.Unlock()
	if projectPosts != 1 || channelPosts != 1 {
		t.Fatal("repeat issued duplicate registrations", projectPosts, channelPosts)
	}
	f.assertOriginalBytes(t)
}

func TestTeamCreateRejectsAliasBindingConflictsBeforeNetwork(t *testing.T) {
	for _, field := range []string{"url", "profile", "project", "channel"} {
		t.Run(field, func(t *testing.T) {
			f := newTeamCreateFixture(t)
			cfg := Object{"protocol": "otoha-team/1", "name": "new-team", "mode": "member",
				"url": f.server.URL, "profile": f.profilePath, "project": "new-team", "channel": "new-team-general"}
			cfg[field] = "other-binding"
			if err := Save(teamConfigPath("new-team"), cfg); err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(teamConfigPath("new-team"))
			if _, err := f.create(); err == nil {
				t.Fatal("conflicting alias was accepted")
			}
			after, _ := os.ReadFile(teamConfigPath("new-team"))
			f.mu.Lock()
			requests := f.requests
			f.mu.Unlock()
			if requests != 0 || string(before) != string(after) {
				t.Fatal("alias conflict contacted authority or rewrote state", requests)
			}
			f.assertOriginalBytes(t)
		})
	}
}

func TestTeamCreateRejectsCrossProjectChannelBeforeAnyPost(t *testing.T) {
	f := newTeamCreateFixture(t)
	f.channels["new-team-general"] = Object{"id": "new-team-general", "project": "another-project"}
	if _, err := f.create(); err == nil || !strings.Contains(err.Error(), "another project") {
		t.Fatal("cross-project channel collision was not rejected", err)
	}
	f.mu.Lock()
	writes := f.posts["projects"] + f.posts["channels"]
	f.mu.Unlock()
	if writes != 0 {
		t.Fatal("channel conflict left an unnecessary project behind")
	}
	f.assertNoTarget(t)
}

func TestTeamCreateReadsBackConflictAndUnknownPostOutcomes(t *testing.T) {
	for _, scenario := range []string{"conflict-committed", "invalid-response-committed", "server-error-committed", "conflict-absent", "success-absent", "wrong-project", "conflict-wrong-project", "definite-rejection"} {
		t.Run(scenario, func(t *testing.T) {
			f := newTeamCreateFixture(t)
			f.mu.Lock()
			success := true
			switch scenario {
			case "conflict-committed":
				f.projectPost.status = http.StatusConflict
			case "invalid-response-committed":
				f.projectPost.malformed = true
			case "server-error-committed":
				f.channelPost.status = http.StatusServiceUnavailable
			case "conflict-absent":
				f.projectPost = teamCreatePostScenario{status: http.StatusConflict}
				success = false
			case "success-absent":
				f.channelPost.commit = false
				success = false
			case "wrong-project":
				f.channelPost.wrongProject = "another-project"
				success = false
			case "conflict-wrong-project":
				f.channelPost.status, f.channelPost.wrongProject = http.StatusConflict, "another-project"
				success = false
			case "definite-rejection":
				f.channelPost.status = http.StatusForbidden
				success = false
			}
			f.mu.Unlock()
			result, err := f.create()
			if success {
				if err != nil || !B(result, "registered") {
					t.Fatal("committed stable IDs were not recovered by readback", result, err)
				}
			} else {
				if err == nil {
					t.Fatal("unconfirmed registration was accepted", result)
				}
				f.assertNoTarget(t)
			}
			f.assertOriginalBytes(t)
		})
	}
}

func TestTeamCreatePartialRegistrationResumesWithoutDeletingProject(t *testing.T) {
	f := newTeamCreateFixture(t)
	f.channelPost = teamCreatePostScenario{status: http.StatusServiceUnavailable}
	if _, err := f.create(); err == nil {
		t.Fatal("failed channel registration was accepted")
	}
	f.assertNoTarget(t)
	f.mu.Lock()
	_, projectRetained := f.projects["new-team"]
	f.channelPost = teamCreatePostScenario{commit: true}
	f.mu.Unlock()
	if !projectRetained {
		t.Fatal("partial failure removed the confirmed project")
	}
	if result, err := f.create(); err != nil || !B(result, "registered") {
		t.Fatal("partial registration did not resume", result, err)
	}
	f.mu.Lock()
	projectPosts, channelPosts := f.posts["projects"], f.posts["channels"]
	f.mu.Unlock()
	if projectPosts != 1 || channelPosts != 2 {
		t.Fatal("retry rebuilt project or failed to retry channel", projectPosts, channelPosts)
	}
	f.assertOriginalBytes(t)
}

func TestTeamCreateExplicitProjectChannelAndSameSourceRejection(t *testing.T) {
	f := newTeamCreateFixture(t)
	result, err := f.create("--project", "work-project", "--channel", "work-channel")
	if err != nil || S(result, "project") != "work-project" || S(result, "channel") != "work-channel" {
		t.Fatal("explicit project/channel not retained", result, err)
	}
	if _, err := teamCreateCommand(context.Background(), []string{"source", "--from-team", "source"}); err == nil {
		t.Fatal("same-name target could replace the source configuration")
	}
	f.assertOriginalBytes(t)
}
