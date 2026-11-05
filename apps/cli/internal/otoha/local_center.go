package otoha

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"time"
)

type localCenterProcess struct {
	cmd     *exec.Cmd
	done    chan error
	started time.Time
}

func (p *localCenterProcess) stop() {
	if p == nil {
		return
	}
	select {
	case <-p.done:
		return
	default:
	}
	// Only this supervisor's child is stopped. An already-running authority
	// that passed the authenticated probe is reused without taking ownership.
	if err := p.cmd.Process.Signal(os.Interrupt); err != nil {
		_ = p.cmd.Process.Kill()
	}
	select {
	case <-p.done:
	case <-time.After(2 * time.Second):
		_ = p.cmd.Process.Kill()
		<-p.done
	}
}

func localCenterOrigin(cfg TeamConfig) (*url.URL, error) {
	u, err := url.Parse(cfg.URL)
	if err != nil || u.Scheme != "http" || u.Port() == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return nil, errors.New("managed local center requires an HTTP loopback origin with a port")
	}
	if u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost" && u.Hostname() != "::1" {
		return nil, errors.New("managed local center cannot bind a remote authority")
	}
	return u, nil
}

// An HTTP listener that rejects the saved identity is not a stopped center.
// Return reachable=true in that case so it is reported instead of spawning a
// competing process against an occupied port.
func probeLocalCenter(ctx context.Context, cfg TeamConfig) (reachable bool, err error) {
	probe, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(probe, http.MethodGet, cfg.URL+"/health", nil)
	if err != nil {
		return false, err
	}
	client := &http.Client{Timeout: time.Second, Transport: &http.Transport{Proxy: nil}}
	defer client.CloseIdleConnections()
	response, err := client.Do(request)
	if err != nil {
		return false, err
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return true, fmt.Errorf("local center health returned HTTP %d", response.StatusCode)
	}
	profile, authority, err := teamProfile(cfg)
	if err != nil {
		return true, err
	}
	defer authority.Transport.Close()
	if S(profile, "url") != cfg.URL {
		return true, errors.New("local center profile does not match its configured origin")
	}
	manifest, err := authority.Call(probe, http.MethodGet, "/v1/manifest", nil)
	if err != nil {
		return true, fmt.Errorf("local center saved identity was not accepted: %w", err)
	}
	if S(manifest, "protocol") != "otoha-collaboration/1" || !B(O(manifest, "actor"), "owner") {
		return true, errors.New("local center did not confirm the saved authority identity")
	}
	return true, nil
}

func startManagedLocalCenter(ctx context.Context, cfg TeamConfig) (*localCenterProcess, error) {
	origin, err := localCenterOrigin(cfg)
	if err != nil {
		return nil, err
	}
	// serve must never initialize a missing database or substitute new state
	// when a configured volume or directory is unavailable.
	for _, path := range []string{filepath.Join(cfg.State, "server.json"), filepath.Join(cfg.State, "messages.sqlite3"), cfg.Profile} {
		if info, err := os.Stat(path); err != nil {
			return nil, fmt.Errorf("existing local authority is unavailable: %w", err)
		} else if !info.Mode().IsRegular() {
			return nil, errors.New("existing local authority requires regular state and identity files")
		}
	}
	script, python := localTeamRuntime(cfg)
	if script == "" || !filepath.IsAbs(script) {
		return nil, errors.New("local center server_script must name the existing absolute script path")
	}
	if _, err := os.Stat(script); err != nil {
		return nil, fmt.Errorf("local center script unavailable: %w", err)
	}
	if _, err := exec.LookPath(python); err != nil {
		return nil, fmt.Errorf("local center Python unavailable: %w", err)
	}
	if err := PrivateDir(filepath.Dir(cfg.ServerLog)); err != nil {
		return nil, err
	}
	log, err := os.OpenFile(cfg.ServerLog, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return nil, err
	}
	command := exec.Command(python, script, "serve", "--state", cfg.State, "--host", origin.Hostname(), "--port", origin.Port())
	command.Env = append(os.Environ(), "PYTHONUNBUFFERED=1")
	command.Stdout, command.Stderr = log, log
	if err := command.Start(); err != nil {
		log.Close()
		return nil, err
	}
	log.Close()
	child := &localCenterProcess{cmd: command, done: make(chan error, 1), started: time.Now()}
	go func() { child.done <- command.Wait(); close(child.done) }()
	ready, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for ready.Err() == nil {
		select {
		case err := <-child.done:
			return nil, fmt.Errorf("local center exited before becoming ready: %v; see %s", err, cfg.ServerLog)
		default:
		}
		if reachable, err := probeLocalCenter(ready, cfg); reachable && err == nil {
			return child, nil
		}
		if !pagePause(ready, 100*time.Millisecond) {
			break
		}
	}
	child.stop()
	return nil, fmt.Errorf("local center did not accept its saved identity: %w; see %s", ready.Err(), cfg.ServerLog)
}

func (m *pageManager) localCenterStatus(team string, status Object) {
	m.mu.Lock()
	current, err := m.stateLocked(team)
	previous := Clone(O(current, "local_center"))
	m.mu.Unlock()
	if err != nil {
		fmt.Fprintln(os.Stderr, "otoha local center status:", err)
		return
	}
	delete(previous, "updated_at")
	if reflect.DeepEqual(previous, status) {
		return
	}
	status["updated_at"] = pageTime()
	if err := m.changeState(team, func(state Object) error { state["local_center"] = status; return nil }); err != nil {
		fmt.Fprintln(os.Stderr, "otoha local center status:", err)
	}
}

func (m *pageManager) superviseLocalCenter(ctx context.Context, team string) {
	var child *localCenterProcess
	defer func() {
		if child != nil {
			child.stop()
			m.localCenterStatus(team, Object{"state": "stopped", "managed": true, "pid": 0})
		}
	}()
	backoff := time.Second
	for ctx.Err() == nil {
		cfg, err := loadTeamConfig(team)
		if err != nil {
			m.localCenterStatus(team, Object{"state": "failed", "last_error": err.Error()})
		} else if cfg.Mode != "local" || !cfg.ManageCenter {
			return
		} else if _, err = localCenterOrigin(cfg); err != nil {
			m.localCenterStatus(team, Object{"state": "failed", "last_error": err.Error()})
		} else {
			if child != nil {
				select {
				case exited := <-child.done:
					m.localCenterStatus(team, Object{"state": "restarting", "managed": true, "last_error": fmt.Sprintf("local center exited: %v", exited)})
					child = nil
					if !pagePause(ctx, backoff) {
						return
					}
					if backoff < 8*time.Second {
						backoff *= 2
					}
				default:
					if time.Since(child.started) > 30*time.Second {
						backoff = time.Second
					}
				}
			}
			reachable, probeErr := probeLocalCenter(ctx, cfg)
			if reachable && probeErr == nil {
				state := Object{"state": "ready", "managed": child != nil, "reused": child == nil, "pid": 0}
				if child != nil {
					state["pid"] = child.cmd.Process.Pid
				}
				m.localCenterStatus(team, state)
				if !pagePause(ctx, 2*time.Second) {
					return
				}
				continue
			}
			if reachable || child != nil {
				m.localCenterStatus(team, Object{"state": "unavailable", "managed": child != nil, "last_error": probeErr.Error()})
			} else {
				m.localCenterStatus(team, Object{"state": "starting", "managed": true, "pid": 0})
				child, err = startManagedLocalCenter(ctx, cfg)
				if err != nil {
					m.localCenterStatus(team, Object{"state": "failed", "managed": true, "last_error": err.Error()})
				} else {
					continue
				}
			}
		}
		if !pagePause(ctx, backoff) {
			return
		}
		if backoff < 8*time.Second {
			backoff *= 2
		}
	}
}
