package otoha

import (
	"context"
	"crypto/sha256"
	"net/http"
	"os"
	"testing"
	"time"
)

func TestLocalCenterRecoversExistingState(t *testing.T) {
	if _, err := os.Stat(serverScript()); err != nil {
		t.Skipf("collaboration source unavailable: %v", err)
	}
	t.Setenv("OTOHA_REPO_ROOT", repoRoot())
	t.Setenv("OTOHA_HOME", t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	initialized, err := TeamCommand(ctx, []string{"init", "recovery", "--min-free-bytes", "0"})
	if err != nil {
		t.Fatal(err)
	}
	oldPID := N(initialized, "server_pid")
	defer stopProcess(oldPID)
	cfg, err := loadTeamConfig("recovery")
	if err != nil {
		t.Fatal(err)
	}
	control, err := Load(teamConfigPath(cfg.Name))
	if err != nil {
		t.Fatal(err)
	}
	control["manage_center"] = true
	if err = Save(teamConfigPath(cfg.Name), control); err != nil {
		t.Fatal(err)
	}
	identityBefore, err := os.ReadFile(cfg.Profile)
	if err != nil {
		t.Fatal(err)
	}
	_, client, err := teamProfile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Transport.Close()
	sent, err := client.Call(ctx, http.MethodPost, "/v1/messages", Object{"channel": cfg.Channel, "client_id": "before-restart", "text": "Original recovery fixture 中文"})
	if err != nil {
		t.Fatal(err)
	}
	m := newPageManager(Home())
	go m.discoverTeams(ctx)
	if err = stopProcess(oldPID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(8 * time.Second)
	recovered := false
	for time.Now().Before(deadline) {
		probe, stop := context.WithTimeout(ctx, 300*time.Millisecond)
		_, err = client.Call(probe, http.MethodGet, "/v1/manifest", nil)
		stop()
		if err == nil {
			recovered = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !recovered {
		t.Fatal("managed local center did not restart with the existing authority")
	}
	history, err := client.Call(ctx, http.MethodGet, "/v1/messages?channel="+cfg.Channel, nil)
	if err != nil {
		t.Fatal(err)
	}
	rows := Items(history, "messages")
	if len(rows) != 1 || N(rows[0], "id") != N(sent, "message_id") || S(O(rows[0], "payload"), "body") != "Original recovery fixture 中文" {
		t.Fatalf("accepted original was not preserved: %d messages", len(rows))
	}
	identityAfter, err := os.ReadFile(cfg.Profile)
	if err != nil {
		t.Fatal(err)
	}
	if sha256.Sum256(identityBefore) != sha256.Sum256(identityAfter) {
		t.Fatal("recovery replaced the existing credentials")
	}
}
