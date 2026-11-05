package otoha

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func configureReceiverTestTeam(t *testing.T) string {
	t.Helper()
	profile := filepath.Join(Home(), "teams", "example", "member.json")
	if err := Save(profile, Object{"url": "https://example.test", "actor": "member", "token": "private-team-token"}); err != nil {
		t.Fatal(err)
	}
	if err := saveTeamConfig(TeamConfig{Name: "example", URL: "https://example.test", Project: "example", Channel: "example-general", Profile: profile, Mode: "member"}); err != nil {
		t.Fatal(err)
	}
	return profile
}

func TestReceiverFingerprintSupportsTeamWithoutLegacyConfig(t *testing.T) {
	t.Setenv("OTOHA_HOME", t.TempDir())
	profile := configureReceiverTestTeam(t)
	first, err := proxyFingerprint()
	if err != nil || len(first) != 64 || strings.Contains(first, "private-team-token") {
		t.Fatalf("team fingerprint = %q, %v", first, err)
	}
	if _, err := os.Stat(filepath.Join(Home(), "config.json")); !os.IsNotExist(err) {
		t.Fatalf("fingerprinting created legacy config: %v", err)
	}
	identity, err := Load(profile)
	if err != nil {
		t.Fatal(err)
	}
	identity["token"] = "rotated-private-team-token"
	if err := Save(profile, identity); err != nil {
		t.Fatal(err)
	}
	second, err := proxyFingerprint()
	if err != nil || second == first {
		t.Fatalf("changed credential did not change private digest: %q, %v", second, err)
	}
	identity["url"] = "https://another.test"
	if err := Save(profile, identity); err != nil {
		t.Fatal(err)
	}
	if _, err := proxyFingerprint(); err == nil {
		t.Fatal("mismatched service identity was accepted")
	}
}

func TestReceiverFingerprintDoesNotBypassInvalidLegacyConfig(t *testing.T) {
	t.Setenv("OTOHA_HOME", t.TempDir())
	configureReceiverTestTeam(t)
	if err := os.WriteFile(filepath.Join(Home(), "config.json"), []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := proxyFingerprint(); err == nil {
		t.Fatal("invalid existing legacy config was bypassed")
	}
}

func TestReceiverFingerprintRequiresAUsableTeam(t *testing.T) {
	t.Setenv("OTOHA_HOME", t.TempDir())
	if _, err := proxyFingerprint(); err == nil {
		t.Fatal("empty Home can install an unconfigured daemon")
	}
	configureReceiverTestTeam(t)
	if err := os.Remove(filepath.Join(Home(), "teams", "example", "member.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := proxyFingerprint(); err == nil {
		t.Fatal("missing team identity was accepted")
	}
}

func TestTeamOnlyReceiverInstallStoresDigestWithoutLegacyConfig(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("uses a test launchd/systemd command")
	}
	t.Setenv("OTOHA_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	configureReceiverTestTeam(t)
	commands := t.TempDir()
	manager := "systemctl"
	if runtime.GOOS == "darwin" {
		manager = "launchctl"
	}
	script := "#!/bin/sh\nif [ \"$1\" = print ]; then exit 1; fi\nexit 0\n"
	if err := os.WriteFile(filepath.Join(commands, manager), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", commands+string(os.PathListSeparator)+os.Getenv("PATH"))
	if err := InstallReceiver(); err != nil {
		t.Fatal(err)
	}
	metadata, err := os.ReadFile(filepath.Join(Home(), "receiver-service.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(metadata), "private-team-token") {
		t.Fatal("service metadata contains team credential")
	}
	managed, ok := daemonManaged()
	if !ok || len(S(managed, "proxy_fingerprint")) != 64 {
		t.Fatalf("invalid service metadata: %#v", managed)
	}
	for _, legacy := range []string{"config.json", "git-proxy.json"} {
		if _, err := os.Stat(filepath.Join(Home(), legacy)); !os.IsNotExist(err) {
			t.Fatalf("team-only installation created %s: %v", legacy, err)
		}
	}
}

func TestDaemonRestartPreservesStopFailure(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("uses a test launchd/systemd command")
	}
	t.Setenv("OTOHA_HOME", t.TempDir())
	if err := Save(filepath.Join(Home(), "receiver-service.json"), Object{"managed": true, "name": serviceName()}); err != nil {
		t.Fatal(err)
	}
	commands := t.TempDir()
	manager := "systemctl"
	if runtime.GOOS == "darwin" {
		manager = "launchctl"
	}
	if err := os.WriteFile(filepath.Join(commands, manager), []byte("#!/bin/sh\necho stop-blocked >&2\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", commands+string(os.PathListSeparator)+os.Getenv("PATH"))
	if _, err := DaemonCommand([]string{"restart"}); err == nil || !strings.Contains(err.Error(), "stop-blocked") {
		t.Fatalf("restart discarded stop failure: %v", err)
	}
}

func TestDaemonStatusRedactsPageBootstrapSecrets(t *testing.T) {
	t.Setenv("OTOHA_HOME", t.TempDir())
	if err := Save(filepath.Join(Home(), "page-runtime.json"), Object{
		"url":   "http://127.0.0.1:45678/?private=hidden#token=hidden",
		"token": "private-page-token", "pid": os.Getpid(), "started_at": "test",
	}); err != nil {
		t.Fatal(err)
	}
	status, err := DaemonStatus()
	if err != nil {
		t.Fatal(err)
	}
	page := O(status, "page")
	if S(page, "base_url") != "http://127.0.0.1:45678" || !B(page, "alive") {
		t.Fatalf("page status = %#v", page)
	}
	encoded, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "hidden") || strings.Contains(string(encoded), "private-page-token") || strings.Contains(string(encoded), "token") {
		t.Fatalf("daemon status exposes bootstrap material: %s", encoded)
	}
}

func TestLinuxReceiverVerificationRejectsUnrelatedLiveProcess(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("/proc process ownership is verified on Linux")
	}
	t.Setenv("OTOHA_HOME", t.TempDir())
	if err := verifyLinuxReceiver(os.Getpid(), filepath.Join(Home(), "bin", "otoha")); err == nil {
		t.Fatal("test runner was accepted as an Otoha receiver")
	}
}
