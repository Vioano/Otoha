package otoha

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Exercise the existing collaboration authority, rather than inventing an
// enrollment/profile copy for each project. All identities and data are temporary.
func TestTeamCreatePublishesWithOriginalOwnerActualServer(t *testing.T) {
	if _, err := os.Stat(serverScript()); err != nil {
		t.Skipf("collaboration server source is unavailable: %v", err)
	}
	t.Setenv("OTOHA_HOME", t.TempDir())
	t.Setenv("OTOHA_REPO_ROOT", repoRoot())
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	initialized, err := TeamCommand(ctx, []string{"init", "existing-center", "--min-free-bytes", "0"})
	if err != nil {
		t.Fatal(err)
	}
	defer stopProcess(N(initialized, "server_pid"))
	source, err := loadTeamConfig("existing-center")
	if err != nil {
		t.Fatal(err)
	}
	identityBefore, err := os.ReadFile(source.Profile)
	if err != nil {
		t.Fatal(err)
	}
	configBefore, err := os.ReadFile(teamConfigPath(source.Name))
	if err != nil {
		t.Fatal(err)
	}
	manager := newPageManager(Home())
	publish := func(team, request, title string) {
		t.Helper()
		cfg, err := loadTeamConfig(team)
		if err != nil {
			t.Fatal(err)
		}
		_, client, err := teamProfile(cfg)
		if err != nil {
			t.Fatal(err)
		}
		defer client.Transport.Close()
		_, err = manager.enqueue(team, "publish", Object{"request_id": request, "document": testPageDocument(title)})
		if err != nil {
			t.Fatal(err)
		}
		op, err := Load(manager.pendingPath(team, request))
		if err != nil {
			t.Fatal(err)
		}
		if err = manager.submitOperation(ctx, team, client, op); err != nil {
			t.Fatal(err)
		}
		events, err := client.Call(ctx, http.MethodGet, "/v1/events?channel="+cfg.Channel+"&after=0&wait=0", nil)
		if err != nil {
			t.Fatal(err)
		}
		if err = manager.acceptEvents(team, cfg.Channel, events); err != nil {
			t.Fatal(err)
		}
	}
	publish(source.Name, "original-page-v1", "Existing team's page")

	created, err := TeamCommand(ctx, []string{"create", "layout-fixture", "--from-team", source.Name})
	if err != nil || !B(created, "registered") {
		t.Fatalf("normal creation failed: %v %v", created, err)
	}
	target, err := loadTeamConfig("layout-fixture")
	if err != nil {
		t.Fatal(err)
	}
	if target.Profile != source.Profile || target.URL != source.URL || target.Project != "layout-fixture" || target.Channel != "layout-fixture-general" {
		t.Fatalf("new project did not reuse the original identity/authority: %#v", target)
	}
	if target.Mode != "member" || target.ManageCenter || target.ServerPID != 0 || target.State != "ready" || target.RepositoryURL != "" {
		t.Fatalf("registration inherited center or repository management: %#v", target)
	}
	if _, err := os.Stat(filepath.Join(teamRoot(target.Name), "captain.json")); !os.IsNotExist(err) {
		t.Fatalf("registration minted or copied another identity: %v", err)
	}
	publish(target.Name, "layout-page-v1", "This project's own page")
	for team, request := range map[string]string{source.Name: "original-page-v1", target.Name: "layout-page-v1"} {
		state, err := newPageManager(Home()).snapshot(team)
		if err != nil || S(O(state, "page"), "request_id") != request {
			t.Fatalf("project page was mixed or overwritten: %s %v %v", team, state, err)
		}
	}
	if _, err = TeamCommand(ctx, []string{"create", target.Name, "--from-team", source.Name}); err != nil {
		t.Fatal("matching registration could not be resumed", err)
	}
	identityAfter, _ := os.ReadFile(source.Profile)
	configAfter, _ := os.ReadFile(teamConfigPath(source.Name))
	if string(identityAfter) != string(identityBefore) || string(configAfter) != string(configBefore) {
		t.Fatal("registration changed the source identity or team configuration")
	}
}
