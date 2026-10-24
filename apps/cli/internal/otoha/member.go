package otoha

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func HTTPSProfile(p, inv Object) error {
	endpoint := strings.TrimRight(S(inv, "https_endpoint"), "/")
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" {
		return fmt.Errorf("a verified HTTPS origin is required")
	}
	p["url"] = endpoint + "/otoha-api"
	p["transport"] = "https"
	if gw := p["gateway"]; gw != nil {
		p["ssh_gateway"] = gw
	}
	delete(p, "gateway")
	g := Clone(O(inv, "gitea"))
	g["url"] = endpoint + "/otoha-git"
	p["gitea"] = g
	return nil
}

func Join(ctx context.Context, key string, httpsOrigin ...string) (Object, error) {
	inv, err := Unpack(key)
	if err != nil {
		return nil, err
	}
	actor := S(inv, "actor")
	if err = Name(actor); err != nil {
		return nil, err
	}
	if S(inv, "protocol") == "otoha-access/1" {
		return AttachExisting(ctx, inv, httpsOrigin...)
	}
	path := filepath.Join(Home(), "members", actor, "identity.json")
	if old, e := Load(path); e == nil {
		if S(old, "actor") != actor || (S(old, "url") != S(inv, "url") && S(old, "url") != strings.TrimRight(S(inv, "https_endpoint"), "/")+"/otoha-api") {
			return nil, fmt.Errorf("different identity already exists; it will not be replaced")
		}
		if B(inv, "prefer_https") {
			if err = HTTPSProfile(old, inv); err != nil {
				return nil, err
			}
		}
		c := NewClient(old, nil, false)
		defer c.Transport.Close()
		m, err := c.Call(ctx, "GET", "/v1/manifest", nil)
		if err != nil {
			return nil, err
		}
		if B(inv, "prefer_https") {
			if err = Save(path, old); err != nil {
				return nil, err
			}
		}
		if err = Save(filepath.Join(Home(), "config.json"), Object{"mode": "member", "profile": path}); err != nil {
			return nil, err
		}
		return Object{"profile": path, "actor": actor, "grants": m["grants"], "resumed": true}, nil
	} else if !os.IsNotExist(e) {
		return nil, e
	}
	pendingPath := path + ".pending"
	pending, err := Load(pendingPath)
	if os.IsNotExist(err) {
		host, _ := os.Hostname()
		host = strings.Split(host, ".")[0]
		host = strings.Map(func(r rune) rune {
			if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
				return r
			}
			return '-'
		}, host)
		if len(host) > 60 {
			host = host[:60]
		}
		if host == "" {
			host = "device"
		}
		pending = Object{"invitation": S(inv, "invitation"), "credential": ID() + ID(), "enrollment_id": ID(), "device": host}
		if err = Save(pendingPath, pending); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	if S(pending, "invitation") != S(inv, "invitation") {
		return nil, fmt.Errorf("another enrollment is pending; original state preserved")
	}
	if id := S(pending, "enrollment_id"); strings.HasPrefix(id, "-") || strings.HasPrefix(id, "_") {
		pending["enrollment_id"] = "a" + id
		if err = Save(pendingPath, pending); err != nil {
			return nil, err
		}
	}
	p := Object{"url": S(inv, "url"), "gateway": inv["gateway"], "gitea": inv["gitea"]}
	if B(inv, "prefer_https") {
		if err = HTTPSProfile(p, inv); err != nil {
			return nil, err
		}
	}
	c := NewClient(p, nil, false)
	defer func() { c.Transport.Close() }()
	probe, cancel := context.WithTimeout(ctx, 5*time.Second)
	_, err = c.Call(probe, "GET", "/health", nil)
	cancel()
	if err != nil {
		if S(inv, "https_endpoint") == "" || B(inv, "prefer_https") {
			return nil, fmt.Errorf("transport unavailable before enrollment: %w", err)
		}
		c.Transport.Close()
		if err = HTTPSProfile(p, inv); err != nil {
			return nil, err
		}
		c = NewClient(p, nil, false)
		if _, err = c.Call(ctx, "GET", "/health", nil); err != nil {
			return nil, fmt.Errorf("SSH and HTTPS unavailable: %w", err)
		}
	}
	enrolled, err := c.Call(ctx, "POST", "/v1/enroll", pending)
	if err != nil {
		return nil, fmt.Errorf("enrollment: %w", err)
	}
	p["actor"] = S(enrolled, "actor")
	p["device"] = S(enrolled, "device")
	p["token"] = S(pending, "credential")
	p["project"] = inv["project"]
	p["channel"] = inv["channel"]
	p["protocol"] = "otoha-collaboration/1"
	if err = Save(path, p); err != nil {
		return nil, err
	}
	if err = Save(filepath.Join(Home(), "config.json"), Object{"mode": "member", "profile": path}); err != nil {
		return nil, err
	}
	os.Remove(pendingPath)
	m, err := NewClient(p, c.Transport, false).Call(ctx, "GET", "/v1/manifest", nil)
	if err != nil {
		return nil, err
	}
	git := NewClient(O(p, "gitea"), c.Transport, true)
	if _, err = git.Call(ctx, "GET", "/api/v1/repos/"+S(O(p, "gitea"), "repository"), nil); err != nil {
		return nil, fmt.Errorf("identity saved; repository check: %w", err)
	}
	return Object{"profile": path, "actor": actor, "grants": m["grants"], "project": p["project"], "channel": p["channel"], "repository": S(O(p, "gitea"), "repository"), "enrolled_identity_expires": false}, nil
}

// A grant key selects a project; authentication comes only from the member's
// existing local identity. Unauthenticated key metadata cannot change its hosts
// or replace its credentials. Old identity files and bindings are retained.
func AttachExisting(ctx context.Context, inv Object, httpsOrigin ...string) (Object, error) {
	project, channel, repo := S(inv, "project"), S(inv, "channel"), S(O(inv, "gitea"), "repository")
	if err := Name(project); err != nil {
		return nil, err
	}
	parts := strings.Split(repo, "/")
	if len(parts) != 2 || channel != project+"-general" {
		return nil, fmt.Errorf("invalid project access key")
	}
	for _, part := range parts {
		if err := Name(part); err != nil {
			return nil, err
		}
	}
	settings, err := Load(filepath.Join(Home(), "config.json"))
	if err != nil {
		return nil, fmt.Errorf("import your existing identity and original bundle first; no new enrollment is performed: %w", err)
	}
	p, c, err := OpenProfile(settings)
	if err != nil {
		return nil, err
	}
	if S(p, "actor") != S(inv, "actor") {
		c.Transport.Close()
		return nil, fmt.Errorf("project access belongs to another actor; existing identity preserved")
	}
	if len(httpsOrigin) > 0 && httpsOrigin[0] != "" {
		// Only an explicit CLI option can migrate an existing identity's route.
		// The grant key itself cannot redirect its authentication.
		c.Transport.Close()
		if err = HTTPSProfile(p, Object{"https_endpoint": httpsOrigin[0], "gitea": p["gitea"]}); err != nil {
			return nil, err
		}
		settings["https_endpoint"] = httpsOrigin[0]
		c = NewClient(p, nil, false)
	}
	defer c.Transport.Close()
	m, err := c.Call(ctx, "GET", "/v1/manifest", nil)
	if err != nil {
		return nil, err
	}
	if S(O(m, "actor"), "id") != S(p, "actor") {
		return nil, fmt.Errorf("authenticated actor differs from existing profile")
	}
	granted := false
	for _, grant := range Items(m, "grants") {
		if S(grant, "project") == project {
			granted = true
		}
	}
	if !granted {
		return nil, fmt.Errorf("existing identity has no grant for this project")
	}
	g := Clone(O(p, "gitea"))
	if S(g, "url") == "" {
		return nil, fmt.Errorf("import your original bundle to reuse existing Git authentication")
	}
	git := NewClient(g, c.Transport, true)
	if _, err = git.Call(ctx, "GET", "/api/v1/repos/"+repo, nil); err != nil {
		return nil, fmt.Errorf("existing Git identity cannot read the granted repository: %w", err)
	}
	g["repository"] = repo
	p["gitea"], p["project"], p["channel"] = g, project, channel
	path := filepath.Join(Home(), "projects", project, "identity.json")
	if err = Save(path, p); err != nil {
		return nil, err
	}
	settings["profile"] = path
	// OpenProfile already resolved the old bundle into this private copy.
	// Retaining the active legacy_bundle would overwrite the new project.
	if old := settings["legacy_bundle"]; old != nil {
		settings["legacy_bundle_source"] = old
		delete(settings, "legacy_bundle")
	}
	if err = Save(filepath.Join(Home(), "config.json"), settings); err != nil {
		return nil, err
	}
	return Object{"profile": path, "actor": S(p, "actor"), "grants": m["grants"], "project": project, "channel": channel, "repository": repo, "existing_credentials_preserved": true, "enrollment_called": false}, nil
}

func OpenProfile(settings Object) (Object, *Client, error) {
	path := S(settings, "profile")
	if path == "" {
		path = S(settings, "otoha_profile")
	}
	p, err := Load(path)
	if err != nil {
		return nil, nil, err
	}
	if bundle := S(settings, "legacy_bundle"); bundle != "" {
		inv, e := CompactBundle(bundle)
		if e != nil {
			return nil, nil, e
		}
		if S(inv, "actor") != S(p, "actor") {
			return nil, nil, fmt.Errorf("bundle belongs to a different identity")
		}
		p["gitea"] = inv["gitea"]
		p["project"] = inv["project"]
		p["channel"] = inv["channel"]
	}
	if endpoint := S(settings, "https_endpoint"); endpoint != "" && S(settings, "mode") == "member" {
		if err = HTTPSProfile(p, Object{"https_endpoint": endpoint, "gitea": p["gitea"]}); err != nil {
			return nil, nil, err
		}
	}
	if gp := S(settings, "gateway_profile"); gp != "" {
		g, e := Load(gp)
		if e != nil {
			return nil, nil, e
		}
		p["gateway"] = g
	}
	return p, NewClient(p, nil, false), nil
}
func ImportLegacy(path string) (Object, error) {
	p, err := Load(path)
	if err != nil {
		return nil, err
	}
	if S(p, "token") == "" || S(p, "actor") == "" {
		return nil, fmt.Errorf("existing enrolled identity required")
	}
	return Object{"mode": "member", "profile": path, "legacy_identity_preserved": true}, nil
}
func Send(ctx context.Context, c *Client, channel, session, text, target, reply string) (Object, error) {
	if session == "" {
		return nil, fmt.Errorf("bind the current agent or provide --session")
	}
	id := ID()
	body := Object{"channel": channel, "session": session, "client_id": id, "text": text, "kind": "message", "delivery": "both"}
	if target != "" {
		body["to_sessions"] = []string{target}
	}
	if reply != "" {
		return nil, fmt.Errorf("use the numeric --reply-to option")
	}
	outbox := filepath.Join(Home(), "outbox", id+".json")
	if err := Save(outbox, body); err != nil {
		return nil, err
	}
	r, err := c.Call(ctx, "POST", "/v1/messages", body)
	if err != nil {
		return nil, fmt.Errorf("send outcome uncertain; retry with otoha retry %s: %w", id, err)
	}
	return r, nil
}
func Benchmark(ctx context.Context, c *Client, count int) (Object, error) {
	if count < 1 || count > 100 {
		return nil, fmt.Errorf("sample count must be 1 to 100")
	}
	var ms []float64
	var failures []string
	for i := 0; i < count; i++ {
		start := time.Now()
		_, err := c.Call(ctx, "GET", "/health", nil)
		elapsed := float64(time.Since(start).Microseconds()) / 1000
		if err != nil {
			failures = append(failures, err.Error())
			c.Transport.Close()
		} else {
			ms = append(ms, elapsed)
		}
	}
	return Object{"samples_ms": ms, "failures": failures, "transport": c.Transport.Mode, "count": count, "measured_at": time.Now().UTC().Format(time.RFC3339)}, nil
}
func History(ctx context.Context, c *Client, channel string, before int) (Object, error) {
	path := "/v1/messages?channel=" + url.QueryEscape(channel)
	if before > 0 {
		path += "&before=" + fmt.Sprint(before)
	}
	return c.Call(ctx, "GET", path, nil)
}
