package otoha

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func InitCommandHelp() Object {
	return Object{"schema": "otoha-init/1", "state": "needs_input",
		"questions": []Object{
			{"id": "name", "question": "这个团队叫什么？"},
			{"id": "owner", "question": "你希望以什么名字管理团队？"},
			{"id": "members", "question": "先邀请哪几位队友？可以暂时留空。"},
			{"id": "storage", "question": "愿意把数据放在这台机器，并在协作时保持它开机吗？", "default": "local"},
		},
		"next":    "otoha init --name TEAM --owner YOU --members ALICE,BOB --storage local",
		"answers": "--answers FILE.json or --answers '{\"name\":\"TEAM\",\"owner\":\"YOU\",\"members\":[\"ALICE\"],\"storage\":\"local\"}'",
		"scope":   "本版自动建立这台机器上的团队并接通默认公网中继；主机需要保持在线。CLI 会下载依赖、验证连接并给出个人邀请，不需要手动配置数据库或隧道。已有团队请使用 join。"}
}

func initPrivateToken() (string, error) {
	data := make([]byte, 32)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func initIdentifier(value, fallback string) string {
	if Name(value) == nil && len(value) <= 32 {
		return value
	}
	var slug strings.Builder
	for _, r := range strings.ToLower(value) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			slug.WriteRune(r)
		} else if slug.Len() > 0 {
			slug.WriteByte('-')
		}
	}
	base := strings.Trim(slug.String(), "-_")
	if base == "" {
		base = fallback
	}
	if base[0] < 'a' || base[0] > 'z' {
		base = fallback + "-" + base
	}
	if len(base) > 23 {
		base = strings.TrimRight(base[:23], "-_")
	}
	sum := sha256.Sum256([]byte(value))
	return base + "-" + hex.EncodeToString(sum[:4])
}

var initResolveRuntimes = resolveInitRuntimes
var initAllocateRoute = allocateInitRoute

func initSessionPath(team string) string {
	return filepath.Join(Home(), "init", team, "session.json")
}

func initPhase(record Object, phase string) error {
	record["phase"], record["updated_at"] = phase, pageTime()
	return Save(initSessionPath(S(record, "team")), record)
}

func initSummary(record Object) Object {
	return Object{"schema": "otoha-init/1", "team": record["team"], "state": record["state"], "phase": record["phase"],
		"started_at": record["started_at"], "updated_at": record["updated_at"], "readiness": record["readiness"],
		"public_url": O(record, "route")["public_url"], "session_file": initSessionPath(S(record, "team")),
		"next": "otoha init --name " + shellQuote(S(O(record, "answers"), "name")),
		"note": "本机运行与公网验证分别记录；主机离线时团队中心不可用，穿透不会让关机设备继续提供服务。"}
}

func initFailure(record Object, err error) (Object, error) {
	// Before the team configuration is published, the daemon cannot discover
	// these processes. Keep the saved intent/data, but stop an incomplete first
	// bootstrap rather than leaving unmanaged runtime children behind.
	if _, configErr := os.Stat(teamConfigPath(S(record, "team"))); os.IsNotExist(configErr) {
		if _, runtimeErr := os.Stat(initRuntimeConfigPath(S(record, "team"))); runtimeErr == nil {
			if stopErr := stopInitRuntime(S(record, "team")); stopErr != nil {
				err = fmt.Errorf("%w; stop incomplete initialization runtime: %v", err, stopErr)
			}
		}
	}
	record["state"], record["last_error"], record["updated_at"] = "needs_action", err.Error(), pageTime()
	if saveErr := Save(initSessionPath(S(record, "team")), record); saveErr != nil {
		err = fmt.Errorf("%w; save recovery state: %v", err, saveErr)
	}
	result := initSummary(record)
	result["error"] = err.Error()
	return result, err
}

func initMembers(value any) ([]string, error) {
	var raw []string
	switch members := value.(type) {
	case nil:
	case string:
		raw = strings.Split(members, ",")
	case []any:
		for _, member := range members {
			name, ok := member.(string)
			if !ok {
				return nil, errors.New("members must contain names")
			}
			raw = append(raw, name)
		}
	default:
		return nil, errors.New("members must be a comma-separated string or a JSON array of names")
	}
	result, seen := []string{}, map[string]bool{}
	for _, member := range raw {
		member = strings.TrimSpace(member)
		if member != "" && !seen[member] {
			seen[member] = true
			result = append(result, member)
		}
	}
	return result, nil
}

func initAnswers(args []string) (Object, error) {
	f := flag.NewFlagSet("init", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	name := f.String("name", "", "team name")
	owner := f.String("owner", "", "your name")
	members := f.String("members", "", "comma-separated teammate names")
	storage := f.String("storage", "local", "local: this machine stores the team and stays online")
	answers := f.String("answers", "", "JSON answers or a JSON file")
	if err := f.Parse(args); err != nil {
		return nil, err
	}
	if f.NArg() != 0 {
		return nil, errors.New("otoha init --name TEAM --owner YOU --members ALICE,BOB --storage local")
	}
	input := Object{}
	if *answers != "" {
		data := []byte(*answers)
		if !strings.HasPrefix(strings.TrimSpace(*answers), "{") {
			var err error
			data, err = os.ReadFile(*answers)
			if err != nil {
				return nil, err
			}
		}
		if err := json.Unmarshal(data, &input); err != nil || input == nil {
			return nil, errors.New("--answers requires a JSON object or a readable JSON file")
		}
		for key := range input {
			if key != "name" && key != "owner" && key != "members" && key != "storage" {
				return nil, fmt.Errorf("unknown answer %q", key)
			}
		}
	}
	f.Visit(func(value *flag.Flag) {
		switch value.Name {
		case "name":
			input["name"] = *name
		case "owner":
			input["owner"] = *owner
		case "members":
			input["members"] = *members
		case "storage":
			input["storage"] = *storage
		}
	})
	if _, ok := input["storage"]; !ok {
		input["storage"] = "local"
	}
	return input, nil
}

func allocateInitRoute(ctx context.Context, request Object) (Object, error) {
	client := NewClient(Object{"url": DefaultRelayEndpoint}, nil, false)
	defer client.Transport.Close()
	result, err := client.Call(ctx, http.MethodPost, "/relay/v1/routes", request)
	if err != nil {
		return nil, err
	}
	route := O(result, "route")
	id := S(route, "id")
	if S(result, "schema") != "otoha-relay/1" || Name(id) != nil || S(route, "public_url") != DefaultRelayEndpoint+"/t/"+id || len(S(route, "token")) < 40 || S(route, "server_name") != "otoha.kessokuu.top" || S(route, "location") != "/t/"+id+"/" {
		return nil, errors.New("default relay returned an invalid route; original enrollment request preserved")
	}
	if S(route, "state") == "revoked" {
		return nil, errors.New("this route was revoked; no replacement identity was created")
	}
	return route, nil
}

func renewInitRoute(ctx context.Context, config Object) error {
	endpoint, id, token := S(config, "relay_url"), S(config, "relay_route_id"), S(config, "relay_token")
	if endpoint == "" && id == "" {
		return nil
	}
	if endpoint != DefaultRelayEndpoint || Name(id) != nil || len(token) < 40 {
		return errors.New("invalid managed relay renewal configuration")
	}
	client := NewClient(Object{"url": endpoint, "token": token}, nil, false)
	defer client.Transport.Close()
	result, err := client.Call(ctx, http.MethodPost, "/relay/v1/routes/"+id+"/renew", Object{})
	if err != nil {
		return err
	}
	route := O(result, "route")
	if S(route, "id") != id || S(route, "state") != "active" {
		return errors.New("managed relay renewal did not confirm the original active route")
	}
	return nil
}

func bootstrapInitCenter(ctx context.Context, cfg TeamConfig, runtime Object, credentials string) error {
	args := []string{S(runtime, "server"), "bootstrap", "--state", cfg.State, "--url", cfg.URL, "--credentials", cfg.Profile,
		"--owner", S(runtime, "owner"), "--project", cfg.Project, "--channel", cfg.Channel, "--repository-name", cfg.Project,
		"--graph-url", S(runtime, "surreal_url"), "--graph-namespace", "otoha", "--graph-database", cfg.Project, "--graph-id", cfg.Project,
		"--graph-credentials", credentials}
	command := exec.CommandContext(ctx, cfg.Python, args...)
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("initialize team data: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func waitInitPublic(ctx context.Context, cfg TeamConfig, owner string) error {
	profile, client, err := teamProfile(cfg)
	if err != nil {
		return err
	}
	defer client.Transport.Close()
	if S(profile, "actor") != owner {
		return errors.New("saved owner identity does not match the initialization session")
	}
	deadline := time.Now().Add(35 * time.Second)
	var last error
	for time.Now().Before(deadline) {
		probe, cancel := context.WithTimeout(ctx, 5*time.Second)
		_, last = client.Call(probe, http.MethodGet, "/health", nil)
		if last == nil {
			var manifest Object
			manifest, last = client.Call(probe, http.MethodGet, "/v1/manifest", nil)
			if last == nil && S(O(manifest, "actor"), "id") != owner {
				last = errors.New("public route reached a different authority identity")
			}
		}
		cancel()
		if last == nil {
			return nil
		}
		if !pagePause(ctx, 200*time.Millisecond) {
			return ctx.Err()
		}
	}
	return fmt.Errorf("public team endpoint is not ready: %w", last)
}

func InitCommand(ctx context.Context, args []string) (Object, error) {
	if len(args) == 0 {
		return InitCommandHelp(), nil
	}
	for _, arg := range args {
		if arg == "help" || arg == "--help" || arg == "-h" {
			return InitCommandHelp(), nil
		}
	}
	input, err := initAnswers(args)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(S(input, "name"))
	if name == "" {
		return InitCommandHelp(), nil
	}
	team := initIdentifier(name, "team")
	record, loadErr := Load(initSessionPath(team))
	if loadErr == nil {
		stored := O(record, "answers")
		for key, value := range input {
			if key == "storage" && value == "local" {
				continue
			}
			previous, _ := json.Marshal(stored[key])
			current, _ := json.Marshal(value)
			if key == "members" {
				a, _ := initMembers(stored[key])
				b, _ := initMembers(value)
				previous, _ = json.Marshal(a)
				current, _ = json.Marshal(b)
			}
			if string(previous) != string(current) {
				return nil, fmt.Errorf("initialization %s already has different %s; original identity preserved", team, key)
			}
		}
		input = stored
	} else if !os.IsNotExist(loadErr) {
		return nil, loadErr
	}
	owner := strings.TrimSpace(S(input, "owner"))
	if owner == "" {
		result := InitCommandHelp()
		result["known"] = input
		return result, nil
	}
	if S(input, "storage") != "local" {
		return nil, errors.New("this version supports --storage local; choose a machine that can stay online, or run init on your existing always-on machine")
	}
	members, err := initMembers(input["members"])
	if err != nil {
		return nil, err
	}
	if len([]rune(name)) > 120 || len([]byte(name)) > 256 {
		return nil, errors.New("team name must fit 120 characters and 256 UTF-8 bytes")
	}
	if record == nil {
		if _, err := os.Stat(teamConfigPath(team)); err == nil {
			return nil, errors.New("team already exists outside this initialization session; use its current status, not a new identity")
		} else if !os.IsNotExist(err) {
			return nil, err
		}
		centerPort, err := freeLoopbackPort()
		if err != nil {
			return nil, err
		}
		surrealPort, err := freeLoopbackPort()
		if err != nil {
			return nil, err
		}
		secret, err := initPrivateToken()
		if err != nil {
			return nil, err
		}
		password, err := initPrivateToken()
		if err != nil {
			return nil, err
		}
		input["name"], input["owner"], input["members"], input["storage"] = name, owner, members, "local"
		record = Object{"schema": "otoha-init/1", "team": team, "answers": input, "owner": initIdentifier(owner, "owner"), "state": "initializing", "started_at": pageTime(), "phase": "prepare",
			"request": Object{"request_id": "init-" + ID(), "enrollment_secret": secret, "label": name}, "center_port": centerPort, "surreal_port": surrealPort, "surreal_password": password, "readiness": Object{}}
		if err = initPhase(record, "prepare"); err != nil {
			return nil, err
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	ready := O(record, "readiness")
	if _, err = exec.LookPath("git"); err != nil {
		return initFailure(record, errors.New("Git is needed to prepare the shared repository; install Git with this machine's normal installer, then resume the same init command"))
	}
	if err = initPhase(record, "runtime"); err != nil {
		return nil, err
	}
	runtimes := O(record, "runtimes")
	if S(runtimes, "root") == "" {
		runtimes, err = initResolveRuntimes(ctx)
		if err != nil {
			return initFailure(record, err)
		}
		if S(runtimes, "root") != "" {
			record["runtimes"] = runtimes
			if err = initPhase(record, "runtime"); err != nil {
				return nil, err
			}
		}
	}
	for _, required := range []string{"server", "python", "surreal", "frpc", "ca_bundle"} {
		if S(runtimes, required) == "" {
			return initFailure(record, fmt.Errorf("downloaded runtime is missing %s", required))
		}
		if S(runtimes, "root") != "" {
			if info, statErr := os.Stat(S(runtimes, required)); statErr != nil || !info.Mode().IsRegular() {
				return initFailure(record, fmt.Errorf("saved initialization runtime %s is unavailable; restore its original release package before resuming", required))
			}
		}
	}
	ready["runtime"] = "ready"
	route := O(record, "route")
	if S(route, "id") == "" {
		if err = initPhase(record, "connect"); err != nil {
			return nil, err
		}
		route, err = initAllocateRoute(ctx, O(record, "request"))
		if err != nil {
			return initFailure(record, err)
		}
		record["route"] = route
		if err = initPhase(record, "configure"); err != nil {
			return nil, err
		}
	}
	if S(route, "state") == "revoked" {
		return initFailure(record, errors.New("team route was revoked; existing identity preserved"))
	}
	root := teamRoot(team)
	credentials := filepath.Join(root, "graph-credentials.json")
	if err = Save(credentials, Object{"username": "otoha", "password": record["surreal_password"]}); err != nil {
		return initFailure(record, err)
	}
	frpcConfig := filepath.Join(root, "frpc.json")
	serverAddr := S(route, "server_addr")
	if serverAddr == "otoha.kessokuu.top" {
		serverAddr = DefaultRelayDialIP
	}
	frp := Object{"serverAddr": serverAddr, "serverPort": route["server_port"], "user": route["id"], "metadatas": Object{"otoha_token": route["token"]},
		"transport": Object{"protocol": "wss", "tls": Object{"enable": true, "useSystemCa": true, "serverName": "otoha.kessokuu.top", "trustedCaFile": runtimes["ca_bundle"]}},
		"proxies":   []Object{{"name": route["proxy_name"], "type": "http", "customDomains": []string{"otoha.kessokuu.top"}, "locations": []string{S(route, "location")}, "localIP": "127.0.0.1", "localPort": N(record, "center_port")}}}
	if err = Save(frpcConfig, frp); err != nil {
		return initFailure(record, err)
	}
	localURL := "http://127.0.0.1:" + strconv.Itoa(N(record, "center_port"))
	runtimeConfig := Object{"schema": 1, "managed": true, "python": runtimes["python"], "center_script": runtimes["server"], "center_state": filepath.Join(root, "center"), "center_url": localURL,
		"frpc": runtimes["frpc"], "frpc_config": frpcConfig, "surreal": runtimes["surreal"], "surreal_url": "http://127.0.0.1:" + strconv.Itoa(N(record, "surreal_port")),
		"surreal_data": filepath.Join(root, "graph-data"), "surreal_username": "otoha", "surreal_password": record["surreal_password"], "relay_url": DefaultRelayEndpoint, "relay_route_id": route["id"], "relay_token": route["token"], "relay_dial_ip": DefaultRelayDialIP}
	if err = Save(filepath.Join(root, "runtime.json"), runtimeConfig); err != nil {
		return initFailure(record, err)
	}
	if err = renewInitRoute(ctx, runtimeConfig); err != nil {
		return initFailure(record, fmt.Errorf("resume the original public route: %w", err))
	}
	if err = initPhase(record, "start_runtime"); err != nil {
		return nil, err
	}
	if _, err = EnsureInitRuntime(ctx, team); err != nil {
		return initFailure(record, err)
	}
	cfg := TeamConfig{Protocol: "otoha-team/1", Name: team, Mode: "local", URL: S(route, "public_url"), LocalURL: localURL, Project: team, Channel: team + "-general", State: filepath.Join(root, "center"), Profile: filepath.Join(root, "owner.json"), ServerLog: filepath.Join(root, "center.log"), CreatedAt: S(record, "started_at"), StartedBy: "otoha init", ServerScript: S(runtimes, "server"), Python: S(runtimes, "python"), ManageCenter: true}
	if previous, e := loadTeamConfig(team); e == nil {
		cfg = previous
		if cfg.URL != S(route, "public_url") || cfg.LocalURL != localURL || cfg.Mode != "local" {
			return initFailure(record, errors.New("saved team differs from this initialization session; original configuration preserved"))
		}
	} else if !os.IsNotExist(e) {
		return initFailure(record, e)
	}
	if err = initPhase(record, "initialize_data"); err != nil {
		return nil, err
	}
	backend := Clone(runtimeConfig)
	backend["server"], backend["owner"] = runtimes["server"], record["owner"]
	if err = bootstrapInitCenter(ctx, cfg, backend, credentials); err != nil {
		return initFailure(record, err)
	}
	if err = saveTeamConfig(cfg); err != nil {
		return initFailure(record, err)
	}
	if reachable, probeErr := probeLocalCenter(ctx, cfg); !reachable {
		// The existing daemon owns the center from its first start, rather
		// than leaving a detached bootstrap process outside its lifecycle.
		if err = InstallReceiver(); err != nil {
			return initFailure(record, err)
		}
		deadline := time.Now().Add(15 * time.Second)
		for {
			connected, currentErr := probeLocalCenter(ctx, cfg)
			if connected && currentErr == nil {
				break
			}
			if connected {
				return initFailure(record, currentErr)
			}
			if time.Now().After(deadline) || !pagePause(ctx, 150*time.Millisecond) {
				return initFailure(record, errors.New("local team service did not become ready; inspect otoha daemon status and logs, then resume the same initialization"))
			}
		}
	} else if probeErr != nil {
		return initFailure(record, probeErr)
	}
	ready["local_center"] = "ready"
	if err = initPhase(record, "verify_public"); err != nil {
		return nil, err
	}
	if err = waitInitPublic(ctx, cfg, S(record, "owner")); err != nil {
		return initFailure(record, err)
	}
	ready["public_connection"], ready["identity"] = "ready", "ready"
	_, client, err := teamProfile(cfg)
	if err != nil {
		return initFailure(record, err)
	}
	graph, err := client.Call(ctx, http.MethodGet, "/v1/graph/manifest?project="+url.QueryEscape(team), nil)
	client.Transport.Close()
	if err != nil {
		return initFailure(record, err)
	}
	ready["graph"] = "ready"
	if err = initPhase(record, "invite"); err != nil {
		return nil, err
	}
	invitations := []Object{}
	for _, member := range members {
		actor := initIdentifier(member, "member")
		if actor == S(record, "owner") {
			continue
		}
		invitePath := filepath.Join(root, "invites", actor+".json")
		inv, readErr := Load(invitePath)
		if os.IsNotExist(readErr) {
			_, err = teamInviteCommand(ctx, []string{team, actor, "--display-name", member, "--out", invitePath})
			if err != nil {
				return initFailure(record, err)
			}
			inv, readErr = Load(invitePath)
		}
		if readErr != nil {
			return initFailure(record, readErr)
		}
		if S(inv, "actor") != actor || S(inv, "project") != team || S(inv, "url") != cfg.URL {
			return initFailure(record, errors.New("existing invitation differs from this team; original invitation preserved"))
		}
		invitations = append(invitations, Object{"member": member, "actor": actor, "invitation_file": invitePath, "join_command": "otoha join --endpoint " + shellQuote(cfg.URL) + " --key " + shellQuote(S(inv, "invitation"))})
	}
	if err = initPhase(record, "checkout"); err != nil {
		return nil, err
	}
	binding, err := discoverJoinedRepository(ctx, cfg)
	if err != nil {
		return initFailure(record, err)
	}
	if binding == nil {
		return initFailure(record, errors.New("new team's repository is not configured"))
	}
	ownerResult := Object{"team": team, "readiness": ready, "ready": false}
	ownerResult, err = finishManagedTeamJoin(ctx, cfg, binding, "", ownerResult, ready)
	if err != nil {
		return initFailure(record, err)
	}
	if !B(ownerResult, "ready") {
		return initFailure(record, errors.New("local collaboration service is still connecting; resume initialization"))
	}
	if !B(ownerResult, "setup_ready_reported") {
		return initFailure(record, errors.New("this new initialization requires a setup_ready-capable authority; restore its matching center runtime and resume"))
	}
	ready["repository"], ready["service"] = "ready", "ready"
	record["state"], record["readiness"] = "owner_ready", ready
	if len(invitations) > 0 {
		record["state"] = "awaiting_members"
	}
	delete(record, "last_error")
	if err = initPhase(record, "ready"); err != nil {
		return nil, err
	}
	result := initSummary(record)
	result["ready"], result["owner_ready"], result["team_ready"] = false, true, false
	result["owner"], result["invitations"], result["graph"], result["checkout"], result["page"] = record["owner"], invitations, graph, ownerResult["checkout"], ownerResult["page"]
	result["setup_ready_receipt"], result["checks_source"], result["user_accepted"] = ownerResult["setup_ready_receipt"], "self_report", false
	result["waiting_for"] = "Expected teammates must enroll and report their actual local setup checks."
	result["next"] = "otoha team wait " + team + " --timeout 60"
	result["invitation_next"] = "Share each personal invitation privately with its teammate; their successful join automatically reports setup checks."
	setup, setupErr := teamWaitCommand(ctx, []string{team, "--timeout", "0"})
	if setup != nil {
		result["team_setup"] = setup
	}
	if setupErr == nil && B(setup, "team_ready") {
		record["state"] = "ready"
		if err = initPhase(record, "ready"); err != nil {
			return nil, err
		}
		result["state"], result["ready"], result["team_ready"] = "ready", true, true
		delete(result, "waiting_for")
		result["next"] = "Team setup is complete. Begin the team's work; no extra setup message or Git round trip is required."
	} else if setupErr != nil {
		result["team_setup_error"] = setupErr.Error()
	}
	return result, nil
}
