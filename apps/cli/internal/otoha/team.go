package otoha

import (
	"context"
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
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// TeamConfig is the local, private control record for a team authority or
// member. It intentionally contains a profile path, never the profile data.
type TeamConfig struct {
	Protocol  string `json:"protocol"`
	Name      string `json:"name"`
	Mode      string `json:"mode"`
	URL       string `json:"url"`
	Project   string `json:"project"`
	Channel   string `json:"channel"`
	State     string `json:"state"`
	Profile   string `json:"profile"`
	ServerPID int    `json:"server_pid,omitempty"`
	ServerLog string `json:"server_log,omitempty"`
	CreatedAt string `json:"created_at"`
	StartedBy string `json:"started_by,omitempty"`
}

func teamRoot(name string) string       { return filepath.Join(Home(), "teams", name) }
func teamConfigPath(name string) string { return filepath.Join(teamRoot(name), "team.json") }

func saveTeamConfig(cfg TeamConfig) error {
	return Save(teamConfigPath(cfg.Name), Object{
		"protocol": cfg.Protocol, "name": cfg.Name, "mode": cfg.Mode,
		"url": cfg.URL, "project": cfg.Project, "channel": cfg.Channel,
		"state": cfg.State, "profile": cfg.Profile, "server_pid": cfg.ServerPID,
		"server_log": cfg.ServerLog, "created_at": cfg.CreatedAt, "started_by": cfg.StartedBy,
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
		State: S(o, "state"), Profile: S(o, "profile"), ServerPID: N(o, "server_pid"),
		ServerLog: S(o, "server_log"), CreatedAt: S(o, "created_at"), StartedBy: S(o, "started_by"),
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
	return p, NewClient(p, nil, false), nil
}

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
	return ""
}

func pythonCommand() string {
	if p := os.Getenv("OTOHA_PYTHON"); p != "" {
		return p
	}
	return "python3"
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
	script := serverScript()
	if script == "" {
		return 0, errors.New("cannot find services/collaboration/otoha_server.py; set OTOHA_SERVER_SCRIPT")
	}
	if _, err := os.Stat(script); err != nil {
		return 0, err
	}
	if _, err := exec.LookPath(pythonCommand()); err != nil {
		return 0, fmt.Errorf("python runtime %q is unavailable: %w", pythonCommand(), err)
	}
	if err := PrivateDir(filepath.Dir(cfg.ServerLog)); err != nil {
		return 0, err
	}
	log, err := os.OpenFile(cfg.ServerLog, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return 0, err
	}
	cmd := exec.Command(pythonCommand(), script, "serve", "--state", cfg.State, "--host", "127.0.0.1", "--port", portFromURL(cfg.URL))
	cmd.Stdout, cmd.Stderr = log, log
	cmd.Stdin = nil
	if err = cmd.Start(); err != nil {
		log.Close()
		return 0, err
	}
	pid := cmd.Process.Pid
	_ = cmd.Process.Release()
	_ = log.Close()
	if err = waitHealth(ctx, cfg.URL); err != nil {
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
	script := serverScript()
	if script == "" {
		return errors.New("cannot find collaboration server script")
	}
	args := []string{script, "init", "--state", cfg.State, "--owner", owner, "--url", cfg.URL, "--credentials", cfg.Profile, "--min-free-bytes", strconv.FormatInt(minFreeBytes, 10)}
	cmd := exec.CommandContext(ctx, pythonCommand(), args...)
	cmd.Env = append(os.Environ(), "PYTHONUNBUFFERED=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("server init: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func teamJoin(ctx context.Context, invitePath, device string) (Object, error) {
	inv, err := Load(invitePath)
	if err != nil {
		return nil, err
	}
	if S(inv, "protocol") != "otoha-collaboration/1" || S(inv, "url") == "" || S(inv, "invitation") == "" || S(inv, "actor") == "" {
		return nil, errors.New("invalid Otoha collaboration invitation")
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
	root := filepath.Join(Home(), "teams", project, "members", actor)
	profilePath := filepath.Join(root, "identity.json")
	if old, loadErr := Load(profilePath); loadErr == nil {
		client := NewClient(old, nil, false)
		defer client.Transport.Close()
		manifest, callErr := client.Call(ctx, http.MethodGet, "/v1/manifest", nil)
		if callErr != nil {
			return nil, fmt.Errorf("existing member identity is not usable: %w", callErr)
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
	client := NewClient(Object{"url": S(inv, "url")}, nil, false)
	defer client.Transport.Close()
	enrolled, err := client.Call(ctx, http.MethodPost, "/v1/enroll", pending)
	if err != nil {
		return nil, fmt.Errorf("enrollment: %w", err)
	}
	profile := Object{"protocol": "otoha-collaboration/1", "url": S(inv, "url"), "actor": actor, "device": S(enrolled, "device"), "token": S(pending, "credential"), "project": project, "channel": channel, "mode": "member"}
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

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	return syscall.Kill(pid, 0) == nil
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
	if _, err := exec.LookPath(pythonCommand()); err != nil {
		add("python", false, err.Error())
	} else {
		add("python", true, pythonCommand())
	}
	script := serverScript()
	if script == "" {
		add("server_script", false, "set OTOHA_SERVER_SCRIPT or run from the repository")
	} else if _, err := os.Stat(script); err != nil {
		add("server_script", false, err.Error())
	} else {
		add("server_script", true, script)
	}
	cfg, err := loadTeamConfig(name)
	if err != nil {
		add("team_config", false, err.Error())
		return Object{"ok": ok, "team": name, "checks": checks}, nil
	}
	add("team_config", true, teamConfigPath(name))
	if cfg.Mode == "local" {
		if st, statErr := os.Stat(cfg.State); statErr != nil || !st.IsDir() {
			add("state_dir", false, cfg.State)
		} else {
			add("state_dir", true, cfg.State)
		}
	} else {
		add("state_dir", true, "not applicable for member mode")
	}
	if _, err = Load(cfg.Profile); err != nil {
		add("owner_profile", false, err.Error())
	} else {
		add("owner_profile", true, cfg.Profile)
	}
	if cfg.Mode == "local" {
		if cfg.ServerPID > 0 && processAlive(cfg.ServerPID) {
			add("server_process", true, strconv.Itoa(cfg.ServerPID))
		} else {
			add("server_process", false, "server process is not alive")
		}
	} else {
		add("server_process", true, "not applicable for member mode")
	}
	if healthErr := waitHealth(ctx, cfg.URL); healthErr != nil {
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

func teamInitCommand(ctx context.Context, args []string) (Object, error) {
	if len(args) < 1 {
		return nil, errors.New("otoha team init NAME [--mode local] [--data DIR] [--port PORT]")
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
	if err := f.Parse(args[1:]); err != nil {
		return nil, err
	}
	if *mode != "local" {
		return nil, errors.New("team init currently supports --mode local; remote SSH/TLS provisioning is reserved for the next stage")
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
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, errors.New("url must be an HTTP(S) origin")
	}
	if u.Scheme == "http" && u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost" && u.Hostname() != "::1" {
		return nil, errors.New("local mode HTTP URL must bind to loopback")
	}
	cfg := TeamConfig{Protocol: "otoha-team/1", Name: name, Mode: "local", URL: strings.TrimRight(*endpoint, "/"), Project: *project, Channel: *channel, State: filepath.Join(dataRoot, "server"), Profile: filepath.Join(dataRoot, "captain.json"), ServerLog: filepath.Join(dataRoot, "server.log"), CreatedAt: time.Now().UTC().Format(time.RFC3339), StartedBy: "otoha team init"}
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
	if err := f.Parse(args[2:]); err != nil {
		return nil, err
	}
	if *role != "reader" && *role != "member" && *role != "admin" {
		return nil, errors.New("role must be reader, member or admin")
	}
	if *days < 1 || *days > 7 {
		return nil, errors.New("days must be between 1 and 7")
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
	if err = Save(*out, envelope); err != nil {
		return nil, err
	}
	return Object{"invited": true, "team": name, "actor": actor, "role": *role, "project": cfg.Project, "channel": cfg.Channel, "invitation_file": filepath.Clean(*out), "expires": inv["expires"], "owner_profile_used": profile["actor"]}, nil
}

func teamJoinCommand(ctx context.Context, args []string) (Object, error) {
	f := teamFlagSet("team join")
	invite := f.String("invite", "", "private invitation JSON file")
	device := f.String("device", "", "local device name")
	if err := f.Parse(args); err != nil {
		return nil, err
	}
	if *invite == "" {
		return nil, errors.New("otoha team join --invite FILE")
	}
	result, err := teamJoin(ctx, *invite, *device)
	if err != nil {
		return nil, err
	}
	project := S(result, "project")
	actor := S(result, "actor")
	profilePath := S(result, "profile")
	if err = saveTeamConfig(TeamConfig{Protocol: "otoha-team/1", Name: project, Mode: "member", URL: S(result, "url"), Project: project, Channel: S(result, "channel"), State: "joined", Profile: profilePath, CreatedAt: time.Now().UTC().Format(time.RFC3339), StartedBy: "otoha team join"}); err != nil {
		// The enrollment itself succeeded; report the durable identity path while
		// preserving the error instead of pretending local team metadata exists.
		return result, fmt.Errorf("joined as %s but could not save team metadata: %w", actor, err)
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
	var processAliveValue any
	if cfg.Mode == "local" {
		processAliveValue = processAlive(cfg.ServerPID)
	}
	alive := cfg.Mode != "local" || processAlive(cfg.ServerPID)
	state := cfg.State
	if cfg.Mode == "local" {
		if alive {
			state = "running"
		} else {
			state = "stopped"
		}
	}
	result := Object{"team": name, "mode": cfg.Mode, "url": cfg.URL, "project": cfg.Project, "channel": cfg.Channel, "state": state, "profile": cfg.Profile, "server_pid": cfg.ServerPID, "process_alive": processAliveValue}
	if cfg.ServerLog != "" {
		result["server_log"] = cfg.ServerLog
	}
	if healthErr := waitHealth(ctx, cfg.URL); healthErr != nil {
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
	if len(args) == 0 {
		return nil, errors.New("otoha team init|invite|join|status|doctor")
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
	default:
		return nil, fmt.Errorf("unknown team action %q", args[0])
	}
}
