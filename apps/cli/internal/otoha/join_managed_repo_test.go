package otoha

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

func TestTeamJoinDiscoversRepositoryThroughUnifiedOtohaIdentity(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses a POSIX installed executable and CGI Git backend")
	}
	home := filepath.Join(t.TempDir(), "member")
	t.Setenv("OTOHA_HOME", home)
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	projects := t.TempDir()
	bare := filepath.Join(projects, "owner", "private.git")
	seed := filepath.Join(t.TempDir(), "seed")
	joinTestGit(t, "init", "--bare", "--initial-branch=main", bare)
	joinTestGit(t, "init", "--initial-branch=main", seed)
	if err = os.WriteFile(filepath.Join(seed, "README.md"), []byte("Unified Otoha fixture\n"), 0600); err != nil {
		t.Fatal(err)
	}
	joinTestGit(t, "-C", seed, "add", "README.md")
	joinTestGit(t, "-C", seed, "-c", "user.name=Otoha Test", "-c", "user.email=test@example.invalid", "commit", "-m", "Unified fixture")
	joinTestGit(t, "-C", seed, "push", bare, "main")
	backend := &cgi.Handler{Path: git, Args: []string{"http-backend"}, Dir: projects, Env: []string{"GIT_PROJECT_ROOT=" + projects, "GIT_HTTP_EXPORT_ALL=1"}}
	var credential, bindingName atomic.Value
	credential.Store("")
	bindingName.Store("owner/private")
	var enrollments, gitRequests atomic.Int32
	var authority *httptest.Server
	authority = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/enroll" {
			var input Object
			json.NewDecoder(r.Body).Decode(&input)
			credential.Store(S(input, "credential"))
			enrollments.Add(1)
			json.NewEncoder(w).Encode(Object{"device": "fixture-mac"})
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+credential.Load().(string) {
			http.Error(w, "Otoha identity required", http.StatusUnauthorized)
			return
		}
		switch {
		case r.URL.Path == "/v1/manifest":
			json.NewEncoder(w).Encode(Object{"actor": Object{"id": "test-member"}, "grants": []Object{{"project": "test-team", "role": "member"}}})
		case r.URL.Path == "/v1/repository":
			if r.URL.Query().Get("project") != "test-team" {
				http.NotFound(w, r)
				return
			}
			repository := bindingName.Load().(string)
			json.NewEncoder(w).Encode(Object{"transport": "otoha", "repository": repository, "url": authority.URL + "/v1/git/test-team/" + repository + ".git", "access": "member"})
		case strings.HasPrefix(r.URL.Path, "/v1/git/test-team/"):
			gitRequests.Add(1)
			copy := r.Clone(r.Context())
			requestURL := *r.URL
			requestURL.Path = strings.TrimPrefix(r.URL.Path, "/v1/git/test-team")
			copy.URL = &requestURL
			backend.ServeHTTP(w, copy)
		default:
			http.NotFound(w, r)
		}
	}))
	defer authority.Close()
	m := newPageManager(home)
	m.token = "synthetic-local-runtime-token"
	local := httptest.NewServer(http.HandlerFunc(m.serveHTTP))
	defer local.Close()
	m.baseURL = local.URL
	if err = Save(filepath.Join(home, "page-runtime.json"), Object{"url": local.URL, "token": m.token}); err != nil {
		t.Fatal(err)
	}
	executable, _ := os.Executable()
	t.Setenv("OTOHA_TEST_CREDENTIAL_BINARY", executable)
	t.Setenv("OTOHA_TEST_CREDENTIAL_CHILD", "1")
	helper := filepath.Join(t.TempDir(), "installed-otoha")
	if err = os.WriteFile(helper, []byte("#!/bin/sh\nexec \"$OTOHA_TEST_CREDENTIAL_BINARY\" -test.run='^TestGiteaCredentialProcess$' -- \"$@\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	previousInstalled, previousEnsure := installedJoinExecutable, ensureJoinedPageService
	t.Cleanup(func() { installedJoinExecutable, ensureJoinedPageService = previousInstalled, previousEnsure })
	installedJoinExecutable = func() (string, bool, error) { return helper, false, nil }
	ensureJoinedPageService = func(context.Context, string) (Object, error) {
		cfg, err := loadTeamConfig("test-team")
		if err != nil || S(cfg.Repository, "repository") != "owner/private" || gitRequests.Load() != 0 && cfg.Checkout == "" {
			t.Fatalf("local proxy started without its durable authority binding: %#v %v", cfg, err)
		}
		return Object{"ready": true, "local_ready": true, "connection": "online", "local_url": local.URL}, nil
	}
	invitation := filepath.Join(t.TempDir(), "short-invitation.json")
	if err = Save(invitation, Object{"protocol": "otoha-collaboration/1", "url": authority.URL, "actor": "test-member", "project": "test-team", "channel": "test-team-general", "invitation": "synthetic-short-invite"}); err != nil {
		t.Fatal(err)
	}
	checkout := filepath.Join(t.TempDir(), "checkout")
	result, err := TeamCommand(context.Background(), []string{"join", "--invite", invitation, "--directory", checkout})
	if err != nil || !B(result, "ready") {
		t.Fatalf("one-invitation join failed: %#v %v", result, err)
	}
	if S(result, "authority_url") != authority.URL || S(result, "origin_url") != local.URL+"/git/test-team/owner/private.git" || S(O(result, "repository"), "url") != "" || S(O(result, "repository"), "authority_url") == "" || !strings.Contains(S(O(result, "commands"), "git_remote"), "ls-remote origin HEAD") {
		t.Fatalf("join did not distinguish native Git from its internal authority: %#v", result)
	}
	cfg, err := loadTeamConfig("test-team")
	expectedOrigin := local.URL + "/git/test-team/owner/private.git"
	if err != nil || S(cfg.Repository, "transport") != "otoha" || cfg.RepositoryURL != expectedOrigin || cfg.Checkout != checkout || cfg.GiteaProfile != "" {
		t.Fatalf("managed repository metadata was not retained: %#v %v", cfg, err)
	}
	if _, err = TeamCommand(context.Background(), []string{"join", "--invite", invitation}); err != nil || enrollments.Load() != 1 {
		t.Fatalf("repeat invitation did not reuse identity and checkout: %v enrollments=%d", err, enrollments.Load())
	}
	// The repository helper pins this Home even if a subsequent shell has
	// another Otoha profile selected.
	fetch := exec.Command("git", "-C", checkout, "fetch", "origin")
	fetch.Env = append(os.Environ(), "OTOHA_HOME="+filepath.Join(t.TempDir(), "different-home"))
	if output, err := fetch.CombinedOutput(); err != nil {
		t.Fatalf("repository helper did not retain its Home in another shell: %v %s", err, output)
	}
	if gitRequests.Load() < 3 {
		t.Fatal("clone/fetch did not traverse the authenticated Otoha repository channel")
	}
	config, _ := os.ReadFile(filepath.Join(checkout, ".git", "config"))
	if strings.Contains(string(config), credential.Load().(string)) || strings.Contains(string(config), m.token) || !strings.Contains(string(config), "OTOHA_HOME=") {
		t.Fatal("repository configuration leaked a token or lost its local Home binding")
	}
	bindingName.Store("owner/another")
	if _, err = TeamCommand(context.Background(), []string{"join", "--invite", invitation}); err == nil {
		t.Fatal("repeat join silently changed the repository authority binding")
	}
	after, _ := loadTeamConfig("test-team")
	if S(after.Repository, "repository") != "owner/private" || after.RepositoryURL != expectedOrigin || joinTestGit(t, "-C", checkout, "remote", "get-url", "origin") != expectedOrigin {
		t.Fatal("binding rejection modified the existing project checkout")
	}
}
