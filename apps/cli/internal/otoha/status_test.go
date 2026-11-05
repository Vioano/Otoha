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

func TestLocalStatusReportsTeamsWithoutLegacyConfigOrInventedConnection(t *testing.T) {
	t.Setenv("OTOHA_HOME", t.TempDir())
	profile := filepath.Join(Home(), "identity.json")
	if err := Save(profile, Object{"url": "https://fixture.example.invalid", "actor": "member", "token": "synthetic-private-token"}); err != nil {
		t.Fatal(err)
	}
	cfg := TeamConfig{Protocol: "otoha-team/1", Name: "sample", Mode: "member", Project: "sample", Channel: "sample-general", URL: "https://fixture.example.invalid", Profile: profile, State: "ready", RepositoryURL: "http://127.0.0.1:12345/git/sample/owner/repo.git", Checkout: "/synthetic/checkout", Repository: Object{"transport": "otoha", "repository": "owner/repo", "url": "https://fixture.example.invalid/v1/git/sample/owner/repo.git"}}
	if err := saveTeamConfig(cfg); err != nil {
		t.Fatal(err)
	}
	status, err := LocalStatus(context.Background())
	if err != nil || S(status, "state") != "joined" || B(status, "legacy_configured") {
		t.Fatalf("new team was hidden behind legacy config: %#v %v", status, err)
	}
	rows := Array(status["teams"])
	if len(rows) != 1 {
		t.Fatalf("missing team: %#v", status)
	}
	row := rows[0].(Object)
	if B(row, "ready") || S(row, "connection") != "unknown" || S(row, "origin_url") != cfg.RepositoryURL || S(O(row, "repository"), "authority_url") != S(cfg.Repository, "url") || S(O(row, "repository"), "url") != "" {
		t.Fatalf("status confused local registration, native origin or internal authority: %#v", row)
	}
	encoded, _ := json.Marshal(status)
	if strings.Contains(string(encoded), "synthetic-private-token") {
		t.Fatal("status leaked identity credentials")
	}
	if _, err := os.Stat(filepath.Join(Home(), "page-runtime.json")); !os.IsNotExist(err) {
		t.Fatal("read-only status started a service")
	}
}

func TestMemberDoctorDoesNotRequirePythonNodeOrServerSource(t *testing.T) {
	t.Setenv("OTOHA_HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	t.Setenv("OTOHA_PYTHON", "/nonexistent/python")
	t.Setenv("OTOHA_SERVER_SCRIPT", "/nonexistent/otoha_server.py")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"service":"synthetic-authority"}`)) }))
	defer server.Close()
	profile := filepath.Join(Home(), "identity.json")
	if err := Save(profile, Object{"url": server.URL, "actor": "member", "token": "synthetic-token"}); err != nil {
		t.Fatal(err)
	}
	if err := saveTeamConfig(TeamConfig{Protocol: "otoha-team/1", Name: "sample", Mode: "member", Project: "sample", Channel: "sample-general", URL: server.URL, Profile: profile}); err != nil {
		t.Fatal(err)
	}
	for _, check := range []func(context.Context) (Object, error){Doctor, func(ctx context.Context) (Object, error) { return teamDoctor(ctx, "sample") }} {
		result, err := check(context.Background())
		if err != nil || !B(result, "ok") {
			t.Fatalf("member-only client required unused server runtimes: %#v %v", result, err)
		}
		for _, row := range result["checks"].([]Object) {
			if (S(row, "id") == "python" || S(row, "id") == "node" || S(row, "id") == "server_script" || S(row, "id") == "collaboration_server") && S(row, "status") == "fail" {
				t.Fatalf("member mode failed an unused runtime: %#v", row)
			}
		}
	}
}

func TestServerSourceIsFoundNextToPackagedBinaryFromAnotherDirectory(t *testing.T) {
	t.Setenv("OTOHA_REPO_ROOT", "")
	t.Setenv("OTOHA_SERVER_SCRIPT", "")
	t.Chdir(t.TempDir())
	packageRoot := t.TempDir()
	executable := filepath.Join(packageRoot, "bin", "otoha")
	if err := os.MkdirAll(filepath.Dir(executable), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executable, []byte("synthetic binary locator"), 0700); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(packageRoot, "services", "collaboration", "otoha_server.py")
	if err := os.MkdirAll(filepath.Dir(script), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script, []byte("# synthetic packaged service"), 0600); err != nil {
		t.Fatal(err)
	}
	previous := executableForRepo
	executableForRepo = func() (string, error) { return executable, nil }
	t.Cleanup(func() { executableForRepo = previous })
	expectedRoot, _ := filepath.EvalSymlinks(packageRoot)
	expectedScript := filepath.Join(expectedRoot, "services", "collaboration", "otoha_server.py")
	if repoRoot() != expectedRoot || serverScript() != expectedScript {
		t.Fatalf("packaged server was not found independently of cwd: %s %s", repoRoot(), serverScript())
	}
}
