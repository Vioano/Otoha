package otoha

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestGiteaCredentialScopeAndReadOnlyOperations(t *testing.T) {
	profilePath := filepath.Join(t.TempDir(), "existing-profile.json")
	if err := Save(profilePath, Object{"url": "https://gitea.example.invalid", "username": "synthetic-member", "token": "synthetic-test-token", "password": "unused-test-password"}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(profilePath)
	for _, tc := range []struct {
		name, input string
		want        bool
	}{
		{"matching", "protocol=https\nhost=gitea.example.invalid\npath=owner/private.git\n\n", true},
		{"default-port", "protocol=https\nhost=gitea.example.invalid:443\npath=owner/private.git\n\n", true},
		{"other-host", "protocol=https\nhost=elsewhere.example.invalid\npath=owner/private.git\n\n", false},
		{"other-protocol", "protocol=http\nhost=gitea.example.invalid\npath=owner/private.git\n\n", false},
		{"other-repository", "protocol=https\nhost=gitea.example.invalid\npath=owner/another.git\n\n", false},
		{"outside-repository", "protocol=https\nhost=gitea.example.invalid\npath=owner/private.git/other\n\n", false},
		{"no-repository", "protocol=https\nhost=gitea.example.invalid\n\n", false},
		{"other-username", "protocol=https\nhost=gitea.example.invalid\npath=owner/private.git\nusername=another-member\n\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			err := GiteaCredential([]string{"--profile", profilePath, "--repository", "owner/private", "get"}, strings.NewReader(tc.input), &output)
			if err != nil {
				t.Fatal(err)
			}
			if tc.want && output.String() != "username=synthetic-member\npassword=synthetic-test-token\n\n" {
				t.Fatal("matching request did not receive the existing token")
			}
			if !tc.want && output.Len() != 0 {
				t.Fatal("credentials escaped the selected origin/repository")
			}
		})
	}
	for _, operation := range []string{"store", "erase"} {
		var output bytes.Buffer
		if err := GiteaCredential([]string{"--profile", "/nonexistent/read-only-profile", "--repository", "owner/private", operation}, strings.NewReader("password=do-not-store\n\n"), &output); err != nil || output.Len() != 0 {
			t.Fatalf("%s was not a read-only no-op: %v", operation, err)
		}
	}
	if _, _, err := joinedGiteaProfile(profilePath, "https://elsewhere.example.invalid/owner/private.git"); err == nil {
		t.Fatal("invitation selected a different credential origin")
	}
	after, _ := os.ReadFile(profilePath)
	if !bytes.Equal(before, after) {
		t.Fatal("credential helper changed the original profile")
	}
}

// The synthetic installed executable below runs this helper in a child process,
// exercising the same Go credential implementation through Git's real protocol.
func TestGiteaCredentialProcess(t *testing.T) {
	if os.Getenv("OTOHA_TEST_CREDENTIAL_CHILD") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "gitea-credential" {
			if err := GiteaCredential(os.Args[i+1:], os.Stdin, os.Stdout); err != nil {
				fmt.Fprintln(os.Stderr, "synthetic credential helper failed")
				os.Exit(1)
			}
			os.Exit(0)
		}
		if arg == "page-git-credential" {
			if err := PageGitCredential(os.Args[i+1:], os.Stdin, os.Stdout); err != nil {
				fmt.Fprintln(os.Stderr, "synthetic local credential helper failed")
				os.Exit(1)
			}
			os.Exit(0)
		}
	}
	os.Exit(2)
}

func TestTeamJoinPrivateHTTPRepositoryUsesOnlySelectedProfile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses a POSIX credential executable and CGI Git backend")
	}
	t.Setenv("OTOHA_HOME", filepath.Join(t.TempDir(), "member"))
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	projects := t.TempDir()
	bare := filepath.Join(projects, "owner", "private.git")
	seed := filepath.Join(t.TempDir(), "seed")
	joinTestGit(t, "init", "--bare", "--initial-branch=main", bare)
	joinTestGit(t, "init", "--initial-branch=main", seed)
	if err = os.WriteFile(filepath.Join(seed, "README.md"), []byte("Synthetic private project\n"), 0600); err != nil {
		t.Fatal(err)
	}
	joinTestGit(t, "-C", seed, "add", "README.md")
	joinTestGit(t, "-C", seed, "-c", "user.name=Otoha Test", "-c", "user.email=test@example.invalid", "commit", "-m", "Private fixture")
	joinTestGit(t, "-C", seed, "push", bare, "main")
	backend := &cgi.Handler{Path: git, Args: []string{"http-backend"}, Dir: projects, Env: []string{"GIT_PROJECT_ROOT=" + projects, "GIT_HTTP_EXPORT_ALL=1"}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username, password, ok := r.BasicAuth()
		if !ok || username != "synthetic-member" || password != "synthetic-test-token" {
			w.Header().Set("WWW-Authenticate", `Basic realm="synthetic-private-project"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		backend.ServeHTTP(w, r)
	}))
	defer server.Close()
	profilePath := filepath.Join(t.TempDir(), "original-profile.json")
	if err = Save(profilePath, Object{"url": server.URL, "username": "synthetic-member", "token": "synthetic-test-token", "password": "unused-test-password"}); err != nil {
		t.Fatal(err)
	}
	profileBefore, _ := os.ReadFile(profilePath)
	globalConfig := filepath.Join(t.TempDir(), "original-gitconfig")
	globalMarker := filepath.Join(t.TempDir(), "global-helper-was-called")
	if err = os.WriteFile(globalConfig, []byte("[credential]\n\thelper = !touch "+shellQuote(globalMarker)+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", globalConfig)
	globalBefore, _ := os.ReadFile(globalConfig)
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
		return Object{"ready": true, "local_ready": true, "connection": "online"}, nil
	}
	invitation, _ := joinTestInvitation(t, Object{"url": server.URL + "/owner/private.git", "branch": "main"})
	checkout := filepath.Join(t.TempDir(), "checkout")
	result, err := TeamCommand(context.Background(), []string{"join", "--invite", invitation, "--directory", checkout, "--gitea-profile", profilePath})
	if err != nil || !B(result, "ready") {
		t.Fatalf("private HTTP join failed: %#v %v", result, err)
	}
	cfg, err := loadTeamConfig("test-team")
	if err != nil || cfg.GiteaProfile != profilePath {
		t.Fatalf("local profile locator was not retained: %#v %v", cfg, err)
	}
	if _, err = TeamCommand(context.Background(), []string{"join", "--invite", invitation, "--directory", checkout}); err != nil {
		t.Fatalf("repeat join did not reuse the profile locator: %v", err)
	}
	joinTestGit(t, "-C", checkout, "fetch", "origin")
	profileAfter, _ := os.ReadFile(profilePath)
	globalAfter, _ := os.ReadFile(globalConfig)
	config, _ := os.ReadFile(filepath.Join(checkout, ".git", "config"))
	if !bytes.Equal(profileBefore, profileAfter) || !bytes.Equal(globalBefore, globalAfter) || bytes.Contains(config, []byte("synthetic-test-token")) {
		t.Fatal("join changed original credentials/global config or saved a token in repository configuration")
	}
	if _, err = os.Stat(globalMarker); !os.IsNotExist(err) {
		t.Fatal("repository authentication called a global credential helper")
	}
}
