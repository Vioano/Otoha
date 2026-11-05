package otoha

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func pageTestRepository(t *testing.T, team, authority string) {
	t.Helper()
	cfg, err := loadTeamConfig(team)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Repository = Object{"transport": "otoha", "repository": "test-owner/test-repo", "url": authority + "/v1/git/" + cfg.Project + "/test-owner/test-repo.git", "access": "member"}
	if err = saveTeamConfig(cfg); err != nil {
		t.Fatal(err)
	}
}

func TestPageGitProxyUsesBoundRepositoryAndOtohaIdentity(t *testing.T) {
	t.Setenv("OTOHA_HOME", t.TempDir())
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/v1/git/page-fixture/test-owner/test-repo.git/info/refs" || r.URL.Query().Get("service") != "git-upload-pack" {
			t.Error("local Git path was not mapped to the bound authority repository", r.URL)
		}
		if r.Header.Get("Authorization") != "Bearer original-test-credential" || r.Header.Get("Cookie") != "" {
			t.Error("local browser/Git credential was forwarded instead of the Otoha identity")
		}
		w.Header().Set("Content-Type", "application/x-git-upload-pack-advertisement")
		io.WriteString(w, "0000")
	}))
	defer upstream.Close()
	team := pageTestTeam(t, upstream.URL)
	pageTestRepository(t, team, upstream.URL)
	m := newPageManager(Home())
	m.token = "local-test-runtime-token"
	local := httptest.NewServer(http.HandlerFunc(m.serveHTTP))
	m.baseURL = local.URL
	defer local.Close()
	path := local.URL + "/git/" + team + "/test-owner/test-repo.git/info/refs?service=git-upload-pack"
	response, err := local.Client().Get(path)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized || calls.Load() != 0 || response.Header.Get("WWW-Authenticate") == "" {
		t.Fatal("Git was accessible without the private local credential")
	}
	request, _ := http.NewRequest(http.MethodGet, path, nil)
	request.SetBasicAuth("otoha-local", m.token)
	request.Header.Set("Cookie", "unrelated-session=must-not-forward")
	response, err = local.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || string(body) != "0000" || calls.Load() != 1 {
		t.Fatal("authorized smart-Git response did not pass through", response.StatusCode, string(body))
	}
	request, _ = http.NewRequest(http.MethodGet, strings.Replace(path, "test-repo.git", "other-repo.git", 1), nil)
	request.SetBasicAuth("otoha-local", m.token)
	response, err = local.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusForbidden || calls.Load() != 1 {
		t.Fatal("a local credential escaped the configured repository binding")
	}
	request, _ = http.NewRequest(http.MethodGet, strings.Replace(path, "/info/refs?service=git-upload-pack", "/info/lfs/objects/batch", 1), nil)
	request.SetBasicAuth("otoha-local", m.token)
	response, err = local.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNotFound || calls.Load() != 1 {
		t.Fatal("unsupported LFS endpoint was silently advertised as supported")
	}
}

func TestPageGitCredentialMatchesOnlyRuntimeAndTeamRepository(t *testing.T) {
	t.Setenv("OTOHA_HOME", t.TempDir())
	team := pageTestTeam(t, "https://authority.invalid")
	pageTestRepository(t, team, "https://authority.invalid")
	runtimePath := filepath.Join(Home(), "page-runtime.json")
	if err := Save(runtimePath, Object{"url": "http://127.0.0.1:43123", "token": "local-test-token"}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(runtimePath)
	base := "protocol=http\nhost=127.0.0.1:43123\npath=git/" + team + "/test-owner/test-repo.git\n\n"
	var output bytes.Buffer
	if err := PageGitCredential([]string{"--team", team, "get"}, strings.NewReader(base), &output); err != nil {
		t.Fatal(err)
	}
	if output.String() != "username=otoha-local\npassword=local-test-token\n\n" {
		t.Fatal("bound Git request did not obtain only the local proxy credential")
	}
	for _, input := range []string{
		strings.Replace(base, "43123", "43124", 1),
		strings.Replace(base, "test-repo.git", "other-repo.git", 1),
		strings.Replace(base, "path=git/", "path=git/other/", 1),
		strings.Replace(base, "protocol=http", "protocol=https", 1),
		strings.Replace(base, "test-repo.git", "test-repo.git/../other", 1),
	} {
		output.Reset()
		if err := PageGitCredential([]string{"--team", team, "get"}, strings.NewReader(input), &output); err != nil || output.Len() != 0 {
			t.Fatal("credential escaped its exact local runtime/repository", err)
		}
	}
	for _, action := range []string{"store", "erase"} {
		output.Reset()
		if err := PageGitCredential([]string{"--team", team, action}, strings.NewReader(base), &output); err != nil || output.Len() != 0 {
			t.Fatal("credential store/erase was not a no-op", err)
		}
	}
	after, _ := os.ReadFile(runtimePath)
	if !bytes.Equal(before, after) {
		t.Fatal("credential helper changed local login material")
	}
}

func TestPageGitTargetRejectsAnotherAuthorityOrProject(t *testing.T) {
	t.Setenv("OTOHA_HOME", t.TempDir())
	team := pageTestTeam(t, "https://authority.invalid")
	pageTestRepository(t, team, "https://authority.invalid")
	cfg, _ := loadTeamConfig(team)
	profile, _ := Load(cfg.Profile)
	for _, endpoint := range []string{
		"https://another.invalid/v1/git/page-fixture/test-owner/test-repo.git",
		"https://authority.invalid/v1/git/another-project/test-owner/test-repo.git",
		"https://authority.invalid/v1/git/page-fixture/test-owner/another-repo.git",
	} {
		cfg.Repository["url"] = endpoint
		if _, err := pageGitTarget(cfg, profile); err == nil {
			t.Fatal("repository metadata redirected Otoha credentials outside its bound scope", endpoint)
		}
	}
	endpoint, _ := url.Parse("https://authority.invalid/v1/git/page-fixture/test-owner/test-repo.git")
	endpoint.User = url.UserPassword("embedded", "credential")
	cfg.Repository["url"] = endpoint.String()
	if _, err := pageGitTarget(cfg, profile); err == nil {
		t.Fatal("embedded repository credential was accepted")
	}
}
