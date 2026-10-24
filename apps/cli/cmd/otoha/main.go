package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	a "github.com/Vioano/Otoha/apps/cli/internal/otoha"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var version = "dev"

func output(v any)                                  { e := json.NewEncoder(os.Stdout); e.SetIndent("", "  "); e.Encode(v) }
func fail(err error)                                { fmt.Fprintln(os.Stderr, "otoha:", err); os.Exit(1) }
func flags(name string) *flag.FlagSet               { return flag.NewFlagSet(name, flag.ExitOnError) }
func parse(f *flag.FlagSet, args []string) []string { f.Parse(args); return f.Args() }
func loadSettings() (a.Object, error)               { return a.Load(filepath.Join(a.Home(), "config.json")) }
func settings() a.Object {
	s, e := loadSettings()
	if e != nil {
		fail(fmt.Errorf("run otoha setup or otoha import first: %w", e))
	}
	return s
}
func profile(s a.Object) (a.Object, *a.Client) {
	p, c, e := a.OpenProfile(s)
	if e != nil {
		fail(e)
	}
	return p, c
}
func main() {
	if err := run(os.Args[1:]); err != nil {
		fail(err)
	}
}
func run(args []string) error {
	if len(args) == 0 {
		help()
		return nil
	}
	ctx := context.Background()
	command := args[0]
	args = args[1:]
	switch command {
	case "version", "--version":
		fmt.Println("otoha", version)
		return nil
	case "help", "--help", "-h":
		help()
		return nil
	case "admit":
		f := flags("admit")
		thread := f.String("thread", "", "existing Desktop target chat UUID")
		source := f.String("source-thread", "", "optional original sending chat UUID")
		requestID := f.String("request-id", "", "wake UUID already persisted by the original receiver")
		parse(f, args)
		if *thread == "" || *requestID == "" {
			return fmt.Errorf("admit requires --thread UUID and --request-id PERSISTED_UUID; pass private wake text on stdin")
		}
		text, err := io.ReadAll(io.LimitReader(os.Stdin, 65537))
		if err != nil {
			return err
		}
		deadline, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		result, err := a.BusyAdmit(deadline, *thread, *source, *requestID, string(text))
		if result != nil {
			output(result)
		}
		return err
	case "setup":
		f := flags("setup")
		ap := f.String("otoha-profile", "", "existing captain Otoha identity")
		gp := f.String("gitea-profile", "", "existing Gitea administrator identity")
		gw := f.String("gateway-profile", "", "existing restricted gateway profile")
		ga := f.String("gateway-admin", "", "trusted SSH administrator alias")
		https := f.String("https", "", "verified public HTTPS fallback")
		sa := f.String("server-admin", "", "optional private server SSH administrator alias")
		mg := f.String("member-gateway-profile", "", "optional member routes, excluding captain key")
		parse(f, args)
		if *ap == "" || *gp == "" || *gw == "" || *ga == "" {
			return fmt.Errorf("setup requires --otoha-profile, --gitea-profile, --gateway-profile and --gateway-admin")
		}
		s := a.Object{"mode": "captain", "otoha_profile": *ap, "gitea_profile": *gp, "gateway_profile": *gw, "gateway_admin": *ga, "server_admin": *sa, "member_gateway_profile": *mg, "https_endpoint": *https}
		admin, e := a.OpenAdmin(s)
		if e != nil {
			return e
		}
		defer admin.Otoha.Transport.Close()
		if e = admin.Check(ctx); e != nil {
			return e
		}
		if e = a.Save(filepath.Join(a.Home(), "config.json"), s); e != nil {
			return e
		}
		output(a.Object{"configured": true, "existing_credentials_preserved": true})
		return nil
	case "import":
		if len(args) < 1 {
			return fmt.Errorf("otoha import /private/existing-identity.json")
		}
		f := flags("import")
		bundle := f.String("bundle", "", "existing private bundle for Git access")
		endpoint := f.String("https", "", "verified HTTPS endpoint")
		agent := f.String("agent", "", "current or thread UUID")
		parse(f, args[1:])
		p, e := filepath.Abs(args[0])
		if e != nil {
			return e
		}
		s, e := a.ImportLegacy(p)
		if e != nil {
			return e
		}
		if *bundle != "" {
			path, e := filepath.Abs(*bundle)
			if e != nil {
				return e
			}
			s["legacy_bundle"] = path
		}
		if *endpoint != "" {
			s["https_endpoint"] = *endpoint
		}
		profile, c, e := a.OpenProfile(s)
		if e != nil {
			return e
		}
		defer c.Transport.Close()
		if _, e = c.Call(ctx, "GET", "/v1/manifest", nil); e != nil {
			return e
		}
		if e = a.Save(filepath.Join(a.Home(), "config.json"), s); e != nil {
			return e
		}
		if *agent != "" {
			if e = a.Bind(ctx, profile, *agent, []string{strings.Split(a.S(a.O(profile, "gitea"), "repository"), "/")[0]}, s); e != nil {
				return e
			}
		}
		output(s)
		return nil
	case "pack":
		if len(args) != 1 {
			return fmt.Errorf("otoha pack /private/existing.bundle.json")
		}
		inv, e := a.CompactBundle(args[0])
		if e != nil {
			return e
		}
		delete(inv, "client_files")

		key, e := a.Pack(inv)
		if e != nil {
			return e
		}
		fmt.Print(invitationText(key, ""))
		return nil
	case "project":
		if len(args) < 2 {
			return fmt.Errorf("otoha project create NAME [--invite MEMBER] or otoha project grant NAME --member MEMBER")
		}
		action, project := args[0], args[1]
		if action != "create" && action != "grant" {
			return fmt.Errorf("unknown project action")
		}
		f := flags("project")
		invite := f.String("invite", "", "new or existing member")
		member := f.String("member", "", "existing member")
		purpose := f.String("purpose", "", "repository purpose")
		role := f.String("role", "member", "member or reader")
		days := f.Int("days", 7, "first enrollment lifetime")
		agent := f.String("agent", "", "current or thread UUID")
		out := f.String("out", "", "private two-line invitation file")
		platform := f.String("platform", "", "windows, macos or linux invitation commands")
		parse(f, args[2:])
		start := time.Now()
		s := settings()
		admin, e := a.OpenAdmin(s)
		if e != nil {
			return e
		}
		defer admin.Otoha.Transport.Close()
		r, e := admin.Create(ctx, project, *purpose)
		if e != nil {
			return e
		}
		if action == "grant" {
			if *member == "" {
				return fmt.Errorf("--member required")
			}
			r, e = admin.Grant(ctx, a.S(r, "repository"), project, *member, *role)
			if e != nil {
				return e
			}
		} else if action == "create" {
			if *invite != "" {
				v, e := admin.Invite(ctx, a.S(r, "repository"), project, *invite, *role, *days)
				if e != nil {
					return e
				}
				for k, v := range v {
					r[k] = v
				}
			}
		} else {
			return fmt.Errorf("unknown project action")
		}
		if *agent != "" {
			p := admin.Otoha.Profile
			p["channel"] = project + "-general"
			allowed := *invite
			if allowed == "" {
				allowed = *member
			}
			if allowed == "" {
				return fmt.Errorf("--agent needs --invite or --member for a locally authorized teammate")
			}
			if e = a.Bind(ctx, p, *agent, []string{allowed}, s); e != nil {
				return e
			}
		}
		r["elapsed_ms"] = float64(time.Since(start).Microseconds()) / 1000
		if key := a.S(r, "key"); key != "" {
			text := invitationText(key, *platform)
			delete(r, "key")
			if *out != "" {
				if e = a.PrivateDir(filepath.Dir(*out)); e != nil {
					return e
				}
				if e = os.WriteFile(*out, []byte(text), 0600); e != nil {
					return e
				}
				r["invitation_file"] = *out
				output(r)
			} else {
				fmt.Fprint(os.Stdout, text)
				fmt.Fprintf(os.Stderr, "创建和邀请完成，%.2f 秒。\n", time.Since(start).Seconds())
			}
		} else {
			output(r)
		}
		return nil
	case "team":
		result, err := a.TeamCommand(ctx, args)
		if result != nil {
			output(result)
		}
		return err
	case "join":
		// Accept key before flags so the invitation really is one copyable command.
		key := ""
		if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
			key = args[0]
			args = args[1:]
		}
		f := flags("join")
		keyFile := f.String("key-file", "", "read invitation from a private file")
		bundle := f.String("bundle", "", "reuse original private invitation bundle")
		endpoint := f.String("https", "", "verified HTTPS fallback endpoint")
		agent := f.String("agent", "", "current or thread UUID")
		parse(f, args)
		if *bundle != "" {
			inv, e := a.CompactBundle(*bundle)
			if e != nil {
				return e
			}
			if *endpoint != "" {
				inv["https_endpoint"] = *endpoint
			}
			key, e = a.Pack(inv)
			if e != nil {
				return e
			}
		}
		if *keyFile != "" {
			b, e := os.ReadFile(*keyFile)
			if e != nil {
				return e
			}
			key = strings.TrimSpace(string(b))
		}
		if key == "" {
			fmt.Fprint(os.Stderr, "邀请 key: ")
			scanner := bufio.NewScanner(os.Stdin)
			scanner.Scan()
			key = strings.TrimSpace(scanner.Text())
		}
		if *endpoint != "" {
			inv, e := a.Unpack(key)
			if e != nil {
				return e
			}
			inv["https_endpoint"] = *endpoint
			inv["prefer_https"] = true
			key, e = a.Pack(inv)
			if e != nil {
				return e
			}
		}
		start := time.Now()
		r, e := a.Join(ctx, key, *endpoint)
		if e != nil {
			return e
		}
		if *agent != "" {
			p, e := a.Load(a.S(r, "profile"))
			if e != nil {
				return e
			}
			inv, _ := a.Unpack(key)
			owner := strings.Split(a.S(a.O(inv, "gitea"), "repository"), "/")[0]
			if e = a.Bind(ctx, p, *agent, []string{owner}, settings()); e != nil {
				return fmt.Errorf("membership ready; agent binding: %w", e)
			}
		}
		r["elapsed_ms"] = float64(time.Since(start).Microseconds()) / 1000
		output(r)
		return nil
	case "status", "doctor":
		s := settings()
		p, c := profile(s)
		defer c.Transport.Close()
		start := time.Now()
		m, e := c.Call(ctx, "GET", "/v1/manifest", nil)
		if e != nil {
			return e
		}
		r := a.Object{"actor": a.O(m, "actor"), "grants": m["grants"], "transport": c.Transport.Mode, "elapsed_ms": float64(time.Since(start).Microseconds()) / 1000, "credentials_expire": false}
		if b, e := a.Load(filepath.Join(a.Home(), "binding.json")); e == nil {
			if status, err := a.BindingStatus(b); err == nil {
				b = status
			}
			delete(b, "profile")
			delete(b, "settings")
			r["agent_binding"] = b
		}
		r["legacy_identity"] = a.S(s, "profile")
		if proxy, e := a.Load(filepath.Join(a.Home(), "git-proxy-status.json")); e == nil {
			r["git_proxy"] = proxy
		}
		r["channel"] = p["channel"]
		output(r)
		return nil
	case "agents":
		f := flags("agents")
		pr := f.String("project", "team", "project")
		parse(f, args)
		_, c := profile(settings())
		defer c.Transport.Close()
		r, e := c.Call(ctx, "GET", "/v1/agents?project="+url.QueryEscape(*pr), nil)
		if e == nil {
			output(r)
		}
		return e
	case "bind":
		f := flags("bind")
		agent := f.String("agent", "current", "current or thread UUID")
		channel := f.String("channel", "", "project channel")
		allow := f.String("allow", "", "comma-separated authorized actors")
		parse(f, args)
		s := settings()
		p, _ := profile(s)
		if *channel != "" {
			p["channel"] = *channel
		}
		if *allow == "" {
			return fmt.Errorf("--allow ACTOR[,ACTOR] required")
		}
		return a.Bind(ctx, p, *agent, strings.Split(*allow, ","), s)
	case "telegram":
		if len(args) == 0 {
			return fmt.Errorf("otoha telegram send|receive|status [options]")
		}
		action := args[0]
		f := flags("telegram " + action)
		configPath := f.String("config", "", "private Telegram config JSON")
		tokenFile := f.String("token-file", "", "private Telegram token file")
		stateDir := f.String("state-dir", "", "private Telegram state directory")
		apiBase := f.String("api-base", "", "Telegram API base, for local tests")
		chatID := f.String("chat-id", "", "Telegram chat ID or @username")
		recipient := f.String("to", "", "configured local session/member recipient")
		text := f.String("text", "", "message text")
		messageID := f.String("id", "", "stable Otoha message ID")
		routes := f.String("route", "", "chat_id=session, comma separated")
		once := f.Bool("once", false, "receive one long-poll batch and exit")
		parse(f, args[1:])
		cfg, e := a.LoadTelegramConfig(*configPath)
		if e != nil && *configPath != "" {
			return e
		}
		if *tokenFile != "" {
			cfg.TokenFile, cfg.Token = *tokenFile, ""
		}
		if *stateDir != "" {
			cfg.StateDir = *stateDir
		}
		if *apiBase != "" {
			cfg.APIBase = *apiBase
		}
		if cfg.Routes == nil {
			cfg.Routes = map[string]string{}
		}
		if cfg.Recipients == nil {
			cfg.Recipients = map[string]string{}
		}
		for _, item := range strings.Split(*routes, ",") {
			if item == "" {
				continue
			}
			parts := strings.SplitN(item, "=", 2)
			if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
				return fmt.Errorf("invalid --route %q; expected CHAT_ID=SESSION", item)
			}
			cfg.Routes[parts[0]] = parts[1]
		}
		g, e := a.NewTelegramGateway(cfg)
		if e != nil {
			return e
		}
		defer g.Close()
		switch action {
		case "send":
			if *chatID == "" && *recipient != "" {
				*chatID = cfg.Recipients[*recipient]
			}
			if *chatID == "" || *text == "" {
				return fmt.Errorf("telegram send requires --chat-id or configured --to, and --text")
			}
			result, e := g.Send(ctx, a.Outbound{ID: *messageID, ChatID: *chatID, Text: *text})
			if e == nil {
				output(result)
			}
			return e
		case "receive":
			sink := func(in a.Inbound) error { output(in); return nil }
			if *once {
				_, e = g.ReceiveOnce(ctx, sink)
				return e
			}
			return g.Receive(ctx, sink)
		case "status":
			if e = g.Connect(ctx); e != nil {
				return e
			}
			output(a.Object{"channel": g.Name(), "identity": g.Identity(), "capabilities": g.Capabilities(), "state_path": g.StatePath()})
			return nil
		case "bridge":
			s := settings()
			p, c := profile(s)
			defer c.Transport.Close()
			session := os.Getenv("CODEX_THREAD_ID")
			if b, loadErr := a.Load(filepath.Join(a.Home(), "binding.json")); loadErr == nil && session == "" {
				session = a.S(b, "session")
			}
			if session == "" {
				session = "cli-session"
			}
			channel := a.S(p, "channel")
			if channel == "" {
				return fmt.Errorf("telegram bridge requires a configured project channel")
			}
			if _, e = c.Call(ctx, "POST", "/v1/sessions", a.Object{"id": session, "agent": "otoha-telegram-bridge"}); e != nil {
				return e
			}
			binding := a.Object{"actor": a.S(p, "actor"), "session": session, "channel": channel}
			bridge, e := a.NewChannelBridge(g, c, binding, func(message a.Object) string {
				payload := a.O(message, "payload")
				for _, target := range a.Array(payload["to_sessions"]) {
					if chat := cfg.Recipients[fmt.Sprint(target)]; chat != "" {
						return chat
					}
				}
				return cfg.Recipients["public"]
			})
			if e != nil {
				return e
			}
			return bridge.Run(ctx)
		default:
			return fmt.Errorf("unknown telegram action %q", action)
		}
	case "inbox":
		if len(args) == 0 {
			return fmt.Errorf("otoha inbox check|list|read [options]")
		}
		action := args[0]
		f := flags("inbox " + action)
		channel := f.String("channel", "", "project channel")
		session := f.String("session", "", "local Otoha session")
		messageID := f.String("id", "", "message uid or numeric message ID")
		wait := f.Int("wait", 0, "long-poll seconds for check, 0 to return immediately")
		remaining := parse(f, args[1:])
		if *messageID == "" && len(remaining) == 1 {
			*messageID = remaining[0]
		}
		s := settings()
		p, c := profile(s)
		defer c.Transport.Close()
		if *session == "" {
			*session = os.Getenv("CODEX_THREAD_ID")
		}
		if *session == "" {
			*session = "cli-session"
		}
		if *channel == "" {
			*channel = a.S(p, "channel")
		}
		if *channel == "" {
			return fmt.Errorf("--channel CHANNEL required")
		}
		if _, e := c.Call(ctx, "POST", "/v1/sessions", a.Object{"id": *session, "agent": "otoha-cli"}); e != nil {
			return e
		}
		binding := a.Object{"actor": a.S(p, "actor"), "session": *session, "channel": *channel}
		switch action {
		case "check":
			r, e := a.InboxCheck(ctx, c, binding, *wait)
			if e == nil {
				output(r)
			}
			return e
		case "list":
			r, e := a.ListInbox(*channel)
			if e == nil {
				output(a.Object{"channel": *channel, "messages": r})
			}
			return e
		case "read":
			if *messageID == "" {
				return fmt.Errorf("inbox read requires MESSAGE_UID or --id")
			}
			r, e := a.InboxRead(ctx, c, binding, *messageID)
			if e == nil {
				output(r)
			}
			return e
		default:
			return fmt.Errorf("unknown inbox action %q", action)
		}
	case "receive":
		return a.Receive(ctx)
	case "service":
		if len(args) == 0 || args[0] == "install" {
			return a.InstallReceiver()
		}
		if args[0] == "run" {
			f := flags("service run")
			home := f.String("home", "", "private client directory")
			parse(f, args[1:])
			if *home != "" {
				os.Setenv("OTOHA_HOME", *home)
			}
			return a.Receive(ctx)
		}
		return fmt.Errorf("otoha service install or otoha service run")
	case "send":
		f := flags("send")
		channel := f.String("channel", "", "channel")
		session := f.String("session", "", "Otoha session")
		target := f.String("to", "", "actor/session target")
		text := f.String("text", "", "message text")
		parse(f, args)
		s := settings()
		p, c := profile(s)
		defer c.Transport.Close()
		if *session == "" {
			*session = os.Getenv("CODEX_THREAD_ID")
		}
		if b, e := a.Load(filepath.Join(a.Home(), "binding.json")); e == nil {
			if *session == "" {
				*session = a.S(b, "session")
			}
			if *channel == "" && a.S(b, "session") == *session {
				*channel = a.S(b, "channel")
			}
		}
		if *channel == "" && *session != "" {
			paths, _ := filepath.Glob(filepath.Join(a.Home(), "bindings", *session+"-*.json"))
			if len(paths) == 1 {
				if b, e := a.Load(paths[0]); e == nil {
					*channel = a.S(b, "channel")
				}
			}
			if len(paths) > 1 {
				return fmt.Errorf("this chat is bound to multiple projects; provide --channel")
			}
		}
		if *channel == "" {
			*channel = a.S(p, "channel")
		}
		r, e := a.Send(ctx, c, *channel, *session, *text, *target, "")
		if e == nil {
			output(r)
		}
		return e
	case "retry":
		if len(args) != 1 || strings.ContainsAny(args[0], `/\.`) {
			return fmt.Errorf("provide the original operation ID")
		}
		b, e := a.Load(filepath.Join(a.Home(), "outbox", args[0]+".json"))
		if e != nil {
			return e
		}
		_, c := profile(settings())
		defer c.Transport.Close()
		r, e := c.Call(ctx, "POST", "/v1/messages", b)
		if e == nil {
			b["local_state"] = "accepted_by_server"
			b["server_result"] = r
			if saveErr := a.Save(filepath.Join(a.Home(), "outbox", args[0]+".json"), b); saveErr != nil {
				return fmt.Errorf("server accepted retry but local outbox update failed: %w", saveErr)
			}
			output(r)
		}
		return e
	case "read":
		f := flags("read")
		channel := f.String("channel", "", "channel")
		before := f.Int("before", 0, "read messages before this ID")
		parse(f, args)
		p, c := profile(settings())
		defer c.Transport.Close()
		if *channel == "" {
			*channel = a.S(p, "channel")
		}
		r, e := a.History(ctx, c, *channel, *before)
		if e == nil {
			output(r)
		}
		return e
	case "receipt":
		f := flags("receipt")
		ch := f.String("channel", "", "channel")
		session := f.String("session", "", "session")
		id := f.Int("read", 0, "actually read message ID")
		parse(f, args)
		if *id < 1 {
			return fmt.Errorf("--read MESSAGE_ID required")
		}
		_, c := profile(settings())
		defer c.Transport.Close()
		r, e := c.Call(ctx, "POST", "/v1/receipts", a.Object{"channel": *ch, "session": *session, "received": *id, "read": *id})
		if e == nil {
			output(r)
		}
		return e
	case "benchmark":
		f := flags("benchmark")
		n := f.Int("samples", 20, "sample count")
		parse(f, args)
		_, c := profile(settings())
		defer c.Transport.Close()
		r, e := a.Benchmark(ctx, c, *n)
		if e == nil {
			output(r)
		}
		return e
	case "gateway":
		f := flags("gateway")
		listen := f.String("listen", ":443", "HTTPS listen address")
		cert := f.String("cert", "", "certificate chain")
		key := f.String("tls-key", "", "TLS private key")
		root := f.String("static-root", "/var/www/sub", "preserved static files")
		ap := f.String("otoha-upstream", "http://127.0.0.1:23001", "Otoha loopback upstream")
		gp := f.String("git-upstream", "http://127.0.0.1:23000", "Gitea loopback upstream")
		routes := f.String("routes", "", "additional PREFIX=http://127.0.0.1:PORT routes, comma separated")
		parse(f, args)
		return a.HTTPSGateway(ctx, *listen, *cert, *key, *root, *ap, *gp, *routes)
	case "proxy":
		return proxy(ctx, args)
	case "clone":
		return a.CloneRepo(ctx, args)
	case "credential":
		return a.Credential(args, os.Stdin, os.Stdout)
	default:
		return fmt.Errorf("unknown command %q; run otoha help", command)
	}
}
func invitationText(key, platform string) string {
	inv, _ := a.Unpack(key)
	endpoint := strings.TrimRight(a.S(inv, "https_endpoint"), "/")
	release := "https://github.com/Vioano/Otoha/releases"
	installer := "https://raw.githubusercontent.com/Vioano/Otoha/main/apps/cli/scripts/install.ps1"
	if version != "dev" {
		release += "/tag/v" + version
		installer = "https://raw.githubusercontent.com/Vioano/Otoha/v" + version + "/apps/cli/scripts/install.ps1"
	}
	if platform == "windows" {
		install := "irm " + installer + " | iex"
		if endpoint != "" {
			mirror := endpoint + "/otoha"
			if version != "dev" {
				mirror += "/v" + version
			}
			install = "$env:OTOHA_DOWNLOAD_BASE='" + mirror + "'; irm " + mirror + "/install.ps1 | iex"
		}
		transport := ""
		if endpoint != "" {
			transport = " --https " + endpoint
		}
		return install + "\n& \"$env:LOCALAPPDATA\\Otoha\\otoha.exe\" join " + key + " --agent current" + transport + "\n"
	}
	return "请当前 Agent 从 " + release + " 安装本机 CLI，并执行下一行绑定本聊天；凭据只保留在本机。\notoha join " + key + " --agent current\n"
}
func proxy(ctx context.Context, args []string) error {
	f := flags("proxy")
	listen := f.String("listen", "127.0.0.1:13000", "loopback address")
	parse(f, args)
	if !strings.HasPrefix(*listen, "127.0.0.1:") {
		return fmt.Errorf("proxy must bind to loopback")
	}
	s := settings()
	p, c := profile(s)
	g := a.O(p, "gitea")
	if gp := a.S(s, "gitea_profile"); gp != "" {
		var e error
		g, e = a.Load(gp)
		if e != nil {
			return e
		}
	}
	target, e := url.Parse(a.S(g, "url"))
	if e != nil {
		return e
	}
	rp := a.NewGitReverseProxy(target, g, c.Transport.HTTP.Transport, a.Object{"address": *listen})
	output(a.Object{"git_url": "http://" + *listen, "transport": c.Transport.Mode})
	return http.ListenAndServe(*listen, rp)
}
func help() {
	io.WriteString(os.Stdout, `Otoha — private projects and instant agent collaboration

  otoha setup --otoha-profile FILE --gitea-profile FILE --gateway-profile FILE --gateway-admin SSH_ALIAS
  otoha project create NAME --invite MEMBER --purpose TEXT --agent current
  otoha project grant NAME --member MEMBER
  otoha team init NAME [--mode local] [--data DIR] [--port PORT]
  otoha team invite TEAM MEMBER [--role member|admin|reader] [--out FILE]
  otoha team join --invite FILE
  otoha team status [TEAM]
  otoha team doctor [TEAM]
  otoha join KEY --agent current
  otoha import EXISTING_IDENTITY.json
  otoha pack EXISTING_BUNDLE.json
  otoha bind --agent current --channel CHANNEL --allow ACTOR
  otoha admit --thread UUID --request-id PERSISTED_UUID < PRIVATE_WAKE_TEXT
  otoha send --text TEXT --to ACTOR/SESSION
  otoha telegram status --config PRIVATE/telegram.json
  otoha telegram send --config PRIVATE/telegram.json --chat-id @bot --text TEXT --id UID
  otoha telegram receive --config PRIVATE/telegram.json --route CHAT_ID=SESSION
  otoha telegram bridge --config PRIVATE/telegram.json
  otoha inbox check --channel CHANNEL [--session SESSION] [--wait SECONDS]
  otoha inbox list --channel CHANNEL
  otoha inbox read MESSAGE_UID --channel CHANNEL --session SESSION
  otoha read --channel CHANNEL
  otoha status | doctor | agents | benchmark

OTOHA_HOME selects a separate private client directory. Existing identities,
permissions and enrollment paths remain valid. Each invitation is a personal
credential: share it privately and never commit it.
`)
}
