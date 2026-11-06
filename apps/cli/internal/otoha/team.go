package otoha

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// TeamConfig is the local, private control record for a team authority or
// member. It intentionally contains a profile path, never the profile data.
type TeamConfig struct {
	Protocol         string `json:"protocol"`
	Name             string `json:"name"`
	Mode             string `json:"mode"`
	URL              string `json:"url"`
	LocalURL         string `json:"local_url,omitempty"`
	Project          string `json:"project"`
	Channel          string `json:"channel"`
	State            string `json:"state"`
	Profile          string `json:"profile"`
	ServerPID        int    `json:"server_pid,omitempty"`
	ServerLog        string `json:"server_log,omitempty"`
	CreatedAt        string `json:"created_at"`
	StartedBy        string `json:"started_by,omitempty"`
	RepositoryURL    string `json:"repository_url,omitempty"`
	RepositoryBranch string `json:"repository_branch,omitempty"`
	Checkout         string `json:"checkout,omitempty"`
	GiteaProfile     string `json:"gitea_profile,omitempty"`
	Repository       Object `json:"repository,omitempty"`
	ServerScript     string `json:"server_script,omitempty"`
	Python           string `json:"python,omitempty"`
	ManageCenter     bool   `json:"manage_center,omitempty"`
}

type remoteTeamOptions struct {
	Name, Owner, Project, Channel, Endpoint, DataRoot string
	MinFreeBytes                                      int64
	SSHTarget, SSHKey, KnownHosts, RemoteScript       string
	RemotePython, RemoteState, RemoteCredentials      string
	RemoteCert, RemoteKey, RemoteLog                  string
	SSHPort, TLSPort, RemotePort                      int
	CAFile                                            string
}

func teamRoot(name string) string       { return filepath.Join(Home(), "teams", name) }
func teamConfigPath(name string) string { return filepath.Join(teamRoot(name), "team.json") }

func saveTeamConfig(cfg TeamConfig) error {
	return Save(teamConfigPath(cfg.Name), Object{
		"protocol": cfg.Protocol, "name": cfg.Name, "mode": cfg.Mode,
		"url": cfg.URL, "project": cfg.Project, "channel": cfg.Channel,
		"local_url": cfg.LocalURL,
		"state":     cfg.State, "profile": cfg.Profile, "server_pid": cfg.ServerPID,
		"server_log": cfg.ServerLog, "created_at": cfg.CreatedAt, "started_by": cfg.StartedBy,
		"repository_url": cfg.RepositoryURL, "repository_branch": cfg.RepositoryBranch, "checkout": cfg.Checkout,
		"gitea_profile": cfg.GiteaProfile,
		"repository":    cfg.Repository,
		"server_script": cfg.ServerScript, "python": cfg.Python,
		"manage_center": cfg.ManageCenter,
	})
}

func loadTeamConfig(name string) (TeamConfig, error) {
	o, err := Load(teamConfigPath(name))
	if err != nil {
		return TeamConfig{}, err
	}
	cfg := TeamConfig{
		Protocol: S(o, "protocol"), Name: S(o, "name"), Mode: S(o, "mode"),
		URL: S(o, "url"), Project: S(o, "project"), Channel: S(o, "channel"),
		LocalURL: S(o, "local_url"),
		State:    S(o, "state"), Profile: S(o, "profile"), ServerPID: N(o, "server_pid"),
		ServerLog: S(o, "server_log"), CreatedAt: S(o, "created_at"), StartedBy: S(o, "started_by"),
		RepositoryURL: S(o, "repository_url"), RepositoryBranch: S(o, "repository_branch"), Checkout: S(o, "checkout"),
		GiteaProfile: S(o, "gitea_profile"),
		Repository:   O(o, "repository"),
		ServerScript: S(o, "server_script"), Python: S(o, "python"),
		ManageCenter: B(o, "manage_center"),
	}
	if cfg.Name == "" {
		cfg.Name = name
	}
	if cfg.Profile == "" || cfg.URL == "" || cfg.Project == "" || cfg.Channel == "" {
		return TeamConfig{}, fmt.Errorf("team configuration is incomplete: %s", teamConfigPath(name))
	}
	return cfg, nil
}

func teamProfile(cfg TeamConfig) (Object, *Client, error) {
	p, err := Load(cfg.Profile)
	if err != nil {
		return nil, nil, err
	}
	client := NewClient(p, nil, false)
	if err := ConfigureClientTLS(client); err != nil {
		client.Transport.Close()
		return nil, nil, err
	}
	return p, client, nil
}

var executableForRepo = os.Executable

func repoRoot() string {
	if root := os.Getenv("OTOHA_REPO_ROOT"); root != "" {
		return root
	}
	for dir, err := os.Getwd(); err == nil && dir != "."; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, "services", "collaboration", "otoha_server.py")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
	}
	if executable, err := executableForRepo(); err == nil {
		if resolved, resolveErr := filepath.EvalSymlinks(executable); resolveErr == nil {
			executable = resolved
		}
		for _, dir := range []string{filepath.Dir(executable), filepath.Dir(filepath.Dir(executable))} {
			if _, err := os.Stat(filepath.Join(dir, "services", "collaboration", "otoha_server.py")); err == nil {
				return dir
			}
		}
	}
	return ""
}

func pythonCommand() string {
	if p := os.Getenv("OTOHA_PYTHON"); p != "" {
		return p
	}
	return "python3"
}

func localTeamRuntime(cfg TeamConfig) (string, string) {
	script, python := cfg.ServerScript, cfg.Python
	if script == "" {
		script = serverScript()
	}
	if python == "" {
		python = pythonCommand()
	}
	return script, python
}

func serverScript() string {
	if p := os.Getenv("OTOHA_SERVER_SCRIPT"); p != "" {
		return p
	}
	if root := repoRoot(); root != "" {
		return filepath.Join(root, "services", "collaboration", "otoha_server.py")
	}
	return ""
}

func freeLoopbackPort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func waitHealth(ctx context.Context, endpoint string) error {
	client := &http.Client{Timeout: 500 * time.Millisecond}
	var last error
	deadline := time.Now().Add(12 * time.Second)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(endpoint, "/")+"/health", nil)
		if err == nil {
			resp, callErr := client.Do(req)
			if callErr == nil {
				resp.Body.Close()
				if resp.StatusCode >= 200 && resp.StatusCode < 300 {
					return nil
				}
				last = fmt.Errorf("health returned HTTP %d", resp.StatusCode)
			} else {
				last = callErr
			}
		} else {
			last = err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(150 * time.Millisecond):
		}
	}
	if last == nil {
		last = errors.New("health check timed out")
	}
	return last
}

func startLocalTeamServer(ctx context.Context, cfg TeamConfig, minFreeBytes int64) (int, error) {
	if cfg.Mode != "local" {
		return 0, fmt.Errorf("team mode %q is not supported by local init", cfg.Mode)
	}
	script, python := localTeamRuntime(cfg)
	if script == "" {
		return 0, errors.New("cannot find services/collaboration/otoha_server.py; set OTOHA_SERVER_SCRIPT")
	}
	if _, err := os.Stat(script); err != nil {
		return 0, err
	}
	if _, err := exec.LookPath(python); err != nil {
		return 0, fmt.Errorf("python runtime %q is unavailable: %w", python, err)
	}
	if err := PrivateDir(filepath.Dir(cfg.ServerLog)); err != nil {
		return 0, err
	}
	log, err := os.OpenFile(cfg.ServerLog, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return 0, err
	}
	endpoint := cfg.URL
	if cfg.LocalURL != "" {
		endpoint = cfg.LocalURL
	}
	cmd := exec.Command(python, script, "serve", "--state", cfg.State, "--host", "127.0.0.1", "--port", portFromURL(endpoint))
	cmd.Stdout, cmd.Stderr = log, log
	cmd.Stdin = nil
	if err = cmd.Start(); err != nil {
		log.Close()
		return 0, err
	}
	pid := cmd.Process.Pid
	_ = cmd.Process.Release()
	_ = log.Close()
	if err = waitHealth(ctx, endpoint); err != nil {
		return pid, fmt.Errorf("local collaboration service did not become healthy: %w", err)
	}
	return pid, nil
}

func portFromURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Port() == "" {
		return "8788"
	}
	return u.Port()
}

func serverInit(ctx context.Context, cfg TeamConfig, owner string, minFreeBytes int64) error {
	if err := PrivateDir(cfg.State); err != nil {
		return err
	}
	if err := PrivateDir(filepath.Dir(cfg.Profile)); err != nil {
		return err
	}
	script, python := localTeamRuntime(cfg)
	if script == "" {
		return errors.New("cannot find collaboration server script")
	}
	args := []string{script, "init", "--state", cfg.State, "--owner", owner, "--url", cfg.URL, "--credentials", cfg.Profile, "--min-free-bytes", strconv.FormatInt(minFreeBytes, 10)}
	cmd := exec.CommandContext(ctx, python, args...)
	cmd.Env = append(os.Environ(), "PYTHONUNBUFFERED=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("server init: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func teamJoin(ctx context.Context, invitePath, device string, localGateways ...Object) (Object, error) {
	inv, err := Load(invitePath)
	if err != nil {
		return nil, err
	}
	if S(inv, "protocol") != "otoha-collaboration/1" || S(inv, "url") == "" || S(inv, "invitation") == "" || S(inv, "actor") == "" {
		return nil, errors.New("invalid Otoha collaboration invitation")
	}
	var gateway Object
	if len(localGateways) > 0 && len(localGateways[0]) > 0 {
		gateway = localGateways[0]
		if err = validateTeamGateway(S(inv, "url"), gateway); err != nil {
			return nil, err
		}
	}
	actor, project, channel := S(inv, "actor"), S(inv, "project"), S(inv, "channel")
	if err = Name(actor); err != nil {
		return nil, fmt.Errorf("invalid invited actor: %w", err)
	}
	if project == "" {
		project = "team"
	}
	if channel == "" {
		channel = project + "-general"
	}
	if err := Name(project); err != nil {
		return nil, fmt.Errorf("invalid invited project: %w", err)
	}
	if err := Name(channel); err != nil {
		return nil, fmt.Errorf("invalid invited channel: %w", err)
	}
	root := filepath.Join(Home(), "teams", project, "members", actor)
	profilePath := filepath.Join(root, "identity.json")
	if old, loadErr := Load(profilePath); loadErr == nil {
		if S(old, "actor") != actor || S(old, "project") != project || S(old, "channel") != channel || strings.TrimRight(S(old, "url"), "/") != strings.TrimRight(S(inv, "url"), "/") {
			return nil, errors.New("invitation does not match the existing member identity; original identity preserved")
		}
		gatewayChanged := false
		if gateway != nil {
			oldRoute, _ := json.Marshal(O(old, "gateway"))
			newRoute, _ := json.Marshal(gateway)
			gatewayChanged = string(oldRoute) != string(newRoute)
			old = Clone(old)
			old["gateway"] = gateway
		}
		client := NewClient(old, nil, false)
		if err := ConfigureClientTLS(client); err != nil {
			client.Transport.Close()
			return nil, err
		}
		defer client.Transport.Close()
		manifest, callErr := client.Call(ctx, http.MethodGet, "/v1/manifest", nil)
		if callErr != nil {
			return nil, fmt.Errorf("existing member identity is not usable: %w", callErr)
		}
		if S(O(manifest, "actor"), "id") != actor {
			return nil, errors.New("authenticated actor differs from the existing member identity; original identity preserved")
		}
		if gatewayChanged {
			if err = Save(profilePath, old); err != nil {
				return nil, err
			}
		}
		return Object{"joined": true, "resumed": true, "actor": actor, "project": project, "channel": channel, "url": S(old, "url"), "profile": profilePath, "grants": manifest["grants"]}, nil
	} else if !os.IsNotExist(loadErr) {
		return nil, loadErr
	}
	if device == "" {
		device, _ = os.Hostname()
		device = strings.Split(device, ".")[0]
	}
	device = safeName(device, "device")
	pendingPath := profilePath + ".pending"
	pending, pendingErr := Load(pendingPath)
	if os.IsNotExist(pendingErr) {
		pending = Object{"invitation": S(inv, "invitation"), "credential": ID() + ID(), "enrollment_id": ID(), "device": device}
		if err = Save(pendingPath, pending); err != nil {
			return nil, err
		}
	} else if pendingErr != nil {
		return nil, pendingErr
	}
	if S(pending, "invitation") != S(inv, "invitation") {
		return nil, errors.New("another enrollment is pending; original state preserved")
	}
	profileConfig := Object{"url": S(inv, "url"), "ca_file": S(inv, "ca_file"), "ca_pem": S(inv, "ca_pem")}
	if gateway != nil {
		profileConfig["gateway"] = gateway
	}
	client := NewClient(profileConfig, nil, false)
	if err := ConfigureClientTLS(client); err != nil {
		client.Transport.Close()
		return nil, err
	}
	defer client.Transport.Close()
	enrolled, err := client.Call(ctx, http.MethodPost, "/v1/enroll", pending)
	if err != nil {
		return nil, fmt.Errorf("enrollment: %w", err)
	}
	profile := Object{"protocol": "otoha-collaboration/1", "url": S(inv, "url"), "actor": actor, "device": S(enrolled, "device"), "token": S(pending, "credential"), "project": project, "channel": channel, "mode": "member"}
	if gateway != nil {
		profile["gateway"] = gateway
	}
	if ca := S(inv, "ca_pem"); ca != "" {
		profile["ca_pem"] = ca
	}
	if err = Save(profilePath, profile); err != nil {
		return nil, err
	}
	_ = os.Remove(pendingPath)
	return Object{"joined": true, "resumed": false, "actor": actor, "device": S(enrolled, "device"), "project": project, "channel": channel, "url": S(inv, "url"), "profile": profilePath, "grants": inv["grants"]}, nil
}

func safeName(value, fallback string) string {
	value = strings.TrimSpace(value)
	var b strings.Builder
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	value = strings.Trim(b.String(), "-")
	if value == "" {
		value = fallback
	}
	if len(value) > 60 {
		value = value[:60]
	}
	if err := Name(value); err != nil {
		value = "a-" + value
	}
	return value
}

func teamDoctor(ctx context.Context, name string) (Object, error) {
	checks := []Object{}
	ok := true
	add := func(id string, pass bool, detail string) {
		checks = append(checks, Object{"id": id, "status": map[bool]string{true: "pass", false: "fail"}[pass], "detail": detail})
		if !pass {
			ok = false
		}
	}
	cfg, err := loadTeamConfig(name)
	if err != nil {
		add("team_config", false, err.Error())
		return Object{"ok": ok, "team": name, "checks": checks}, nil
	}
	add("team_config", true, teamConfigPath(name))
	if cfg.Mode == "local" {
		script, python := localTeamRuntime(cfg)
		if _, err := exec.LookPath(python); err != nil {
			add("python", false, err.Error())
		} else {
			add("python", true, python)
		}
		if script == "" {
			add("server_script", false, "set OTOHA_SERVER_SCRIPT or run from the repository")
		} else if _, err := os.Stat(script); err != nil {
			add("server_script", false, err.Error())
		} else {
			add("server_script", true, script)
		}
	}
	if cfg.RepositoryURL != "" || S(cfg.Repository, "transport") == "otoha" {
		git, gitErr := exec.LookPath("git")
		if gitErr != nil {
			add("git", false, "install Git to use the configured repository")
		} else {
			add("git", true, git)
		}
	}
	if cfg.Mode == "local" {
		if st, statErr := os.Stat(cfg.State); statErr != nil || !st.IsDir() {
			add("state_dir", false, cfg.State)
		} else {
			add("state_dir", true, cfg.State)
		}
	} else {
		add("state_dir", true, "remote or member mode; local state is not owned by this CLI")
	}
	if _, err = Load(cfg.Profile); err != nil {
		add("owner_profile", false, err.Error())
	} else {
		add("owner_profile", true, cfg.Profile)
	}
	if cfg.Mode == "local" {
		serverPID := localTeamPID(cfg)
		if serverPID > 0 && processAlive(serverPID) {
			add("server_process", true, strconv.Itoa(serverPID))
		} else {
			add("server_process", false, "server process is not alive")
		}
	} else {
		add("server_process", true, "remote or member mode; health is checked through the configured endpoint")
	}
	_, client, clientErr := teamProfile(cfg)
	if clientErr != nil {
		add("health", false, clientErr.Error())
		return Object{"ok": ok, "team": name, "mode": cfg.Mode, "url": cfg.URL, "checks": checks}, nil
	}
	defer client.Transport.Close()
	if _, healthErr := client.Call(ctx, http.MethodGet, "/health", nil); healthErr != nil {
		add("health", false, healthErr.Error())
	} else {
		add("health", true, cfg.URL)
	}
	return Object{"ok": ok, "team": name, "mode": cfg.Mode, "url": cfg.URL, "checks": checks}, nil
}

func teamFlagSet(name string) *flag.FlagSet {
	f := flag.NewFlagSet(name, flag.ContinueOnError)
	f.SetOutput(io.Discard)
	return f
}

func discoverTeamName() (string, error) {
	entries, err := os.ReadDir(filepath.Join(Home(), "teams"))
	if err != nil {
		return "", err
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() && Name(entry.Name()) == nil {
			names = append(names, entry.Name())
		}
	}
	if len(names) != 1 {
		return "", fmt.Errorf("provide TEAM; found %d local teams", len(names))
	}
	return names[0], nil
}

func ensureTeamProject(ctx context.Context, client *Client, project, channel string) error {
	if _, err := client.Call(ctx, http.MethodPost, "/v1/projects", Object{"id": project, "name": project}); err != nil && !IsStatus(err, 409) {
		return err
	}
	if _, err := client.Call(ctx, http.MethodPost, "/v1/channels", Object{"id": channel, "project": project, "name": channel}); err != nil && !IsStatus(err, 409) {
		return err
	}
	return nil
}

func remoteShellQuote(value string) (string, error) {
	if strings.IndexByte(value, 0) >= 0 || strings.ContainsAny(value, "\r\n") {
		return "", errors.New("remote argument contains a newline or NUL")
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'", nil
}

func remoteSSHArgs(o remoteTeamOptions, command string) ([]string, error) {
	if strings.TrimSpace(o.SSHTarget) == "" || strings.ContainsAny(o.SSHTarget, " \t\r\n") {
		return nil, errors.New("--ssh-target USER@HOST is required and must not contain whitespace")
	}
	if o.KnownHosts == "" {
		return nil, errors.New("--known-hosts is required; remote provisioning refuses unpinned SSH hosts")
	}
	args := []string{"-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes", "-o", "UserKnownHostsFile=" + o.KnownHosts}
	if o.SSHKey != "" {
		args = append(args, "-o", "IdentitiesOnly=yes", "-i", o.SSHKey)
	}
	if o.SSHPort > 0 {
		args = append(args, "-p", strconv.Itoa(o.SSHPort))
	}
	args = append(args, o.SSHTarget, command)
	return args, nil
}

func runRemote(ctx context.Context, o remoteTeamOptions, command string) ([]byte, error) {
	args, err := remoteSSHArgs(o, command)
	if err != nil {
		return nil, err
	}
	sshPath, err := exec.LookPath("ssh")
	if err != nil {
		return nil, fmt.Errorf("ssh executable unavailable: %w", err)
	}
	result, err := exec.CommandContext(ctx, sshPath, args...).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("remote SSH command failed: %w (%s)", err, strings.TrimSpace(string(result)))
	}
	return result, nil
}

func remoteCommand(o remoteTeamOptions, subcommand string, values ...string) (string, error) {
	parts := []string{}
	for _, value := range append([]string{o.RemotePython, o.RemoteScript, subcommand}, values...) {
		quoted, err := remoteShellQuote(value)
		if err != nil {
			return "", err
		}
		parts = append(parts, quoted)
	}
	return strings.Join(parts, " "), nil
}

func remoteTeamInitCommand(ctx context.Context, o remoteTeamOptions) (Object, error) {
	if o.Endpoint == "" {
		return nil, errors.New("server mode requires --url https://HOST[:PORT]")
	}
	u, err := url.Parse(o.Endpoint)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("server mode requires a clean HTTPS origin in --url")
	}
	for _, item := range []struct{ name, value string }{
		{"--remote-script", o.RemoteScript}, {"--remote-state", o.RemoteState},
		{"--remote-cert", o.RemoteCert}, {"--remote-key", o.RemoteKey},
	} {
		if item.value == "" {
			return nil, errors.New("server mode requires " + item.name)
		}
	}
	if o.TLSPort < 1 || o.TLSPort > 65535 {
		return nil, errors.New("--tls-port must be between 1 and 65535")
	}
	if o.RemotePort == 0 {
		o.RemotePort = 8788
	}
	if o.RemotePort < 1 || o.RemotePort > 65535 {
		return nil, errors.New("--remote-port must be between 1 and 65535")
	}
	if o.RemotePython == "" {
		o.RemotePython = "python3"
	}
	if o.RemoteCredentials == "" {
		o.RemoteCredentials = filepath.Join(o.RemoteState, "captain.json")
	}
	if o.RemoteLog == "" {
		o.RemoteLog = filepath.Join(o.RemoteState, "server.log")
	}
	initCommand, err := remoteCommand(o, "init", "--state", o.RemoteState, "--owner", o.Owner, "--url", o.Endpoint, "--credentials", o.RemoteCredentials, "--min-free-bytes", strconv.FormatInt(o.MinFreeBytes, 10))
	if err != nil {
		return nil, err
	}
	if _, err = runRemote(ctx, o, initCommand); err != nil {
		return nil, err
	}
	catPath, err := remoteShellQuote(o.RemoteCredentials)
	if err != nil {
		return nil, err
	}
	credentialCommand := "cat -- " + catPath
	// `cat` is the only command in this read-back path. Its output is parsed as
	// JSON and never logged, so the owner token does not enter process output.
	credentialRaw, err := runRemote(ctx, o, credentialCommand)
	if err != nil {
		return nil, fmt.Errorf("read remote owner profile: %w", err)
	}
	var profile Object
	if err = json.Unmarshal(credentialRaw, &profile); err != nil {
		return nil, fmt.Errorf("remote owner profile is not JSON: %w", err)
	}
	if strings.TrimRight(S(profile, "url"), "/") != strings.TrimRight(o.Endpoint, "/") {
		return nil, errors.New("remote owner profile endpoint differs from requested HTTPS endpoint")
	}
	profile["mode"], profile["project"], profile["channel"] = "owner", o.Project, o.Channel
	if o.CAFile != "" {
		ca, readErr := os.ReadFile(o.CAFile)
		if readErr != nil {
			return nil, fmt.Errorf("read --ca-file: %w", readErr)
		}
		if len(ca) > 256*1024 {
			return nil, errors.New("CA bundle is larger than 256 KiB")
		}
		profile["ca_pem"] = string(ca)
	}
	dataRoot, err := filepath.Abs(o.DataRoot)
	if err != nil {
		return nil, err
	}
	profilePath := filepath.Join(dataRoot, "captain.json")
	if err = Save(profilePath, profile); err != nil {
		return nil, err
	}
	serveCommand, err := remoteCommand(o, "serve", "--state", o.RemoteState, "--host", "127.0.0.1", "--port", strconv.Itoa(o.RemotePort), "--cert", o.RemoteCert, "--key", o.RemoteKey, "--tls-host", "0.0.0.0", "--tls-port", strconv.Itoa(o.TLSPort))
	if err != nil {
		return nil, err
	}
	logQuoted, err := remoteShellQuote(o.RemoteLog)
	if err != nil {
		return nil, err
	}
	launch := "nohup " + serveCommand + " > " + logQuoted + " 2>&1 < /dev/null & echo $!"
	pidRaw, err := runRemote(ctx, o, launch)
	if err != nil {
		return nil, fmt.Errorf("start remote collaboration service: %w", err)
	}
	pid := 0
	for _, line := range strings.Split(strings.TrimSpace(string(pidRaw)), "\n") {
		if n, parseErr := strconv.Atoi(strings.TrimSpace(line)); parseErr == nil {
			pid = n
		}
	}
	cfg := TeamConfig{Protocol: "otoha-team/1", Name: o.Name, Mode: "server", URL: strings.TrimRight(o.Endpoint, "/"), Project: o.Project, Channel: o.Channel, State: o.RemoteState, Profile: profilePath, ServerPID: pid, ServerLog: o.RemoteLog, CreatedAt: time.Now().UTC().Format(time.RFC3339), StartedBy: "otoha team init --mode server"}
	if err = saveTeamConfig(cfg); err != nil {
		return nil, err
	}
	client := NewClient(profile, nil, false)
	defer client.Transport.Close()
	if err = ConfigureClientTLS(client); err != nil {
		return nil, err
	}
	healthCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	if _, err = client.Call(healthCtx, http.MethodGet, "/health", nil); err != nil {
		return nil, fmt.Errorf("remote HTTPS health check: %w", err)
	}
	if err = ensureTeamProject(ctx, client, cfg.Project, cfg.Channel); err != nil {
		return nil, fmt.Errorf("create remote team project/channel: %w", err)
	}
	result := Object{"initialized": true, "started": true, "team": o.Name, "mode": "server", "url": cfg.URL, "project": cfg.Project, "channel": cfg.Channel, "profile": profilePath, "server_pid": pid, "remote_log": o.RemoteLog, "ssh_target": o.SSHTarget, "tls": true}
	return result, nil
}

func teamInitCommand(ctx context.Context, args []string) (Object, error) {
	if len(args) < 1 {
		return nil, errors.New("otoha team init NAME [--mode local|server] [options]")
	}
	name := args[0]
	if err := Name(name); err != nil {
		return nil, err
	}
	f := teamFlagSet("team init")
	mode := f.String("mode", "local", "local or server")
	data := f.String("data", "", "private local team data directory")
	owner := f.String("owner", "captain", "captain actor name")
	project := f.String("project", name, "server project id")
	channel := f.String("channel", "", "default project channel")
	endpoint := f.String("url", "", "service URL; local mode defaults to loopback")
	port := f.Int("port", 0, "local service port; 0 chooses a free port")
	start := f.Bool("start", true, "start the local collaboration service")
	minFree := f.Int64("min-free-bytes", 2*1024*1024*1024, "storage reserve passed to the service")
	sshTarget := f.String("ssh-target", "", "remote SSH target USER@HOST for server mode")
	sshKey := f.String("ssh-key", "", "private SSH key used only for provisioning")
	knownHosts := f.String("known-hosts", "", "pinned SSH known_hosts file")
	sshPort := f.Int("ssh-port", 22, "remote SSH port")
	remoteScript := f.String("remote-script", "", "remote collaboration server Python path")
	remotePython := f.String("remote-python", "python3", "remote Python executable")
	remoteState := f.String("remote-state", "", "remote private service state directory")
	remoteCredentials := f.String("remote-credentials", "", "remote owner credentials file")
	remoteCert := f.String("remote-cert", "", "remote TLS certificate chain")
	remoteKey := f.String("remote-tls-key", "", "remote TLS private key")
	remoteLog := f.String("remote-log", "", "remote service log path")
	tlsPort := f.Int("tls-port", 443, "remote TLS listener port")
	remotePort := f.Int("remote-port", 8788, "remote loopback listener port")
	caFile := f.String("ca-file", "", "optional PEM CA bundle for the remote TLS service")
	if err := f.Parse(args[1:]); err != nil {
		return nil, err
	}
	if *mode != "local" && *mode != "server" {
		return nil, errors.New("--mode must be local or server")
	}
	if err := Name(*owner); err != nil {
		return nil, fmt.Errorf("invalid owner: %w", err)
	}
	if err := Name(*project); err != nil {
		return nil, fmt.Errorf("invalid project: %w", err)
	}
	if *channel == "" {
		*channel = *project + "-general"
	}
	if err := Name(*channel); err != nil {
		return nil, fmt.Errorf("invalid channel: %w", err)
	}
	if _, err := os.Stat(teamConfigPath(name)); err == nil {
		return nil, fmt.Errorf("team %q is already initialized; refusing to replace it", name)
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	dataRoot := *data
	if dataRoot == "" {
		dataRoot = teamRoot(name)
	}
	if *mode == "server" {
		if *start == false {
			return nil, errors.New("server mode always starts the remotely provisioned service")
		}
		if *remoteState == "" {
			return nil, errors.New("server mode requires --remote-state")
		}
		return remoteTeamInitCommand(ctx, remoteTeamOptions{Name: name, Owner: *owner, Project: *project, Channel: *channel, Endpoint: strings.TrimRight(*endpoint, "/"), DataRoot: dataRoot, MinFreeBytes: *minFree, SSHTarget: *sshTarget, SSHKey: *sshKey, KnownHosts: *knownHosts, RemoteScript: *remoteScript, RemotePython: *remotePython, RemoteState: *remoteState, RemoteCredentials: *remoteCredentials, RemoteCert: *remoteCert, RemoteKey: *remoteKey, RemoteLog: *remoteLog, SSHPort: *sshPort, TLSPort: *tlsPort, RemotePort: *remotePort, CAFile: *caFile})
	}
	dataRoot, err := filepath.Abs(dataRoot)
	if err != nil {
		return nil, err
	}
	if *port == 0 {
		*port, err = freeLoopbackPort()
		if err != nil {
			return nil, err
		}
	}
	if *port < 1 || *port > 65535 {
		return nil, errors.New("port must be between 1 and 65535")
	}
	if *endpoint == "" {
		*endpoint = fmt.Sprintf("http://127.0.0.1:%d", *port)
	}
	u, err := url.Parse(*endpoint)
	if err != nil || u.Host == "" || u.Scheme != "http" {
		return nil, errors.New("local mode currently requires an HTTP loopback origin")
	}
	if u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost" && u.Hostname() != "::1" {
		return nil, errors.New("local mode HTTP URL must bind to loopback")
	}
	cfg := TeamConfig{Protocol: "otoha-team/1", Name: name, Mode: "local", URL: strings.TrimRight(*endpoint, "/"), Project: *project, Channel: *channel, State: filepath.Join(dataRoot, "server"), Profile: filepath.Join(dataRoot, "captain.json"), ServerLog: filepath.Join(dataRoot, "server.log"), CreatedAt: time.Now().UTC().Format(time.RFC3339), StartedBy: "otoha team init"}
	cfg.ManageCenter = *start
	if script := serverScript(); script != "" {
		cfg.ServerScript, err = filepath.Abs(script)
	} else {
		return nil, errors.New("cannot find the packaged collaboration service; set OTOHA_SERVER_SCRIPT")
	}
	if err != nil {
		return nil, err
	}
	python, err := exec.LookPath(pythonCommand())
	if err != nil {
		return nil, fmt.Errorf("local hosting requires Python; set OTOHA_PYTHON: %w", err)
	}
	cfg.Python, err = filepath.Abs(python)
	if err != nil {
		return nil, err
	}
	if err = serverInit(ctx, cfg, *owner, *minFree); err != nil {
		return nil, err
	}
	profile, err := Load(cfg.Profile)
	if err != nil {
		return nil, err
	}
	profile["mode"], profile["project"], profile["channel"] = "owner", cfg.Project, cfg.Channel
	if err = Save(cfg.Profile, profile); err != nil {
		return nil, err
	}
	if !*start {
		if err = saveTeamConfig(cfg); err != nil {
			return nil, err
		}
		return Object{"initialized": true, "started": false, "team": name, "mode": cfg.Mode, "url": cfg.URL, "project": cfg.Project, "channel": cfg.Channel, "profile": cfg.Profile, "state": "initialized"}, nil
	}
	pid, err := startLocalTeamServer(ctx, cfg, *minFree)
	if err != nil {
		return nil, err
	}
	cfg.ServerPID = pid
	if err = saveTeamConfig(cfg); err != nil {
		return nil, err
	}
	client := NewClient(profile, nil, false)
	defer client.Transport.Close()
	if err = ensureTeamProject(ctx, client, cfg.Project, cfg.Channel); err != nil {
		return nil, fmt.Errorf("create team project/channel: %w", err)
	}
	return Object{"initialized": true, "started": true, "team": name, "mode": cfg.Mode, "url": cfg.URL, "project": cfg.Project, "channel": cfg.Channel, "profile": cfg.Profile, "state": "running", "server_pid": pid, "server_log": cfg.ServerLog}, nil
}

func teamInviteCommand(ctx context.Context, args []string) (Object, error) {
	if len(args) < 2 {
		return nil, errors.New("otoha team invite TEAM MEMBER [--role member|admin|reader] [--out FILE]")
	}
	name, actor := args[0], args[1]
	if err := Name(name); err != nil {
		return nil, err
	}
	if err := Name(actor); err != nil {
		return nil, fmt.Errorf("invalid member: %w", err)
	}
	f := teamFlagSet("team invite")
	role := f.String("role", "member", "reader, member or admin")
	days := f.Int("days", 1, "invitation lifetime in days")
	display := f.String("display-name", actor, "member display name")
	out := f.String("out", "", "private invitation file")
	repository := f.String("repository", "", "credential-free Git repository URL")
	branch := f.String("branch", "", "optional repository branch")
	if err := f.Parse(args[2:]); err != nil {
		return nil, err
	}
	if *role != "reader" && *role != "member" && *role != "admin" {
		return nil, errors.New("role must be reader, member or admin")
	}
	if *days < 1 || *days > 7 {
		return nil, errors.New("days must be between 1 and 7")
	}
	if err := validateJoinRepository(Object{"url": *repository, "branch": *branch}); err != nil {
		return nil, err
	}
	cfg, err := loadTeamConfig(name)
	if err != nil {
		return nil, err
	}
	profile, client, err := teamProfile(cfg)
	if err != nil {
		return nil, err
	}
	defer client.Transport.Close()
	if *out == "" {
		*out = filepath.Join(teamRoot(name), "invites", actor+".json")
	}
	if _, err = os.Stat(*out); err == nil {
		return nil, fmt.Errorf("invitation file exists; refusing to replace: %s", *out)
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	inv, err := client.Call(ctx, http.MethodPost, "/v1/invites", Object{"actor": actor, "name": *display, "grants": Object{cfg.Project: *role}, "ttl_seconds": *days * 86400})
	if err != nil {
		return nil, err
	}
	envelope := Object{"protocol": "otoha-collaboration/1", "url": S(inv, "url"), "actor": actor, "project": cfg.Project, "channel": cfg.Channel, "invitation": S(inv, "invitation"), "expires": inv["expires"], "grants": inv["grants"]}
	if ca := S(profile, "ca_pem"); ca != "" && strings.HasPrefix(S(inv, "url"), "https://") {
		envelope["ca_pem"] = ca
	}
	if *repository != "" {
		envelope["repository"] = Object{"url": *repository, "branch": *branch}
	}
	if err = Save(*out, envelope); err != nil {
		return nil, err
	}
	return Object{"invited": true, "team": name, "actor": actor, "role": *role, "project": cfg.Project, "channel": cfg.Channel, "invitation_file": filepath.Clean(*out), "expires": inv["expires"], "owner_profile_used": profile["actor"]}, nil
}

func teamJoinCommand(ctx context.Context, args []string) (Object, error) {
	f := teamFlagSet("team join")
	invite := f.String("invite", "", "private invitation JSON file")
	endpoint := f.String("endpoint", "", "public HTTPS team endpoint")
	key := f.String("key", "", "personal one-time invitation key")
	caFile := f.String("ca-file", "", "explicit CA certificate for --endpoint")
	device := f.String("device", "", "local device name")
	directory := f.String("directory", "", "local repository checkout directory")
	gatewayPath := f.String("gateway-profile", os.Getenv("OTOHA_GATEWAY_PROFILE"), "machine-preconfigured private SSH profile (or OTOHA_GATEWAY_PROFILE); relative key/known_hosts paths resolve from this file's directory")
	giteaPath := f.String("gitea-profile", "", "existing private Gitea profile used only for this repository's HTTP(S) credentials")
	if err := f.Parse(args); err != nil {
		return nil, err
	}
	keyMode := *endpoint != "" || *key != ""
	if f.NArg() != 0 || (*invite != "" && keyMode) || (*invite == "" && !keyMode) || (keyMode && (*endpoint == "" || *key == "")) || (!keyMode && *caFile != "") {
		return nil, errors.New("otoha join --invite FILE, or otoha join --endpoint HTTPS_URL --key KEY")
	}
	if keyMode {
		gatewayExplicit := false
		f.Visit(func(flag *flag.Flag) { gatewayExplicit = gatewayExplicit || flag.Name == "gateway-profile" })
		if gatewayExplicit {
			return nil, errors.New("--endpoint uses direct HTTPS; --gateway-profile applies only to loopback invitations")
		}
		// A public endpoint does not inherit an unrelated local SSH route.
		*gatewayPath = ""
		path, resolveErr := resolveJoinKey(ctx, *endpoint, *key, *caFile)
		if resolveErr != nil {
			return nil, resolveErr
		}
		*invite = path
	}
	inv, err := Load(*invite)
	if err != nil {
		return nil, err
	}
	var gateway Object
	if *gatewayPath != "" {
		gateway, err = loadLocalTeamGateway(*gatewayPath)
		if err != nil {
			return nil, fmt.Errorf("read local gateway profile: %w", err)
		}
		if err = validateTeamGateway(S(inv, "url"), gateway); err != nil {
			return nil, err
		}
	}
	repository := O(inv, "repository")
	if err = validateJoinRepository(repository); err != nil {
		return nil, err
	}
	project := S(inv, "project")
	if project == "" {
		project = "team"
	}
	if err = Name(project); err != nil {
		return nil, err
	}
	actor := S(inv, "actor")
	if err = Name(actor); err != nil {
		return nil, err
	}
	profilePath := filepath.Join(Home(), "teams", project, "members", actor, "identity.json")
	previous, previousErr := loadTeamConfig(project)
	if previousErr == nil {
		if previous.Mode != "member" || filepath.Clean(previous.Profile) != filepath.Clean(profilePath) || strings.TrimRight(previous.URL, "/") != strings.TrimRight(S(inv, "url"), "/") {
			return nil, errors.New("team name is already used by another authority or identity; existing project preserved")
		}
		if previous.RepositoryURL != "" && S(previous.Repository, "transport") != "otoha" && (previous.RepositoryURL != S(repository, "url") || previous.RepositoryBranch != S(repository, "branch")) {
			return nil, errors.New("invitation repository differs from the existing team checkout; existing project preserved")
		}
	} else if !os.IsNotExist(previousErr) {
		return nil, previousErr
	}
	if *giteaPath == "" && previousErr == nil {
		*giteaPath = previous.GiteaProfile
	}
	if *giteaPath != "" {
		*giteaPath, _, err = joinedGiteaProfile(*giteaPath, S(repository, "url"))
		if err != nil {
			return nil, fmt.Errorf("local Gitea credentials are not applicable to this repository: %w", err)
		}
	}
	result, err := teamJoin(ctx, *invite, *device, gateway)
	if err != nil {
		return nil, err
	}
	project, actor, profilePath = S(result, "project"), S(result, "actor"), S(result, "profile")
	cfg := TeamConfig{Protocol: "otoha-team/1", Name: project, Mode: "member", URL: S(result, "url"), Project: project, Channel: S(result, "channel"), State: "joined", Profile: profilePath, CreatedAt: time.Now().UTC().Format(time.RFC3339), StartedBy: "otoha team join", RepositoryURL: S(repository, "url"), RepositoryBranch: S(repository, "branch")}
	cfg.GiteaProfile = *giteaPath
	if previousErr == nil {
		cfg.CreatedAt, cfg.Checkout = previous.CreatedAt, previous.Checkout
		cfg.State = previous.State
		cfg.Repository = previous.Repository
		if S(previous.Repository, "transport") == "otoha" {
			cfg.RepositoryURL = previous.RepositoryURL
		}
	}
	if err = saveTeamConfig(cfg); err != nil {
		// The enrollment itself succeeded; report the durable identity path while
		// preserving the error instead of pretending local team metadata exists.
		return result, fmt.Errorf("joined as %s but could not save team metadata: %w", actor, err)
	}
	readiness := Object{"identity": "ready", "project": "not_checked", "repository": "not_configured", "service": "not_started", "communication": "not_connected"}
	result["readiness"], result["ready"] = readiness, false
	if err = verifyJoinedProject(ctx, cfg); err != nil {
		readiness["project"] = "error"
		return result, fmt.Errorf("identity joined; project authorization is not ready: %w", err)
	}
	readiness["project"] = "ready"
	managed, err := discoverJoinedRepository(ctx, cfg)
	if err != nil {
		readiness["repository"] = "error"
		return result, fmt.Errorf("project joined; repository discovery failed: %w", err)
	}
	if managed != nil && cfg.GiteaProfile == "" && S(repository, "url") == "" {
		return finishManagedTeamJoin(ctx, cfg, managed, *directory, result, readiness)
	}
	if S(cfg.Repository, "transport") == "otoha" {
		readiness["repository"] = "error"
		return result, errors.New("the existing Otoha repository binding is unavailable or conflicts with a direct repository; existing checkout preserved")
	}
	if cfg.RepositoryURL == "" {
		if *directory != "" {
			return result, errors.New("project has no bound repository for --directory")
		}
		// Authorities without a repository binding retain identity-only join.
		return result, nil
	}
	checkout := *directory
	if checkout == "" {
		checkout = cfg.Checkout
	}
	if checkout == "" {
		checkout = filepath.Join(teamRoot(project), "checkout")
	}
	checkout, err = filepath.Abs(checkout)
	if err != nil {
		return result, err
	}
	if cfg.Checkout != "" && filepath.Clean(cfg.Checkout) != checkout {
		return result, errors.New("team already has another checkout; existing directory preserved")
	}
	cfg.Checkout = checkout
	teamClientContext(result, cfg)
	if err = saveTeamConfig(cfg); err != nil {
		return result, err
	}
	if err = checkoutTeamRepository(ctx, repository, checkout, cfg.GiteaProfile); err != nil {
		readiness["repository"] = "error"
		result["next"] = "check repository access, then repeat the same team join command"
		return result, fmt.Errorf("identity joined; repository is not ready: %w", err)
	}
	if err = ensureJoinedRepositoryIdentity(ctx, checkout, cfg.Profile); err != nil {
		readiness["repository"] = "error"
		return result, err
	}
	cfg.Checkout = checkout
	readiness["repository"], result["checkout"] = "ready", checkout
	if err = saveTeamConfig(cfg); err != nil {
		return result, err
	}
	page, err := ensureJoinedPageService(ctx, project)
	if err != nil {
		readiness["service"] = "error"
		result["next"] = "inspect otoha daemon status, then repeat the same team join command"
		return result, fmt.Errorf("identity and repository ready; local service is not ready: %w", err)
	}
	readiness["service"], readiness["communication"] = "ready", S(page, "connection")
	result["page"], result["ready"] = page, B(page, "ready")
	result["next"] = S(O(result, "commands"), "page")
	if B(page, "ready") {
		cfg.State = "ready"
	}
	if err = saveTeamConfig(cfg); err != nil {
		return result, err
	}
	return result, nil
}

func teamStatusCommand(ctx context.Context, args []string) (Object, error) {
	f := teamFlagSet("team status")
	nameFlag := f.String("team", "", "team name")
	if err := f.Parse(args); err != nil {
		return nil, err
	}
	name := *nameFlag
	if name == "" && len(f.Args()) > 0 {
		name = f.Args()[0]
	}
	if name == "" {
		var err error
		name, err = discoverTeamName()
		if err != nil {
			return nil, err
		}
	}
	cfg, err := loadTeamConfig(name)
	if err != nil {
		return nil, err
	}
	_, client, err := teamProfile(cfg)
	if err != nil {
		return nil, err
	}
	defer client.Transport.Close()
	serverPID := localTeamPID(cfg)
	var processAliveValue any
	if cfg.Mode == "local" {
		processAliveValue = processAlive(serverPID)
	}
	alive := cfg.Mode != "local" || processAlive(serverPID)
	state := cfg.State
	if cfg.Mode == "local" {
		if alive {
			state = "running"
		} else {
			state = "stopped"
		}
	}
	result := Object{"team": name, "mode": cfg.Mode, "url": cfg.URL, "project": cfg.Project, "channel": cfg.Channel, "state": state, "profile": cfg.Profile, "server_pid": serverPID, "process_alive": processAliveValue}
	teamClientContext(result, cfg)
	if cfg.ServerLog != "" {
		result["server_log"] = cfg.ServerLog
	}
	if _, healthErr := client.Call(ctx, http.MethodGet, "/health", nil); healthErr != nil {
		result["healthy"] = false
		result["health_error"] = healthErr.Error()
		return result, nil
	}
	manifest, err := client.Call(ctx, http.MethodGet, "/v1/manifest", nil)
	if err != nil {
		result["healthy"] = false
		result["health_error"] = err.Error()
		return result, nil
	}
	result["healthy"] = true
	result["actor"] = O(manifest, "actor")
	result["grants"] = manifest["grants"]
	result["capabilities"] = manifest["capabilities"]
	return result, nil
}

func teamDoctorCommand(ctx context.Context, args []string) (Object, error) {
	f := teamFlagSet("team doctor")
	nameFlag := f.String("team", "", "team name")
	if err := f.Parse(args); err != nil {
		return nil, err
	}
	name := *nameFlag
	if name == "" && len(f.Args()) > 0 {
		name = f.Args()[0]
	}
	if name == "" {
		var err error
		name, err = discoverTeamName()
		if err != nil {
			return nil, err
		}
	}
	result, err := teamDoctor(ctx, name)
	if err != nil {
		return result, err
	}
	if !B(result, "ok") {
		return result, errors.New("team doctor found failing checks")
	}
	return result, nil
}

// TeamCommand is the stable orchestration entry point for the native CLI.
// Remote SSH/TLS provisioning deliberately returns a clear unsupported error
// until its host-specific service manager and certificate checks are ready.
func TeamCommand(ctx context.Context, args []string) (Object, error) {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		return teamCommandHelp(), nil
	}
	for _, arg := range args[1:] {
		if arg == "--help" || arg == "-h" {
			if args[0] == "join" {
				return JoinCommandHelp(), nil
			}
			return teamCommandHelp(), nil
		}
	}
	switch args[0] {
	case "init":
		return teamInitCommand(ctx, args[1:])
	case "invite":
		return teamInviteCommand(ctx, args[1:])
	case "join":
		return teamJoinCommand(ctx, args[1:])
	case "status":
		return teamStatusCommand(ctx, args[1:])
	case "doctor":
		return teamDoctorCommand(ctx, args[1:])
	case "wait":
		return teamWaitCommand(ctx, args[1:])
	default:
		return nil, fmt.Errorf("unknown team action %q", args[0])
	}
}

func teamCommandHelp() Object {
	return Object{"usage": "otoha team init|invite|join|status|doctor|wait", "examples": []string{"otoha join --endpoint https://team.example.com --key KEY", "otoha join --invite FILE", "otoha team init NAME", "otoha team invite TEAM MEMBER --out INVITE.json", "otoha team status TEAM", "otoha team doctor TEAM", "otoha team wait TEAM --timeout 60"}, "join_options": Object{"--endpoint HTTPS_URL --key KEY": "join directly with a personal one-time key", "--directory DIR": "checkout target; defaults to this team's private checkout", "--gateway-profile FILE": "optional machine-preconfigured route for a loopback invitation; OTOHA_GATEWAY_PROFILE is also supported", "--gitea-profile FILE": "advanced direct repository access only; not required for an Otoha-bound repository"}}
}

func JoinCommandHelp() Object {
	return Object{"usage": "otoha join --endpoint HTTPS_URL --key KEY [--directory DIR], or otoha join --invite FILE", "example": "otoha join --endpoint https://team.example.com --key KEY", "result": "joins the project, starts the local service and checks out its bound repository; status and commands are returned as JSON", "deployment": "HTTPS is direct; loopback invitation files can use OTOHA_GATEWAY_PROFILE or --gateway-profile FILE", "advanced": []string{"--ca-file FILE for an explicitly trusted HTTPS CA", "--gitea-profile FILE for an explicitly configured direct repository", "otoha join KEY --agent current for historical packed invitations"}}
}
