package otoha

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func initRuntimeFixture(t *testing.T) (string, Object) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX process fixture; released runtime targets are macOS and Linux")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("Python test fixture unavailable")
	}
	t.Setenv("OTOHA_HOME", t.TempDir())
	team := "runtime-fixture"
	root := teamRoot(team)
	if err := PrivateDir(root); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(root, "spawn-count.txt")
	t.Setenv("OTOHA_RUNTIME_FIXTURE_LOG", log)
	script := `import http.server,os,signal,sys,time
with open(os.environ['OTOHA_RUNTIME_FIXTURE_LOG'],'a') as f:f.write(str(os.getpid())+'\n')
if len(sys.argv)>1 and sys.argv[1]=='start':
 host,port=sys.argv[sys.argv.index('--bind')+1].rsplit(':',1)
 class Health(http.server.BaseHTTPRequestHandler):
  def do_GET(self):self.send_response(200);self.end_headers()
  def log_message(self,*args):pass
 http.server.HTTPServer((host,int(port)),Health).serve_forever()
else:
 while True:time.sleep(.1)
`
	for _, name := range []string{"surreal", "frpc"} {
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, []byte("#!"+python+"\n"+script), 0700); err != nil {
			t.Fatal(err)
		}
	}
	frpcConfig := filepath.Join(root, "frpc.toml")
	if err := os.WriteFile(frpcConfig, []byte("# synthetic fixture\n"), 0600); err != nil {
		t.Fatal(err)
	}
	port, err := freeLoopbackPort()
	if err != nil {
		t.Fatal(err)
	}
	config := Object{"schema": 1, "managed": true, "frpc": filepath.Join(root, "frpc"), "frpc_config": frpcConfig,
		"surreal": filepath.Join(root, "surreal"), "surreal_url": fmt.Sprintf("http://127.0.0.1:%d", port), "surreal_data": filepath.Join(root, "database", "data"), "surreal_username": "fixture", "surreal_password": "fixture-password"}
	if err := Save(initRuntimeConfigPath(team), config); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stopInitRuntime(team) })
	return team, config
}

func waitInitRuntimeProcessExit(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for processAlive(pid) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if processAlive(pid) {
		t.Fatalf("test runtime PID %d did not stop", pid)
	}
}

func TestInitRuntimeCancelledPreparationDoesNotStartProcesses(t *testing.T) {
	team, _ := initRuntimeFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := EnsureInitRuntime(ctx, team); err != context.Canceled {
		t.Fatal("cancelled preparation did not preserve cancellation", err)
	}
	if _, err := os.Stat(initRuntimeStatusPath(team)); !os.IsNotExist(err) {
		t.Fatal("cancelled preparation created process records", err)
	}
	if _, err := os.Stat(os.Getenv("OTOHA_RUNTIME_FIXTURE_LOG")); !os.IsNotExist(err) {
		t.Fatal("cancelled preparation started a persistent child", err)
	}
}

func TestInitRuntimeReusesBootstrapAndRestartsRelayOnly(t *testing.T) {
	team, _ := initRuntimeFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	first, err := EnsureInitRuntime(ctx, team)
	if err != nil {
		t.Fatal(first, err)
	}
	if B(first, "network_verified") || S(first, "state") != "running" {
		t.Fatal("local process state pretended external readiness", first)
	}
	surrealPID := N(O(O(first, "components"), "surreal"), "pid")
	frpcPID := N(O(O(first, "components"), "frpc"), "pid")
	second, err := EnsureInitRuntime(ctx, team)
	if err != nil || N(O(O(second, "components"), "surreal"), "pid") != surrealPID || N(O(O(second, "components"), "frpc"), "pid") != frpcPID {
		t.Fatal("repeat init created duplicate runtimes", second, err)
	}
	if err := stopProcess(frpcPID); err != nil {
		t.Fatal(err)
	}
	waitInitRuntimeProcessExit(t, frpcPID)
	third, err := EnsureInitRuntime(ctx, team)
	if err != nil || N(O(O(third, "components"), "surreal"), "pid") != surrealPID || N(O(O(third, "components"), "frpc"), "pid") == frpcPID {
		t.Fatal("relay restart changed the database lifecycle", third, err)
	}
	cancelled, stop := context.WithCancel(context.Background())
	stop()
	_, _ = EnsureInitRuntime(cancelled, team)
	if !processAlive(surrealPID) || !processAlive(N(O(O(third, "components"), "frpc"), "pid")) {
		t.Fatal("short init context stopped persistent children")
	}
}

func TestInitRuntimeSupervisorStopsItsRecordedProcesses(t *testing.T) {
	team, _ := initRuntimeFixture(t)
	first, err := EnsureInitRuntime(context.Background(), team)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { newPageManager(Home()).superviseInitRuntime(ctx, team); close(done) }()
	time.Sleep(150 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("runtime supervisor did not stop")
	}
	for _, kind := range []string{"surreal", "frpc"} {
		waitInitRuntimeProcessExit(t, N(O(O(first, "components"), kind), "pid"))
	}
}

func TestInitRuntimeDoesNotAdoptAnUnrecordedListener(t *testing.T) {
	team, config := initRuntimeFixture(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer server.Close()
	config["surreal_url"] = server.URL
	if err := Save(initRuntimeConfigPath(team), config); err != nil {
		t.Fatal(err)
	}
	result, err := EnsureInitRuntime(context.Background(), team)
	if err == nil || !strings.Contains(err.Error(), "unrecorded process") || S(result, "state") != "needs_attention" {
		t.Fatal("runtime adopted an unrelated database", result, err)
	}
}

func TestInitRuntimeSupervisorRenewsSameRouteOnEachStartup(t *testing.T) {
	team, config := initRuntimeFixture(t)
	config["relay_route_id"], config["relay_token"] = "original-route", "original-private-route-token"
	if err := Save(initRuntimeConfigPath(team), config); err != nil {
		t.Fatal(err)
	}
	previous := renewManagedInitRoute
	var renewals atomic.Int32
	renewManagedInitRoute = func(ctx context.Context, value Object) error {
		if S(value, "relay_route_id") != "original-route" || S(value, "relay_token") != "original-private-route-token" {
			t.Error("renewal replaced its route identity")
		}
		renewals.Add(1)
		return nil
	}
	t.Cleanup(func() { renewManagedInitRoute = previous })
	for count := int32(1); count <= 2; count++ {
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { newPageManager(Home()).superviseInitRuntime(ctx, team); close(done) }()
		deadline := time.Now().Add(5 * time.Second)
		for renewals.Load() < count && time.Now().Before(deadline) {
			time.Sleep(20 * time.Millisecond)
		}
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("renewal supervisor did not stop")
		}
		if renewals.Load() != count {
			t.Fatal("daemon startup did not renew exactly the original lease", renewals.Load())
		}
		status, err := Load(initRuntimeStatusPath(team))
		if err != nil || S(O(status, "relay"), "state") != "renewed" {
			t.Fatal("renewal outcome was not persisted", status, err)
		}
		for _, kind := range []string{"surreal", "frpc"} {
			waitInitRuntimeProcessExit(t, N(O(O(status, "components"), kind), "pid"))
		}
	}
}

func TestInitRuntimeExpiredRelayCanRenewBeforeProcessRecovery(t *testing.T) {
	team, config := initRuntimeFixture(t)
	config["relay_route_id"], config["relay_token"] = "expired-original-route", "retained-private-route-token"
	if err := Save(initRuntimeConfigPath(team), config); err != nil {
		t.Fatal(err)
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(teamRoot(team), "route-renewed")
	script := fmt.Sprintf("#!%s\nimport pathlib,sys,time\nif not pathlib.Path(%q).exists():sys.exit(11)\nwhile True:time.sleep(.1)\n", python, marker)
	if err := os.WriteFile(S(config, "frpc"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	initial, _ := EnsureInitRuntime(context.Background(), team)
	// Process start is distinct from relay readiness: under load Python may
	// exit just after the initial local-process observation.
	if pid := N(O(O(initial, "components"), "frpc"), "pid"); pid > 0 {
		waitInitRuntimeProcessExit(t, pid)
	}
	previous := renewManagedInitRoute
	var renewals atomic.Int32
	renewManagedInitRoute = func(ctx context.Context, saved Object) error {
		if S(saved, "relay_route_id") != "expired-original-route" || S(saved, "relay_token") != "retained-private-route-token" {
			t.Error("expired route was replaced instead of renewed")
		}
		renewals.Add(1)
		return os.WriteFile(marker, []byte("renewed"), 0600)
	}
	t.Cleanup(func() { renewManagedInitRoute = previous })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { newPageManager(Home()).superviseInitRuntime(ctx, team); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	var status Object
	for time.Now().Before(deadline) {
		status, _ = Load(initRuntimeStatusPath(team))
		if S(status, "state") == "running" && S(O(status, "relay"), "state") == "renewed" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("expired route supervisor did not stop")
	}
	if renewals.Load() != 1 || S(status, "state") != "running" || S(O(status, "relay"), "state") != "renewed" {
		t.Fatal("expired relay could not reach renewal before process checks", status, renewals.Load())
	}
	for _, kind := range []string{"surreal", "frpc"} {
		waitInitRuntimeProcessExit(t, N(O(O(status, "components"), kind), "pid"))
	}
}
