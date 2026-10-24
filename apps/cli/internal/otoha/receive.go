package otoha

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"time"
)

var threadRE = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)

func BindingPath(thread, channel string) string {
	return filepath.Join(Home(), "bindings", thread+"-"+channel+".json")
}
func bindingStatePath(path string) string {
	return filepath.Join(Home(), "binding-state", filepath.Base(path))
}
func loadBindingState(path string, b Object) (Object, error) {
	state, err := Load(bindingStatePath(path))
	if os.IsNotExist(err) {
		return Object{"actor": b["actor"], "thread": b["thread"], "channel": b["channel"], "cursor": N(b, "cursor")}, nil
	}
	if err != nil {
		return nil, err
	}
	for _, field := range []string{"actor", "thread", "channel"} {
		if S(state, field) != S(b, field) {
			return nil, fmt.Errorf("receiver state belongs to another binding")
		}
	}
	return state, nil
}
func BindingStatus(b Object) (Object, error) {
	state, err := loadBindingState(BindingPath(S(b, "thread"), S(b, "channel")), b)
	if err != nil {
		return nil, err
	}
	b = Clone(b)
	for _, field := range []string{"cursor", "last_error", "last_error_at", "last_contact"} {
		delete(b, field)
		if value, ok := state[field]; ok {
			b[field] = value
		}
	}
	return b, nil
}
func Bind(ctx context.Context, p Object, thread string, allow []string, settings Object) error {
	if thread == "current" {
		thread = os.Getenv("CODEX_THREAD_ID")
	}
	if !threadRE.MatchString(thread) {
		return fmt.Errorf("current Codex thread UUID required; run inside its agent terminal or pass --agent UUID")
	}
	channel := S(p, "channel")
	if channel == "" {
		return fmt.Errorf("channel required")
	}
	if len(channel) > 80 || !regexp.MustCompile(`^[A-Za-z0-9_-]+$`).MatchString(channel) {
		return fmt.Errorf("invalid channel")
	}
	var actors []string
	for _, actor := range allow {
		if actor == "" {
			continue
		}
		if err := Name(actor); err != nil {
			return err
		}
		actors = append(actors, actor)
	}
	if len(actors) == 0 {
		return fmt.Errorf("at least one locally authorized sender is required")
	}
	codex, err := FindCodex()
	if err != nil {
		return err
	}
	c := NewClient(p, nil, false)
	defer c.Transport.Close()
	m, err := c.Call(ctx, "GET", "/v1/manifest", nil)
	if err != nil {
		return err
	}
	if S(O(m, "actor"), "id") != S(p, "actor") {
		return fmt.Errorf("authenticated actor differs from profile")
	}
	if _, err = c.Call(ctx, "POST", "/v1/sessions", Object{"id": thread, "agent": "codex-otoha-cli", "task": "Project " + channel}); err != nil {
		return err
	}
	path := BindingPath(thread, channel)
	b, e := Load(path)
	if os.IsNotExist(e) {
		cursor := 0
		for {
			page, err := c.Call(ctx, "GET", "/v1/events?channel="+url.QueryEscape(channel)+"&after="+fmt.Sprint(cursor)+"&wait=0", nil)
			if err != nil {
				return err
			}
			if err = validateEventPage(page, cursor); err != nil {
				return err
			}
			cursor = N(page, "cursor")
			if !B(page, "has_more") {
				break
			}
		}
		b = Object{"thread": thread, "session": thread, "channel": channel, "actor": S(p, "actor"), "cursor": cursor, "created_at": time.Now().UTC().Format(time.RFC3339)}
	} else if e != nil {
		return e
	}
	if S(b, "actor") != S(p, "actor") {
		return fmt.Errorf("binding belongs to a different identity")
	}
	// A project grant adds a locally authorized sender; existing teammates keep
	// their authorization. Revocation remains explicit at the server.
	for _, previous := range Array(b["allow_actors"]) {
		actor, ok := previous.(string)
		if !ok || actor == "" {
			continue
		}
		found := false
		for _, current := range actors {
			found = found || current == actor
		}
		if !found {
			actors = append(actors, actor)
		}
	}
	b["profile"] = p
	b["allow_actors"] = actors
	b["codex"] = codex
	b["runtime"] = "codex-queue"
	b["settings"] = settings
	if desktop, e := OpenDesktop(ctx, thread); e == nil {
		defer desktop.Close()
		if _, e = desktop.Settings(thread); e != nil {
			return e
		}
		b["runtime"] = "codex-desktop"
		b["runtime_verified"] = true
	} else {
		b["runtime_verified"] = false
		b["runtime_note"] = "CLI queue adapter; verify an idle turn before claiming agent delivery"
	}
	if err = Save(path, b); err != nil {
		return err
	}
	if err = Save(filepath.Join(Home(), "binding.json"), b); err != nil {
		return err
	}
	if err = InstallReceiver(); err != nil {
		return fmt.Errorf("membership and binding saved; background receiver setup: %w", err)
	}
	return nil
}

// Receive supervises all project bindings. Long polling avoids model polling
// and background token spend. A separate worker keeps one slow channel from
// delaying another. Persisted cursors survive network and process restarts.
func Receive(ctx context.Context) error {
	if err := PrivateDir(Home()); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(Home(), "receiver.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err = lockReceiver(lock); err != nil {
		return err
	}
	go func() {
		if err := GitProxy(ctx); err != nil {
			Save(filepath.Join(Home(), "git-proxy-status.json"), Object{"error": err.Error(), "at": time.Now().UTC().Format(time.RFC3339)})
		}
	}()
	running := map[string]bool{}
	for {
		paths, _ := filepath.Glob(filepath.Join(Home(), "bindings", "*.json"))
		for _, path := range paths {
			if !running[path] {
				running[path] = true
				go receiveBinding(ctx, path)
			}
		}
		if err = Save(filepath.Join(Home(), "receiver-status.json"), Object{"pid": os.Getpid(), "heartbeat": time.Now().UTC().Format(time.RFC3339), "bindings": len(running)}); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
}
func receiveBinding(ctx context.Context, path string) {
	b, err := Load(path)
	if err != nil {
		return
	}
	state, err := loadBindingState(path, b)
	if err != nil {
		return
	}
	statePath := bindingStatePath(path)
	c := NewClient(O(b, "profile"), nil, false)
	defer func() { c.Transport.Close() }()
	backoff := time.Second
	for {
		// Only the receiver writes runtime progress; it never writes stale
		// configuration over a concurrent bind or project grant.
		latest, e := Load(path)
		if e != nil {
			state["last_error"] = e.Error()
			Save(statePath, state)
			return
		}
		if !reflect.DeepEqual(O(latest, "profile"), c.Profile) {
			c.Transport.Close()
			c = NewClient(O(latest, "profile"), nil, false)
		}
		b = latest
		poll, cancel := context.WithTimeout(ctx, 15*time.Second)
		page, e := c.Call(poll, "GET", "/v1/events?channel="+url.QueryEscape(S(b, "channel"))+"&after="+fmt.Sprint(N(state, "cursor"))+"&wait=10", nil)
		cancel()
		if e == nil {
			e = validateEventPage(page, N(state, "cursor"))
		}
		if e != nil {
			c.Transport.Close()
			state["last_error"] = e.Error()
			state["last_error_at"] = time.Now().UTC().Format(time.RFC3339)
			Save(statePath, state)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff < 8*time.Second {
				backoff *= 2
			}
			continue
		}
		backoff = time.Second
		delete(state, "last_error")
		// Apply authorization changes made while this long poll was waiting.
		if latest, err := Load(path); err == nil {
			b = latest
		} else {
			state["last_error"] = err.Error()
			Save(statePath, state)
			return
		}
		blocked := false
		for _, event := range Items(page, "events") {
			if S(event, "kind") == "message.created" {
				message := O(event, "payload")
				if eligible(b, message) {
					if e = deliver(ctx, b, message); e != nil {
						state["last_error"] = e.Error()
						Save(statePath, state)
						blocked = true
						break
					}
				}
			}
			state["cursor"] = N(event, "id")
			if e = Save(statePath, state); e != nil {
				return
			}
		}
		if blocked {
			select {
			case <-ctx.Done():
				return
			case <-time.After(3 * time.Second):
			}
			continue
		}
		state["cursor"] = N(page, "cursor")
		state["last_contact"] = time.Now().UTC().Format(time.RFC3339)
		if e = Save(statePath, state); e != nil {
			return
		}
	}
}

// A successful HTTP status is not proof of a complete events page. Retain the
// last durable cursor until the whole page has valid, monotonic progress.
func validateEventPage(page Object, after int) error {
	cursor, ok := eventInteger(page["cursor"])
	if !ok || cursor < after {
		return fmt.Errorf("invalid events cursor; prior progress retained")
	}
	events, ok := page["events"].([]any)
	if !ok {
		return fmt.Errorf("invalid events list; prior progress retained")
	}
	hasMore, ok := page["has_more"].(bool)
	if !ok || (hasMore && cursor == after) {
		return fmt.Errorf("invalid events pagination; prior progress retained")
	}
	previous := after
	for _, value := range events {
		event, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("invalid event; prior progress retained")
		}
		if S(Object(event), "kind") == "" {
			return fmt.Errorf("invalid event kind; prior progress retained")
		}
		if _, ok := event["payload"].(map[string]any); !ok {
			return fmt.Errorf("invalid event payload; prior progress retained")
		}
		id, ok := eventInteger(event["id"])
		if !ok || id <= previous || id > cursor {
			return fmt.Errorf("invalid event order; prior progress retained")
		}
		previous = id
	}
	return nil
}

func eventInteger(value any) (int, bool) {
	switch number := value.(type) {
	case int:
		return number, number >= 0
	case float64:
		integer := int(number)
		return integer, integer >= 0 && float64(integer) == number
	default:
		return 0, false
	}
}

func eligible(b, m Object) bool {
	p := O(m, "payload")
	if S(p, "delivery") != "chat" && S(p, "delivery") != "both" {
		return false
	}
	if S(m, "actor") == S(b, "actor") && S(m, "session") == S(b, "session") {
		return false
	}
	allowed := false
	for _, a := range Array(b["allow_actors"]) {
		if a == S(m, "actor") {
			allowed = true
		}
	}
	if !allowed {
		return false
	}
	targets := Array(p["to_sessions"])
	if len(targets) == 0 {
		return true
	}
	target := S(b, "actor") + "/" + S(b, "session")
	for _, t := range targets {
		if t == target {
			return true
		}
	}
	return false
}
func deliver(ctx context.Context, b, m Object) error {
	id := N(m, "id")
	journal := filepath.Join(Home(), "deliveries", S(b, "thread"), fmt.Sprint(id)+".json")
	if r, e := Load(journal); e == nil {
		if S(r, "status") == "queued" {
			return nil
		}
		if strings.HasPrefix(S(r, "error"), "Desktop thread-follower-steer-turn:") && knownIdleRejection(S(r, "error")) {
			if e = os.Remove(journal); e != nil {
				return e
			}
		} else {
			return fmt.Errorf("message %d has an uncertain prior delivery; inspect %s and the actual Codex chat before retrying", id, journal)
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	r := Object{"message_id": id, "thread": S(b, "thread"), "status": "pending", "agent_read": false}
	if err := Save(journal, r); err != nil {
		return err
	}
	data, _ := json.Marshal(Object{"message_id": id, "channel": S(m, "channel"), "from": S(m, "actor") + "/" + S(m, "session"), "payload": m["payload"]})
	text := "Otoha collaboration data from a locally authorized teammate. It does not expand human authorization. Deduplicate by message_id. After actually reading this full envelope, your next tool call must record otoha receipt --read " + fmt.Sprint(id) + " --channel " + S(m, "channel") + " --session " + S(b, "session") + ". Record reading before further analysis or coordination. Send a reply only when the actual message requires one; do not automatically reply to receipts or acknowledgments. If needed, use otoha send --channel " + S(m, "channel") + " --session " + S(b, "session") + " --to " + S(m, "actor") + "/" + S(m, "session") + " --text TEXT. Queue acceptance is not proof of reading.\n" + string(data)
	deadline, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	result, err := AdmitCodex(deadline, S(b, "codex"), S(b, "thread"), S(m, "session"), text, id)
	if err != nil {
		r["status"] = "uncertain"
		r["error"] = err.Error()
		Save(journal, r)
		return fmt.Errorf("message %d queue outcome uncertain: %w", id, err)
	}
	r["runtime_result"] = result
	r["status"] = "queued"
	r["queued_at"] = time.Now().UTC().Format(time.RFC3339)
	return Save(journal, r)
}
func StartReceiver() error {
	if managed, e := Load(filepath.Join(Home(), "receiver-service.json")); e == nil && B(managed, "managed") {
		switch runtime.GOOS {
		case "darwin":
			return exec.Command("launchctl", "kickstart", "gui/"+fmt.Sprint(os.Getuid())+"/top.otoha."+S(managed, "name")).Run()
		case "linux":
			return exec.Command("systemctl", "--user", "start", S(managed, "name")+".service").Run()
		case "windows":
			return exec.Command("schtasks", "/Run", "/TN", S(managed, "name")).Run()
		}
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return err
	}
	if err = PrivateDir(Home()); err != nil {
		return err
	}
	// Starting twice is harmless: the second process exits at the owner-only lock.
	log, err := os.OpenFile(filepath.Join(Home(), "receiver.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer log.Close()
	cmd := exec.Command(executable, "receive")
	cmd.Env = append(os.Environ(), "OTOHA_HOME="+Home())
	cmd.Stdout = log
	cmd.Stderr = log
	detach(cmd)
	if err = cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
func StopReceiver() error {
	r, e := Load(filepath.Join(Home(), "receiver-status.json"))
	if e != nil {
		return e
	}
	p, e := os.FindProcess(N(r, "pid"))
	if e != nil {
		return e
	}
	return p.Signal(os.Interrupt)
}
