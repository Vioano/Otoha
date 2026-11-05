package otoha

import (
	"context"
	"crypto/sha256"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

func localCenterFixture(t *testing.T, start bool) (TeamConfig, *Client) {
	t.Helper()
	if _, err := os.Stat(serverScript()); err != nil {
		t.Skipf("collaboration source unavailable: %v", err)
	}
	t.Setenv("OTOHA_REPO_ROOT", repoRoot())
	t.Setenv("OTOHA_HOME", t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	args := []string{"init", "recovery", "--min-free-bytes", "0"}
	if !start {
		args = append(args, "--start=false")
	}
	initialized, err := TeamCommand(ctx, args)
	if err != nil {
		t.Fatal(err)
	}
	if pid := N(initialized, "server_pid"); pid > 0 {
		t.Cleanup(func() { _ = stopProcess(pid) })
	}
	cfg, err := loadTeamConfig("recovery")
	if err != nil {
		t.Fatal(err)
	}
	_, client, err := teamProfile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Transport.Close() })
	return cfg, client
}

func runLocalCenterFixture(t *testing.T, m *pageManager, team string) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.superviseLocalCenter(ctx, team); close(done) }()
	stop := func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("local center supervisor did not stop")
		}
	}
	t.Cleanup(stop)
	return stop
}

func waitLocalCenterStatus(t *testing.T, m *pageManager, team string, accept func(Object) bool) Object {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	var status Object
	for time.Now().Before(deadline) {
		m.mu.Lock()
		state, err := m.stateLocked(team)
		status = Clone(O(state, "local_center"))
		m.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		if accept(status) {
			return status
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("local center did not reach expected state: %s", S(status, "state"))
	return nil
}

func TestLocalCenterRecoversExistingState(t *testing.T) {
	cfg, client := localCenterFixture(t, true)
	if !cfg.ManageCenter {
		t.Fatal("new local init did not opt in to center management")
	}
	identityBefore, err := os.ReadFile(cfg.Profile)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	sent, err := client.Call(ctx, http.MethodPost, "/v1/messages", Object{"channel": cfg.Channel, "client_id": "before-restart", "text": "Original recovery fixture 中文"})
	if err != nil {
		t.Fatal(err)
	}
	m := newPageManager(Home())
	stop := runLocalCenterFixture(t, m, cfg.Name)
	waitLocalCenterStatus(t, m, cfg.Name, func(state Object) bool { return S(state, "state") == "ready" && B(state, "reused") })
	if err = stopProcess(cfg.ServerPID); err != nil {
		t.Fatal(err)
	}
	restarted := waitLocalCenterStatus(t, m, cfg.Name, func(state Object) bool {
		return S(state, "state") == "ready" && B(state, "owned_child") && N(state, "pid") > 0 && N(state, "pid") != cfg.ServerPID
	})
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
	stop()
	if processAlive(N(restarted, "pid")) {
		t.Fatal("supervisor-owned child outlived an orderly stop")
	}
}

func TestLocalCenterReusesHealthyAuthorityWithoutTakingProcessOwnership(t *testing.T) {
	cfg, client := localCenterFixture(t, true)
	m := newPageManager(Home())
	stop := runLocalCenterFixture(t, m, cfg.Name)
	status := waitLocalCenterStatus(t, m, cfg.Name, func(state Object) bool { return S(state, "state") == "ready" })
	if !B(status, "reused") || B(status, "owned_child") || N(status, "pid") != 0 {
		t.Fatal("existing authority was claimed as a supervisor-owned child")
	}
	stop()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := client.Call(ctx, http.MethodGet, "/v1/manifest", nil); err != nil {
		t.Fatalf("orderly receiver stop killed a reused authority: %v", err)
	}
}

func TestLocalCenterStartupUsesRecordedRuntimeAndNeverInitializesMissingState(t *testing.T) {
	cfg, client := localCenterFixture(t, false)
	cfg.ManageCenter = true
	if err := saveTeamConfig(cfg); err != nil {
		t.Fatal(err)
	}
	// An installed receiver must use saved paths even when its original
	// repository or invoking environment is no longer discoverable.
	t.Setenv("OTOHA_REPO_ROOT", t.TempDir())
	t.Setenv("OTOHA_SERVER_SCRIPT", filepath.Join(t.TempDir(), "missing.py"))
	t.Setenv("OTOHA_PYTHON", filepath.Join(t.TempDir(), "missing-python"))
	m := newPageManager(Home())
	stop := runLocalCenterFixture(t, m, cfg.Name)
	waitLocalCenterStatus(t, m, cfg.Name, func(state Object) bool { return S(state, "state") == "ready" && B(state, "owned_child") })
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := client.Call(ctx, http.MethodGet, "/v1/manifest", nil); err != nil {
		t.Fatal(err)
	}
	stop()
	if err := os.Remove(filepath.Join(cfg.State, "messages.sqlite3")); err != nil {
		t.Fatal(err)
	}
	m = newPageManager(Home())
	stopMissing := runLocalCenterFixture(t, m, cfg.Name)
	failure := waitLocalCenterStatus(t, m, cfg.Name, func(state Object) bool { return S(state, "state") == "failed" })
	stopMissing()
	if !strings.Contains(S(failure, "last_error"), "existing local authority is unavailable") {
		t.Fatalf("missing authority was not explained: %s", S(failure, "last_error"))
	}
	if _, err := os.Stat(filepath.Join(cfg.State, "messages.sqlite3")); !os.IsNotExist(err) {
		t.Fatal("supervisor recreated a missing database")
	}
}

func TestLocalCenterDoesNotStartMemberRemoteOrUnmanagedTeams(t *testing.T) {
	t.Setenv("OTOHA_HOME", t.TempDir())
	for _, scenario := range []struct {
		name, mode string
		manage     bool
	}{{"member", "member", true}, {"remote", "server", true}, {"old-local", "local", false}} {
		t.Run(scenario.name, func(t *testing.T) {
			cfg := TeamConfig{Name: scenario.name, Mode: scenario.mode, ManageCenter: scenario.manage,
				URL: "http://127.0.0.1:1", Project: "test", Channel: "general",
				Profile: filepath.Join(Home(), scenario.name+"-profile.json"), State: filepath.Join(Home(), scenario.name+"-state"),
				ServerLog: filepath.Join(Home(), scenario.name+"-server.log"), ServerScript: "/missing-script.py", Python: "/missing-python"}
			if err := saveTeamConfig(cfg); err != nil {
				t.Fatal(err)
			}
			m := newPageManager(Home())
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			m.superviseLocalCenter(ctx, cfg.Name)
			if _, err := os.Stat(m.statePath(cfg.Name)); !os.IsNotExist(err) {
				t.Fatal("non-managed team acquired local center state")
			}
			if _, err := os.Stat(cfg.State); !os.IsNotExist(err) {
				t.Fatal("non-managed team acquired a local authority directory")
			}
		})
	}
}

func TestLocalCenterDoesNotSpawnAgainstReachableUnauthorizedListener(t *testing.T) {
	t.Setenv("OTOHA_HOME", t.TempDir())
	listener := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer listener.Close()
	cfg := TeamConfig{Name: "unauthorized", Mode: "local", ManageCenter: true, URL: listener.URL,
		Project: "test", Channel: "general", Profile: filepath.Join(Home(), "identity.json"),
		State: filepath.Join(Home(), "authority"), ServerLog: filepath.Join(Home(), "server.log")}
	if err := Save(cfg.Profile, Object{"url": cfg.URL, "token": "synthetic-invalid-credential"}); err != nil {
		t.Fatal(err)
	}
	if err := saveTeamConfig(cfg); err != nil {
		t.Fatal(err)
	}
	m := newPageManager(Home())
	stop := runLocalCenterFixture(t, m, cfg.Name)
	failure := waitLocalCenterStatus(t, m, cfg.Name, func(state Object) bool { return S(state, "state") == "unavailable" })
	stop()
	if !strings.Contains(S(failure, "last_error"), "saved identity was not accepted") {
		t.Fatal("authorization failure was not reported distinctly")
	}
	if _, err := os.Stat(cfg.State); !os.IsNotExist(err) {
		t.Fatal("authorization failure created replacement authority state")
	}
}

func TestReceiverSIGTERMStopsOwnedCenterAndPreservesReusedCenter(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("SIGTERM process validation targets macOS and Linux")
	}
	if _, err := os.Stat(serverScript()); err != nil {
		t.Skipf("collaboration source unavailable: %v", err)
	}
	binary := filepath.Join(t.TempDir(), "otoha")
	build := exec.Command("go", "build", "-o", binary, "./cmd/otoha")
	build.Dir = filepath.Join(repoRoot(), "apps", "cli")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build receiver fixture: %v: %s", err, output)
	}
	for _, reused := range []bool{false, true} {
		name := "owned"
		if reused {
			name = "reused"
		}
		t.Run(name, func(t *testing.T) {
			cfg, client := localCenterFixture(t, reused)
			cfg.ManageCenter = true
			if err := saveTeamConfig(cfg); err != nil {
				t.Fatal(err)
			}
			log, err := os.Create(filepath.Join(Home(), "signal-test.log"))
			if err != nil {
				t.Fatal(err)
			}
			defer log.Close()
			receiver := exec.Command(binary, "receive")
			receiver.Stdout, receiver.Stderr = log, log
			if err := receiver.Start(); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- receiver.Wait(); close(done) }()
			t.Cleanup(func() {
				select {
				case <-done:
				default:
					_ = receiver.Process.Signal(syscall.SIGTERM)
					select {
					case <-done:
					case <-time.After(5 * time.Second):
						_ = receiver.Process.Kill()
						<-done
					}
				}
			})
			var center Object
			deadline := time.Now().Add(8 * time.Second)
			for time.Now().Before(deadline) {
				state, _ := Load(filepath.Join(Home(), "pages", "state", cfg.Name+".json"))
				center = O(state, "local_center")
				if S(center, "state") == "ready" && B(center, "reused") == reused {
					break
				}
				time.Sleep(50 * time.Millisecond)
			}
			if S(center, "state") != "ready" || B(center, "reused") != reused {
				t.Fatalf("actual receiver did not confirm %s center: %s", name, S(center, "state"))
			}
			if err := receiver.Process.Signal(syscall.SIGTERM); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("receiver did not exit normally after SIGTERM: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("receiver did not wait for center cleanup on SIGTERM")
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_, probeErr := client.Call(ctx, http.MethodGet, "/v1/manifest", nil)
			if reused {
				if probeErr != nil || !processAlive(cfg.ServerPID) {
					t.Fatal("receiver SIGTERM stopped an authority it only reused")
				}
			} else if probeErr == nil || processAlive(N(center, "pid")) {
				t.Fatal("receiver SIGTERM left its owned center running")
			}
		})
	}
}
