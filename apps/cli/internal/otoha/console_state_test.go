package otoha

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConsoleEmptyHomeDoesNotInventReadiness(t *testing.T) {
	t.Setenv("OTOHA_HOME", t.TempDir())
	var output bytes.Buffer
	if err := Console(strings.NewReader(""), &output); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	if !strings.Contains(text, "未接入项目") || !strings.Contains(text, "otoha join --invite FILE") {
		t.Fatalf("empty client has no clear join entry: %s", text)
	}
	for _, invented := range []string{"READY", "05", "active", "SQLite", "local core", "\x1b["} {
		if strings.Contains(text, invented) {
			t.Fatalf("empty client displayed invented runtime state: %q", invented)
		}
	}
}

func TestConsoleListsConfiguredTeamWithoutClaimingRemoteActivity(t *testing.T) {
	t.Setenv("OTOHA_HOME", t.TempDir())
	identity := filepath.Join(Home(), "private-member.json")
	if err := Save(identity, Object{"url": "https://fixture.example.invalid", "actor": "test-member", "token": "synthetic-private-token"}); err != nil {
		t.Fatal(err)
	}
	if err := saveTeamConfig(TeamConfig{Protocol: "otoha-team/1", Name: "sample", Mode: "member", Project: "sample", Channel: "sample-general", URL: "https://fixture.example.invalid", Profile: identity, State: "ready"}); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := Console(strings.NewReader(""), &output); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	if !strings.Contains(text, "1 个项目") || !strings.Contains(text, "sample") || !strings.Contains(text, "otoha page open --team sample --browser") {
		t.Fatalf("configured project is not discoverable: %s", text)
	}
	for _, unsupported := range []string{"已连接", "READY", "active", "synthetic-private-token"} {
		if strings.Contains(text, unsupported) {
			t.Fatalf("local configuration was presented as runtime activity or leaked a credential: %q", unsupported)
		}
	}
}

func TestConsoleHelpRefreshAndQuitDoNotCreateServiceState(t *testing.T) {
	t.Setenv("OTOHA_HOME", t.TempDir())
	var output bytes.Buffer
	if err := runLocalConsoleLoop(strings.NewReader("help\nstatus\nq\n"), &output, false); err != nil {
		t.Fatal(err)
	}
	for _, entry := range []string{"otoha join --invite FILE", "otoha page help", "otoha message help", "未接入项目"} {
		if !strings.Contains(output.String(), entry) {
			t.Errorf("console help is missing %q", entry)
		}
	}
	for _, path := range []string{"receiver-status.json", "receiver-service.json", "page-runtime.json"} {
		if _, err := os.Stat(filepath.Join(Home(), path)); !os.IsNotExist(err) {
			t.Errorf("read-only console created service state %s", path)
		}
	}
}
