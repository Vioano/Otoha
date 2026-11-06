package otoha

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"time"
)

var renewManagedInitRoute = renewInitRoute

func initRuntimeConfigPath(team string) string {
	return filepath.Join(teamRoot(team), "runtime.json")
}

func initRuntimeStatusPath(team string) string {
	return filepath.Join(teamRoot(team), "runtime-status.json")
}

func loadInitRuntime(team string) (Object, error) {
	if err := Name(team); err != nil {
		return nil, err
	}
	config, err := Load(initRuntimeConfigPath(team))
	if err != nil {
		return nil, err
	}
	if !B(config, "managed") {
		return nil, errors.New("team runtime is not explicitly managed")
	}
	for _, key := range []string{"frpc", "frpc_config", "surreal", "surreal_data"} {
		if !filepath.IsAbs(S(config, key)) {
			return nil, fmt.Errorf("managed runtime %s must have an absolute path", key)
		}
	}
	u, err := url.Parse(S(config, "surreal_url"))
	if err != nil || u.Scheme != "http" || u.Port() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost" && u.Hostname() != "::1") {
		return nil, errors.New("managed database requires an HTTP loopback origin")
	}
	if S(config, "surreal_username") == "" || S(config, "surreal_password") == "" {
		return nil, errors.New("managed database credentials are missing")
	}
	return config, nil
}

func initRuntimeDigest(config Object) string {
	// Relay renewal metadata does not change process configuration. Secrets
	// participate only in the digest; status never copies the private config.
	stable := Object{}
	for _, field := range []string{"frpc", "frpc_config", "surreal", "surreal_url", "surreal_data", "surreal_username", "surreal_password"} {
		stable[field] = config[field]
	}
	data, _ := json.Marshal(stable)
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func saveInitRuntimeStatus(team string, status Object) error {
	previous, err := Load(initRuntimeStatusPath(team))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if status["relay"] == nil && previous != nil {
		status["relay"] = previous["relay"]
	}
	delete(previous, "updated_at")
	delete(status, "updated_at")
	if reflect.DeepEqual(previous, status) {
		return nil
	}
	status["updated_at"] = time.Now().UTC().Format(time.RFC3339Nano)
	return Save(initRuntimeStatusPath(team), status)
}

func initRuntimeDatabaseHealth(ctx context.Context, origin string) error {
	probe, cancel := context.WithTimeout(ctx, 600*time.Millisecond)
	defer cancel()
	request, err := http.NewRequestWithContext(probe, http.MethodGet, origin+"/health", nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: time.Second, Transport: &http.Transport{Proxy: nil}}
	defer client.CloseIdleConnections()
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("database health returned HTTP %d", response.StatusCode)
	}
	return nil
}

func startInitRuntimeProcess(team, kind string, config Object) (Object, error) {
	executable := S(config, kind)
	if info, err := os.Stat(executable); err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("managed %s executable is unavailable", kind)
	}
	var args []string
	if kind == "surreal" {
		u, _ := url.Parse(S(config, "surreal_url"))
		// Never adopt a listener that has no process record for this Home.
		connection, err := net.DialTimeout("tcp", u.Host, 200*time.Millisecond)
		if err == nil {
			connection.Close()
			return nil, errors.New("database port is already in use by an unrecorded process")
		}
		if err := PrivateDir(filepath.Dir(S(config, "surreal_data"))); err != nil {
			return nil, err
		}
		args = []string{"start", "--no-banner", "--log", "warn", "--bind", u.Host, "surrealkv://" + S(config, "surreal_data")}
	} else {
		if info, err := os.Stat(S(config, "frpc_config")); err != nil || !info.Mode().IsRegular() {
			return nil, errors.New("managed relay configuration is unavailable")
		}
		args = []string{"-c", S(config, "frpc_config")}
	}
	logPath := filepath.Join(teamRoot(team), kind+".log")
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return nil, err
	}
	command := exec.Command(executable, args...)
	command.Env = append(os.Environ(), "OTOHA_HOME="+Home())
	if kind == "surreal" {
		command.Env = append(command.Env, "SURREAL_USER="+S(config, "surreal_username"), "SURREAL_PASS="+S(config, "surreal_password"))
	}
	command.Stdout, command.Stderr = log, log
	detach(command)
	if err := command.Start(); err != nil {
		log.Close()
		return nil, err
	}
	log.Close()
	pid := command.Process.Pid
	go func() { _ = command.Wait() }()
	return Object{"pid": pid, "executable": executable, "state": "starting", "log": logPath, "started_at": time.Now().UTC().Format(time.RFC3339Nano)}, nil
}

// EnsureInitRuntime starts only the explicitly configured team's database and
// relay processes. They remain alive when a short init command exits; the
// existing daemon subsequently adopts their persisted process records.
func EnsureInitRuntime(ctx context.Context, team string) (Object, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	config, err := loadInitRuntime(team)
	if err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(filepath.Join(teamRoot(team), "runtime.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	if err := lockReceiver(lock); err != nil {
		return nil, errors.New("this team's runtime is being updated; retry its status")
	}
	status, err := Load(initRuntimeStatusPath(team))
	if os.IsNotExist(err) {
		status = Object{"schema": "otoha-runtime-status/1", "team": team, "components": Object{}}
	} else if err != nil {
		return nil, err
	}
	digest := initRuntimeDigest(config)
	components := O(status, "components")
	if oldDigest := S(status, "config_sha256"); oldDigest != "" && oldDigest != digest {
		for _, kind := range []string{"surreal", "frpc"} {
			if processAlive(N(O(components, kind), "pid")) {
				return nil, errors.New("active runtime configuration changed; original processes preserved, stop this team's runtime before switching")
			}
		}
	}
	status["config_sha256"], status["components"], status["network_verified"] = digest, components, false
	for _, kind := range []string{"surreal", "frpc"} {
		if err := ctx.Err(); err != nil {
			return status, err
		}
		component := O(components, kind)
		if pid := N(component, "pid"); pid > 0 && processAlive(pid) {
			if S(component, "executable") != S(config, kind) {
				return nil, errors.New("runtime process record differs from its configured executable; original preserved")
			}
		} else {
			component, err = startInitRuntimeProcess(team, kind, config)
			if err != nil {
				status["state"], status["last_error"] = "needs_attention", err.Error()
				_ = saveInitRuntimeStatus(team, status)
				return status, err
			}
			components[kind] = component
			// Persist the child identity before waiting for readiness.
			if err := saveInitRuntimeStatus(team, status); err != nil {
				return status, err
			}
		}
		if kind == "surreal" {
			deadline := time.Now().Add(8 * time.Second)
			for {
				err = initRuntimeDatabaseHealth(ctx, S(config, "surreal_url"))
				if err == nil {
					break
				}
				if !processAlive(N(component, "pid")) || time.Now().After(deadline) || !pagePause(ctx, 80*time.Millisecond) {
					status["state"], status["last_error"] = "needs_attention", "managed database is not healthy; inspect surreal.log"
					_ = saveInitRuntimeStatus(team, status)
					return status, errors.New(S(status, "last_error"))
				}
			}
			component["state"] = "ready"
		} else {
			if !pagePause(ctx, 80*time.Millisecond) {
				return status, ctx.Err()
			}
			if !processAlive(N(component, "pid")) {
				status["state"], status["last_error"] = "needs_attention", "managed relay process exited; inspect frpc.log"
				_ = saveInitRuntimeStatus(team, status)
				return status, errors.New(S(status, "last_error"))
			}
			component["state"] = "running"
		}
		components[kind] = component
	}
	status["state"] = "running"
	delete(status, "last_error")
	if err := saveInitRuntimeStatus(team, status); err != nil {
		return status, err
	}
	return status, nil
}

func stopInitRuntime(team string) error {
	status, err := Load(initRuntimeStatusPath(team))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	config, err := loadInitRuntime(team)
	if err != nil {
		return err
	}
	if S(status, "config_sha256") != initRuntimeDigest(config) {
		return errors.New("runtime configuration changed; original processes preserved")
	}
	components := O(status, "components")
	for _, kind := range []string{"frpc", "surreal"} {
		component := O(components, kind)
		pid := N(component, "pid")
		if pid <= 0 {
			continue
		}
		if S(component, "executable") != S(config, kind) {
			return errors.New("runtime executable record differs; original process preserved")
		}
		if processAlive(pid) {
			if err := stopProcess(pid); err != nil {
				return err
			}
		}
		component["state"] = "stopped"
		components[kind] = component
	}
	status["state"], status["components"] = "stopped", components
	return saveInitRuntimeStatus(team, status)
}

func (m *pageManager) superviseInitRuntime(ctx context.Context, team string) {
	if _, err := loadInitRuntime(team); os.IsNotExist(err) {
		return
	}
	defer func() { _ = stopInitRuntime(team) }()
	nextRenew := time.Time{}
	backoff := time.Second
	for ctx.Err() == nil {
		config, err := loadInitRuntime(team)
		if err == nil && S(config, "relay_route_id") != "" && time.Now().After(nextRenew) {
			renewContext, cancel := context.WithTimeout(ctx, 10*time.Second)
			renewErr := renewManagedInitRoute(renewContext, config)
			cancel()
			status, statusErr := Load(initRuntimeStatusPath(team))
			if statusErr != nil {
				status = Object{"schema": "otoha-runtime-status/1", "team": team}
			}
			if renewErr == nil {
				status["relay"] = Object{"state": "renewed", "renewed_at": time.Now().UTC().Format(time.RFC3339Nano)}
				nextRenew = time.Now().Add(6 * time.Hour)
			} else {
				status["relay"] = Object{"state": "unavailable", "last_error": renewErr.Error()}
				nextRenew = time.Now().Add(30 * time.Second)
				var api *APIError
				if errors.As(renewErr, &api) && (api.Status == http.StatusForbidden || api.Status == http.StatusUnauthorized || api.Status == http.StatusNotFound) {
					status["relay"] = Object{"state": "needs_attention", "last_error": renewErr.Error()}
					nextRenew = time.Now().Add(6 * time.Hour)
				}
			}
			_ = saveInitRuntimeStatus(team, status)
		}
		// An expired route may make frpc exit immediately. Renew the same lease
		// before requiring its process to stay up; local runtimes still recover
		// independently when a temporary relay error prevents renewal.
		if err == nil {
			_, err = EnsureInitRuntime(ctx, team)
		}
		if err != nil {
			if !pagePause(ctx, backoff) {
				return
			}
			if backoff < 8*time.Second {
				backoff *= 2
			}
			continue
		}
		backoff = time.Second
		if !pagePause(ctx, 2*time.Second) {
			return
		}
	}
}
