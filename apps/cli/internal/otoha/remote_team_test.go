package otoha

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRemoteTeamInitUsesPinnedSSHAndHTTPSCA(t *testing.T) {
	home := t.TempDir() + "/captain"
	t.Setenv("OTOHA_HOME", home)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/health":
			w.Write([]byte(`{"service":"otoha-collaboration"}`))
		case "/v1/manifest":
			w.Write([]byte(`{"actor":{"id":"captain","owner":1},"grants":[],"capabilities":["messages"]}`))
		case "/v1/projects", "/v1/channels":
			w.Write([]byte(`{"id":"remote"}`))
		default:
			http.Error(w, "missing", http.StatusNotFound)
		}
	}))
	defer server.Close()
	cert, err := x509.ParseCertificate(server.Certificate().Raw)
	if err != nil {
		t.Fatal(err)
	}
	caPath := filepath.Join(t.TempDir(), "ca.pem")
	if err = os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	knownHosts := filepath.Join(t.TempDir(), "known_hosts")
	if err = os.WriteFile(knownHosts, []byte("example ssh-ed25519 AAAA\n"), 0600); err != nil {
		t.Fatal(err)
	}
	sshBin := t.TempDir()
	profilePath := filepath.Join(t.TempDir(), "remote-owner.json")
	profile := `{"protocol":"otoha-collaboration/1","url":"` + server.URL + `","actor":"captain","device":"owner","token":"remote-secret"}`
	if err = os.WriteFile(profilePath, []byte(profile), 0600); err != nil {
		t.Fatal(err)
	}
	sshScript := filepath.Join(sshBin, "ssh")
	script := "#!/bin/sh\nlast=\"\"\nfor arg in \"$@\"; do last=\"$arg\"; done\ncase \"$last\" in\n  *'cat --'*) cat \"$FAKE_PROFILE\";;\n  *'nohup'*) echo 4242;;\n  *) echo '{\"initialized\":true}';;\nesac\n"
	if err = os.WriteFile(sshScript, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_PROFILE", profilePath)
	t.Setenv("PATH", sshBin+":"+os.Getenv("PATH"))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, err := TeamCommand(ctx, []string{"init", "remote", "--mode", "server", "--url", server.URL, "--ssh-target", "captain@example", "--known-hosts", knownHosts, "--remote-script", "/opt/otoha_server.py", "--remote-state", "/srv/otoha/state", "--remote-cert", "/srv/otoha/cert.pem", "--remote-tls-key", "/srv/otoha/key.pem", "--remote-log", "/srv/otoha/server.log", "--ca-file", caPath, "--min-free-bytes", "0"})
	if err != nil {
		t.Fatal(err)
	}
	if S(result, "mode") != "server" || !B(result, "tls") || N(result, "server_pid") != 4242 {
		t.Fatalf("unexpected remote init result: %#v", result)
	}
	saved, err := Load(S(result, "profile"))
	if err != nil {
		t.Fatal(err)
	}
	if S(saved, "token") != "remote-secret" || S(saved, "ca_pem") == "" {
		t.Fatalf("remote profile did not retain private token and public CA: %#v", saved)
	}
	teamJSON, err := os.ReadFile(teamConfigPath("remote"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(teamJSON), "remote-secret") {
		t.Fatal("team metadata leaked the owner token")
	}
	status, err := TeamCommand(ctx, []string{"status", "remote"})
	if err != nil || !B(status, "healthy") {
		t.Fatalf("remote status did not use the configured CA: %#v %v", status, err)
	}
	doctor, err := TeamCommand(ctx, []string{"doctor", "remote"})
	if err != nil || !B(doctor, "ok") {
		t.Fatalf("remote team doctor did not use the configured CA: %#v %v", doctor, err)
	}
}
