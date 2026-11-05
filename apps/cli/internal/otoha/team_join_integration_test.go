package otoha

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

func joinTestInvitation(t *testing.T, repository Object) (string, *atomic.Int32) {
	t.Helper()
	enrollments := &atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/enroll":
			enrollments.Add(1)
			json.NewEncoder(w).Encode(Object{"device": "test-mac"})
		case "/v1/manifest":
			json.NewEncoder(w).Encode(Object{"actor": Object{"id": "test-member"}, "grants": []Object{{"project": "test-team", "role": "member"}}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	path := filepath.Join(t.TempDir(), "invitation.json")
	if err := Save(path, Object{"protocol": "otoha-collaboration/1", "url": server.URL, "actor": "test-member", "project": "test-team", "channel": "test-team-general", "invitation": "test-short-lived-invitation", "repository": repository}); err != nil {
		t.Fatal(err)
	}
	return path, enrollments
}

func joinTestGit(t *testing.T, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	b, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Git fixture failed: %v: %s", err, b)
	}
	return strings.TrimSpace(string(b))
}

func TestTeamJoinRepositoryResumesSameIdentityAndPreservesWork(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses a POSIX SSH wrapper")
	}
	t.Setenv("OTOHA_HOME", filepath.Join(t.TempDir(), "member"))
	bare := filepath.Join(t.TempDir(), "test.git")
	seed := filepath.Join(t.TempDir(), "seed")
	joinTestGit(t, "init", "--bare", "--initial-branch=main", bare)
	joinTestGit(t, "init", "--initial-branch=main", seed)
	if err := os.WriteFile(filepath.Join(seed, "README.md"), []byte("Synthetic test project\n"), 0600); err != nil {
		t.Fatal(err)
	}
	joinTestGit(t, "-C", seed, "add", "README.md")
	joinTestGit(t, "-C", seed, "-c", "user.name=Otoha Test", "-c", "user.email=test@example.invalid", "commit", "-m", "Test fixture")
	joinTestGit(t, "-C", seed, "push", bare, "main")
	ssh := filepath.Join(t.TempDir(), "test-ssh")
	if err := os.WriteFile(ssh, []byte("#!/bin/sh\nexec git-upload-pack \"$OTOHA_TEST_REPOSITORY\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_SSH_COMMAND", ssh)
	t.Setenv("GIT_SSH_VARIANT", "ssh")
	t.Setenv("OTOHA_TEST_REPOSITORY", bare)
	invitation, enrollments := joinTestInvitation(t, Object{"url": "git@fixture.invalid:team/test.git", "branch": "main"})
	previousEnsure := ensureJoinedPageService
	t.Cleanup(func() { ensureJoinedPageService = previousEnsure })
	started := 0
	ensureJoinedPageService = func(ctx context.Context, team string) (Object, error) {
		started++
		cfg, err := loadTeamConfig(team)
		if err != nil || cfg.Checkout == "" {
			t.Fatalf("service started before durable checkout metadata: %#v %v", cfg, err)
		}
		return Object{"url": "http://127.0.0.1:12345/", "team": team, "ready": true, "connection": "online", "local_ready": true}, nil
	}
	checkout := filepath.Join(t.TempDir(), "checkout")
	args := []string{"join", "--invite", invitation, "--directory", checkout}
	first, err := TeamCommand(context.Background(), args)
	if err != nil || !B(first, "ready") || S(O(first, "readiness"), "repository") != "ready" {
		t.Fatalf("join failed: %#v %v", first, err)
	}
	identityBefore, err := os.ReadFile(S(first, "profile"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(checkout, "README.md"), []byte("Uncommitted member work\n"), 0600); err != nil {
		t.Fatal(err)
	}
	second, err := TeamCommand(context.Background(), args)
	if err != nil || !B(second, "resumed") || !B(second, "ready") || enrollments.Load() != 1 || started != 2 {
		t.Fatalf("join did not resume exactly one identity: %#v %v enrollments=%d started=%d", second, err, enrollments.Load(), started)
	}
	identityAfter, _ := os.ReadFile(S(first, "profile"))
	readme, _ := os.ReadFile(filepath.Join(checkout, "README.md"))
	if string(identityBefore) != string(identityAfter) || string(readme) != "Uncommitted member work\n" {
		t.Fatal("repeated join replaced identity or local work")
	}
	if got := joinTestGit(t, "-C", checkout, "remote", "get-url", "origin"); got != "git@fixture.invalid:team/test.git" {
		t.Fatalf("unexpected origin: %s", got)
	}
	ensureJoinedPageService = func(ctx context.Context, team string) (Object, error) {
		return Object{"url": "http://127.0.0.1:12345/", "team": team, "local_ready": true, "connection": "offline", "ready": false}, nil
	}
	offline, err := TeamCommand(context.Background(), args)
	if err != nil || !B(offline, "joined") || B(offline, "ready") || S(O(offline, "readiness"), "service") != "ready" || S(O(offline, "readiness"), "communication") != "offline" || enrollments.Load() != 1 {
		t.Fatalf("local page availability masked offline communication: %#v %v", offline, err)
	}
}

func TestTeamJoinDoesNotOverwriteAnExistingProjectOrDirectory(t *testing.T) {
	t.Setenv("OTOHA_HOME", filepath.Join(t.TempDir(), "member"))
	invitation, enrollments := joinTestInvitation(t, Object{"url": "git@fixture.invalid:team/test.git"})
	owner := TeamConfig{Protocol: "otoha-team/1", Name: "test-team", Mode: "local", URL: "http://127.0.0.1:1234", Project: "test-team", Channel: "test-team-general", Profile: "/synthetic/owner.json"}
	if err := saveTeamConfig(owner); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(teamConfigPath("test-team"))
	if _, err := TeamCommand(context.Background(), []string{"join", "--invite", invitation}); err == nil {
		t.Fatal("join accepted a conflicting authority")
	}
	after, _ := os.ReadFile(teamConfigPath("test-team"))
	if string(before) != string(after) || enrollments.Load() != 0 {
		t.Fatal("conflict touched an existing project or enrolled another identity")
	}
	if err := os.Remove(teamConfigPath("test-team")); err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	marker := filepath.Join(directory, "existing-work.txt")
	if err := os.WriteFile(marker, []byte("Do not overwrite\n"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := TeamCommand(context.Background(), []string{"join", "--invite", invitation, "--directory", directory})
	if err == nil || !B(result, "joined") || B(result, "ready") || S(O(result, "readiness"), "repository") != "error" || S(result, "checkout") != directory {
		t.Fatalf("partial readiness was not reported: %#v %v", result, err)
	}
	if b, _ := os.ReadFile(marker); string(b) != "Do not overwrite\n" {
		t.Fatal("existing directory was changed")
	}
}

func TestJoinRepositoryLocationCannotCarryCredentialsOrLocalCommands(t *testing.T) {
	for _, raw := range []string{"https://user:password@example.invalid/team/test.git", "http://example.invalid/team/test.git", "file:///private/repository", "ext::sh -c command", "/private/repository"} {
		if err := validateJoinRepository(Object{"url": raw}); err == nil {
			t.Errorf("accepted invalid invitation locator: %s", raw)
		}
	}
	for _, raw := range []string{"git@example.invalid:team/test.git", "ssh://git@example.invalid/team/test.git", "https://example.invalid/team/test.git", "http://127.0.0.1:8080/team/test.git"} {
		if err := validateJoinRepository(Object{"url": raw}); err != nil {
			t.Errorf("rejected supported invitation locator %s: %v", raw, err)
		}
	}
}

func TestTeamJoinRequiresProjectGrantBeforeRepositoryCheckout(t *testing.T) {
	t.Setenv("OTOHA_HOME", filepath.Join(t.TempDir(), "member"))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/enroll" {
			json.NewEncoder(w).Encode(Object{"device": "test-mac"})
			return
		}
		json.NewEncoder(w).Encode(Object{"actor": Object{"id": "test-member"}, "grants": []Object{}})
	}))
	defer server.Close()
	invitation := filepath.Join(t.TempDir(), "invitation.json")
	if err := Save(invitation, Object{"protocol": "otoha-collaboration/1", "url": server.URL, "actor": "test-member", "project": "test-team", "channel": "test-team-general", "invitation": "test-invitation", "repository": Object{"url": "git@fixture.invalid:team/test.git"}}); err != nil {
		t.Fatal(err)
	}
	checkout := filepath.Join(t.TempDir(), "checkout")
	result, err := TeamCommand(context.Background(), []string{"join", "--invite", invitation, "--directory", checkout})
	if err == nil || !B(result, "joined") || B(result, "ready") || S(O(result, "readiness"), "project") != "error" {
		t.Fatalf("missing project grant was not reported: %#v %v", result, err)
	}
	if _, err = os.Stat(checkout); !os.IsNotExist(err) {
		t.Fatal("repository checkout ran without a project grant")
	}
	if _, err = Load(S(result, "profile")); err != nil {
		t.Fatal("joined identity was not retained for recovery")
	}
}

func TestTeamJoinRejectsUnfinishedMatchingCheckout(t *testing.T) {
	t.Setenv("OTOHA_HOME", filepath.Join(t.TempDir(), "member"))
	repository := "git@fixture.invalid:team/test.git"
	invitation, _ := joinTestInvitation(t, Object{"url": repository, "branch": "main"})
	checkout := filepath.Join(t.TempDir(), "checkout")
	joinTestGit(t, "init", "--initial-branch=main", checkout)
	joinTestGit(t, "-C", checkout, "remote", "add", "origin", repository)
	marker := filepath.Join(checkout, "uncommitted.txt")
	if err := os.WriteFile(marker, []byte("Existing work\n"), 0600); err != nil {
		t.Fatal(err)
	}
	headBefore, _ := os.ReadFile(filepath.Join(checkout, ".git", "HEAD"))
	result, err := TeamCommand(context.Background(), []string{"join", "--invite", invitation, "--directory", checkout})
	if err == nil || !strings.Contains(err.Error(), "HEAD has no commit") || B(result, "ready") || S(O(result, "readiness"), "repository") != "error" || S(O(result, "readiness"), "service") != "not_started" {
		t.Fatalf("unfinished checkout was considered ready: %#v %v", result, err)
	}
	headAfter, _ := os.ReadFile(filepath.Join(checkout, ".git", "HEAD"))
	work, _ := os.ReadFile(marker)
	if string(headBefore) != string(headAfter) || string(work) != "Existing work\n" {
		t.Fatal("unfinished checkout was overwritten")
	}
}

func TestTeamJoinUsesPrivateGatewayAndResumesWithoutInvitationSecrets(t *testing.T) {
	t.Setenv("OTOHA_HOME", filepath.Join(t.TempDir(), "member"))
	invitation, enrollments := joinTestInvitation(t, Object{})
	inv, err := Load(invitation)
	if err != nil {
		t.Fatal(err)
	}
	upstream, _ := net.ResolveTCPAddr("tcp", strings.TrimPrefix(S(inv, "url"), "http://"))
	// No HTTP listener is present at the invitation's local port. Enrollment
	// can only succeed if NewTransport carries it over the pinned SSH route.
	localPort, err := freeLoopbackPort()
	if err != nil {
		t.Fatal(err)
	}
	inv["url"] = "http://127.0.0.1:" + strconv.Itoa(localPort)
	if err = Save(invitation, inv); err != nil {
		t.Fatal(err)
	}
	invitationBefore, _ := os.ReadFile(invitation)
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := x509.MarshalPKCS8PrivateKey(private)
	keyBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encoded})
	gatewayDirectory := t.TempDir()
	keyFile := filepath.Join(gatewayDirectory, "existing-key")
	if err = os.WriteFile(keyFile, keyBytes, 0600); err != nil {
		t.Fatal(err)
	}
	signer, _ := ssh.NewSignerFromKey(private)
	config := &ssh.ServerConfig{PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
		if string(key.Marshal()) != string(signer.PublicKey().Marshal()) {
			return nil, io.EOF
		}
		return nil, nil
	}}
	config.AddHostKey(signer)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				server, channels, requests, err := ssh.NewServerConn(conn, config)
				if err != nil {
					conn.Close()
					return
				}
				defer server.Close()
				go ssh.DiscardRequests(requests)
				for channel := range channels {
					var target struct {
						Host       string
						Port       uint32
						OriginHost string
						OriginPort uint32
					}
					if channel.ChannelType() != "direct-tcpip" || ssh.Unmarshal(channel.ExtraData(), &target) != nil || target.Host != "127.0.0.1" || int(target.Port) != upstream.Port {
						channel.Reject(ssh.Prohibited, "outside test forwarding scope")
						continue
					}
					remote, err := net.Dial("tcp", upstream.String())
					if err != nil {
						channel.Reject(ssh.ConnectionFailed, "upstream unavailable")
						continue
					}
					forward, forwardRequests, err := channel.Accept()
					if err != nil {
						remote.Close()
						continue
					}
					go ssh.DiscardRequests(forwardRequests)
					go func() { io.Copy(forward, remote); forward.Close(); remote.Close() }()
					go func() { io.Copy(remote, forward); forward.Close(); remote.Close() }()
				}
			}()
		}
	}()
	knownHostsFile := filepath.Join(gatewayDirectory, "known_hosts")
	if err = os.WriteFile(knownHostsFile, []byte(knownhosts.Normalize(listener.Addr().String())+" "+string(ssh.MarshalAuthorizedKey(signer.PublicKey()))), 0600); err != nil {
		t.Fatal(err)
	}
	gatewayFile := filepath.Join(gatewayDirectory, "existing-gateway.json")
	if err = Save(gatewayFile, Object{"host": "127.0.0.1", "port": listener.Addr().(*net.TCPAddr).Port, "user": "fixture", "key_file": "existing-key", "known_hosts_file": "known_hosts", "routes": []Object{{"key_file": "existing-key", "known_hosts_file": "known_hosts"}}, "forwards": []Object{{"local": localPort, "remote": upstream.Port}}}); err != nil {
		t.Fatal(err)
	}
	gatewayBefore, _ := os.ReadFile(gatewayFile)
	t.Chdir(t.TempDir())
	t.Setenv("OTOHA_GATEWAY_PROFILE", gatewayFile)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	joined, err := TeamCommand(ctx, []string{"join", "--invite", invitation})
	if err != nil || !B(joined, "joined") {
		t.Fatalf("SSH-carried enrollment failed: %#v %v", joined, err)
	}
	profile, err := Load(S(joined, "profile"))
	savedGateway := O(profile, "gateway")
	savedRoutes := Items(savedGateway, "routes")
	if err != nil || S(savedGateway, "key_file") != keyFile || S(savedGateway, "known_hosts_file") != knownHostsFile || len(savedRoutes) != 1 || S(savedRoutes[0], "key_file") != keyFile || S(savedRoutes[0], "known_hosts_file") != knownHostsFile {
		t.Fatalf("private route was not retained for recovery: %#v %v", profile, err)
	}
	profileBefore, _ := os.ReadFile(S(joined, "profile"))
	gatewayAfter, _ := os.ReadFile(gatewayFile)
	if string(gatewayBefore) != string(gatewayAfter) {
		t.Fatal("normalization rewrote the original gateway profile")
	}
	t.Setenv("OTOHA_GATEWAY_PROFILE", "")
	if err = os.Remove(gatewayFile); err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())
	resumed, err := TeamCommand(ctx, []string{"join", "--invite", invitation})
	if err != nil || !B(resumed, "resumed") || enrollments.Load() != 1 {
		t.Fatalf("resume did not reuse the saved private route: %#v %v enrollments=%d", resumed, err, enrollments.Load())
	}
	profileAfter, _ := os.ReadFile(S(joined, "profile"))
	invitationAfter, _ := os.ReadFile(invitation)
	keyAfter, _ := os.ReadFile(keyFile)
	if string(profileBefore) != string(profileAfter) || string(invitationBefore) != string(invitationAfter) || strings.Contains(string(invitationAfter), "key_file") || string(keyBytes) != string(keyAfter) {
		t.Fatal("resume changed the identity, invitation or existing SSH key")
	}
}
