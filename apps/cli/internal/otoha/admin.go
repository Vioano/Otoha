package otoha

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"golang.org/x/crypto/ssh"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var nameRE = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,39}$`)
var aliasRE = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

func Name(s string) error {
	if !nameRE.MatchString(s) {
		return fmt.Errorf("use a name starting with a lowercase letter and at most 40 letters, digits, underscores or hyphens")
	}
	return nil
}

type Admin struct {
	Settings   Object
	Otoha, Git *Client
	Root       string
}

func OpenAdmin(settings Object) (*Admin, error) {
	p, err := Load(S(settings, "otoha_profile"))
	if err != nil {
		return nil, err
	}
	g, err := Load(S(settings, "gitea_profile"))
	if err != nil {
		return nil, err
	}
	gateway := O(p, "gateway")
	if path := S(settings, "gateway_profile"); path != "" {
		gateway, err = Load(path)
		if err != nil {
			return nil, err
		}
	}
	p["gateway"] = gateway
	t := NewTransport(gateway)
	return &Admin{settings, NewClient(p, t, false), NewClient(g, t, true), filepath.Join(Home(), "admin")}, nil
}
func (a *Admin) Check(ctx context.Context) error {
	m, err := a.Otoha.Call(ctx, "GET", "/v1/manifest", nil)
	if err != nil {
		return err
	}
	if !B(O(m, "actor"), "owner") {
		return fmt.Errorf("captain identity required")
	}
	return nil
}
func (a *Admin) Create(ctx context.Context, project, purpose string) (Object, error) {
	if err := Name(project); err != nil {
		return nil, err
	}
	if err := a.Check(ctx); err != nil {
		return nil, err
	}
	owner := S(a.Git.Profile, "username")
	if owner == "" {
		u, err := a.Git.Call(ctx, "GET", "/api/v1/user", nil)
		if err != nil {
			return nil, err
		}
		owner = S(u, "login")
	}
	repo := owner + "/" + project
	r, err := a.Git.Call(ctx, "GET", "/api/v1/repos/"+repo, nil)
	if IsStatus(err, 404) {
		r, err = a.Git.Call(ctx, "POST", "/api/v1/user/repos", Object{"name": project, "private": true, "auto_init": true, "default_branch": "main", "description": purpose})
	}
	if err != nil {
		return nil, err
	}
	if !B(r, "private") {
		return nil, fmt.Errorf("existing repository is public; refusing to change its visibility")
	}
	ps, err := a.Otoha.Call(ctx, "GET", "/v1/projects", nil)
	if err != nil {
		return nil, err
	}
	found := false
	for _, p := range Items(ps, "projects") {
		if S(p, "id") == project {
			found = true
		}
	}
	if !found {
		if _, err = a.Otoha.Call(ctx, "POST", "/v1/projects", Object{"id": project, "name": project}); err != nil && !IsStatus(err, 409) {
			return nil, err
		}
	}
	channel := project + "-general"
	if len(channel) > 80 {
		return nil, fmt.Errorf("channel name too long")
	}
	cs, err := a.Otoha.Call(ctx, "GET", "/v1/channels", nil)
	if err != nil {
		return nil, err
	}
	found = false
	for _, c := range Items(cs, "channels") {
		if S(c, "id") == channel {
			if S(c, "project") != project {
				return nil, fmt.Errorf("channel exists in another project")
			}
			found = true
		}
	}
	if !found {
		if _, err = a.Otoha.Call(ctx, "POST", "/v1/channels", Object{"id": channel, "project": project, "name": project}); err != nil && !IsStatus(err, 409) {
			return nil, err
		}
	}
	if purpose != "" {
		_, err = a.Git.Call(ctx, "GET", "/api/v1/repos/"+repo+"/contents/AGENTS.md", nil)
		if IsStatus(err, 404) {
			_, err = a.Git.Call(ctx, "POST", "/api/v1/repos/"+repo+"/contents/AGENTS.md", Object{"content": base64.StdEncoding.EncodeToString([]byte("# " + project + "\n\n" + purpose + "\n")), "message": "Describe project purpose"})
		}
		if err != nil {
			return nil, err
		}
	}
	return Object{"repository": repo, "project": project, "channel": channel, "private": true}, nil
}
func (a *Admin) Grant(ctx context.Context, repo, project, actor, role string) (Object, error) {
	if err := Name(actor); err != nil {
		return nil, err
	}
	if role != "reader" && role != "member" {
		return nil, fmt.Errorf("role must be reader or member")
	}
	permission := "write"
	if role == "reader" {
		permission = "read"
	}
	if _, err := a.Git.Call(ctx, "PUT", "/api/v1/repos/"+repo+"/collaborators/"+actor, Object{"permission": permission}); err != nil {
		return nil, err
	}
	if _, err := a.Otoha.Call(ctx, "POST", "/v1/grants", Object{"actor": actor, "project": project, "role": role}); err != nil {
		return nil, err
	}
	access := Object{"protocol": "otoha-access/1", "actor": actor, "url": S(a.Otoha.Profile, "url"), "project": project, "channel": project + "-general", "gitea": Object{"repository": repo}}
	if endpoint := S(a.Settings, "https_endpoint"); endpoint != "" {
		access["https_endpoint"] = endpoint
	}
	key, err := Pack(access)
	if err != nil {
		return nil, err
	}
	return Object{"actor": actor, "repository": repo, "project": project, "role": role, "existing_credentials_preserved": true, "key": key}, nil
}
func (a *Admin) Invite(ctx context.Context, repo, project, actor, role string, days int) (Object, error) {
	if err := Name(actor); err != nil {
		return nil, err
	}
	if err := Name(project); err != nil {
		return nil, err
	}
	if role != "reader" && role != "member" {
		return nil, fmt.Errorf("role must be reader or member")
	}
	if days < 1 || days > 7 {
		return nil, fmt.Errorf("invitation lifetime must be 1 to 7 days")
	}
	if err := a.Check(ctx); err != nil {
		return nil, err
	}
	actors, err := a.Otoha.Call(ctx, "GET", "/v1/actors", nil)
	if err != nil {
		return nil, err
	}
	for _, u := range Items(actors, "actors") {
		if S(u, "id") == actor {
			if B(u, "owner") || N(u, "disabled") != 0 {
				return nil, fmt.Errorf("cannot invite an owner or disabled identity")
			}
			return a.Grant(ctx, repo, project, actor, role)
		}
	}
	r, err := a.Git.Call(ctx, "GET", "/api/v1/repos/"+repo, nil)
	if err != nil {
		return nil, err
	}
	if !B(r, "private") {
		return nil, fmt.Errorf("private repository required")
	}
	path := filepath.Join(a.Root, actor+".json")
	record, err := Load(path)
	if os.IsNotExist(err) {
		record = Object{"actor": actor, "password": ID() + ID(), "project": project, "repo": repo, "role": role, "state": "pending"}
		pub, priv, e := ed25519.GenerateKey(rand.Reader)
		if e != nil {
			return nil, e
		}
		block, e := ssh.MarshalPrivateKey(priv, "otoha-member-"+actor)
		if e != nil {
			return nil, e
		}
		key, e := ssh.NewPublicKey(pub)
		if e != nil {
			return nil, e
		}
		record["private_key"] = string(pem.EncodeToMemory(block))
		record["public_key"] = strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))
		if e = Save(path, record); e != nil {
			return nil, e
		}
	} else if err != nil {
		return nil, err
	}
	if S(record, "project") != project || S(record, "repo") != repo || S(record, "role") != role {
		return nil, fmt.Errorf("a different enrollment is pending; preserve it or explicitly cancel it")
	}
	user, err := a.Git.Call(ctx, "GET", "/api/v1/users/"+actor, nil)
	if IsStatus(err, 404) {
		user, err = a.Git.Call(ctx, "POST", "/api/v1/admin/users", Object{"username": actor, "full_name": actor, "email": actor + "@otoha.invalid", "password": S(record, "password"), "must_change_password": false, "send_notify": false})
		if err == nil {
			record["git_created"] = true
			err = Save(path, record)
		}
	} else if err == nil && !B(record, "git_created") {
		return nil, fmt.Errorf("Gitea identity already exists; use its existing invitation or grant, never reset its password")
	}
	if err != nil {
		return nil, err
	}
	if B(user, "is_admin") {
		return nil, fmt.Errorf("refusing to enroll an administrator as a new member")
	}
	permission := "write"
	if role == "reader" {
		permission = "read"
	}
	if _, err = a.Git.Call(ctx, "PUT", "/api/v1/repos/"+repo+"/collaborators/"+actor, Object{"permission": permission}); err != nil {
		return nil, err
	}
	if !B(record, "gateway_registered") {
		alias := S(a.Settings, "gateway_admin")
		if !aliasRE.MatchString(alias) || strings.HasPrefix(alias, "-") {
			return nil, fmt.Errorf("trusted gateway administrator SSH alias required")
		}
		cp := filepath.Join(a.Root, "ssh-%C")
		if err = PrivateDir(a.Root); err != nil {
			return nil, err
		}
		cmd := exec.CommandContext(ctx, "ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=8", "-o", "ControlMaster=auto", "-o", "ControlPersist=60", "-o", "ControlPath="+cp, alias, "sudo -n python3 /usr/local/libexec/otoha-collaboration/gateway_members.py add "+actor+" --public-key-base64 "+base64.StdEncoding.EncodeToString([]byte(S(record, "public_key"))))
		if b, e := cmd.CombinedOutput(); e != nil {
			return nil, fmt.Errorf("gateway key registration: %w (%s)", e, strings.TrimSpace(string(b)))
		}
		record["gateway_registered"] = true
		if err = Save(path, record); err != nil {
			return nil, err
		}
	}
	invitation := O(record, "invitation")
	if len(invitation) == 0 {
		invitation, err = a.Otoha.Call(ctx, "POST", "/v1/invites", Object{"actor": actor, "name": actor, "grants": Object{project: role}, "ttl_seconds": days * 86400})
		if err != nil {
			return nil, err
		}
		record["invitation"] = invitation
		if err = Save(path, record); err != nil {
			return nil, err
		}
	}
	if alias := S(a.Settings, "server_admin"); alias != "" && !B(record, "lan_registered") {
		if !aliasRE.MatchString(alias) || strings.HasPrefix(alias, "-") {
			return nil, fmt.Errorf("invalid private server administrator alias")
		}
		cmd := exec.CommandContext(ctx, "ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=4", alias, "sudo -n python3 /usr/local/libexec/otoha-cli/add-key.py "+actor+" "+base64.StdEncoding.EncodeToString([]byte(S(record, "public_key"))))
		if _, e := cmd.CombinedOutput(); e == nil {
			record["lan_registered"] = true
			Save(path, record)
		}
	}
	gatewayPath := S(a.Settings, "member_gateway_profile")
	if gatewayPath == "" {
		gatewayPath = S(a.Settings, "gateway_profile")
	}
	gateway, err := Load(gatewayPath)
	if err != nil {
		return nil, err
	}
	g := Object{}
	for _, k := range []string{"host", "port", "user", "host_public_key", "forwards", "routes"} {
		if v, ok := gateway[k]; ok {
			g[k] = v
		}
	}
	if S(g, "host_public_key") == "" {
		raw, e := os.ReadFile(S(gateway, "known_hosts_file"))
		if e != nil {
			return nil, e
		}
		for _, line := range strings.Split(string(raw), "\n") {
			f := strings.Fields(line)
			if len(f) >= 3 && !strings.HasPrefix(f[0], "#") {
				g["host_public_key"] = strings.Join(f[1:3], " ")
				break
			}
		}
		if S(g, "host_public_key") == "" {
			return nil, fmt.Errorf("pinned gateway host key is missing")
		}
	}
	g["private_key"] = S(record, "private_key")
	keyData := Clone(invitation)
	keyData["url"] = S(a.Otoha.Profile, "url")
	keyData["gateway"] = g
	keyData["gitea"] = Object{"url": S(a.Git.Profile, "url"), "username": actor, "password": S(record, "password"), "repository": repo}
	keyData["project"] = project
	keyData["channel"] = project + "-general"
	if endpoint := S(a.Settings, "https_endpoint"); endpoint != "" {
		keyData["https_endpoint"] = endpoint
	}
	key, err := Pack(keyData)
	if err != nil {
		return nil, err
	}
	record["state"] = "invited"
	record["key"] = key
	if err = Save(path, record); err != nil {
		return nil, err
	}
	return Object{"actor": actor, "repository": repo, "project": project, "role": role, "key": key, "key_characters": len(key), "invitation_expires": invitation["expires"], "enrolled_identity_expires": false, "issued_at": time.Now().UTC().Format(time.RFC3339)}, nil
}
