package otoha

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

func teamClientContext(result Object, cfg TeamConfig) {
	result["authority_url"] = cfg.URL
	result["origin_url"] = cfg.RepositoryURL
	if cfg.Checkout != "" {
		result["checkout"] = cfg.Checkout
	}
	commands := Object{"page": "otoha page open --team " + cfg.Name + " --browser", "messages": "otoha message list --team " + cfg.Name}
	if cfg.Checkout != "" {
		commands["git_status"] = "git -C " + shellQuote(cfg.Checkout) + " status"
		commands["git_remote"] = "git -C " + shellQuote(cfg.Checkout) + " ls-remote origin HEAD"
	}
	result["commands"] = commands
	if S(cfg.Repository, "transport") == "otoha" {
		result["repository"] = Object{"transport": "otoha", "name": S(cfg.Repository, "repository"), "authority_url": S(cfg.Repository, "url"), "origin_url": cfg.RepositoryURL, "checkout": cfg.Checkout, "access": S(cfg.Repository, "access")}
	}
}

// LocalStatus observes local metadata and an already-running daemon. It never
// starts a service, changes credentials or contacts a remote authority.
func LocalStatus(ctx context.Context) (Object, error) {
	daemon, err := DaemonStatus()
	if err != nil {
		return nil, err
	}
	teams := []any{}
	for _, name := range teamNamesForDoctor() {
		cfg, err := loadTeamConfig(name)
		if err != nil {
			teams = append(teams, Object{"team": name, "connection": "unknown", "ready": false, "error": err.Error()})
			continue
		}
		identityState := "missing"
		if _, err := os.Stat(cfg.Profile); err == nil {
			identityState = "present"
		}
		row := Object{"team": cfg.Name, "name": cfg.Name, "project": cfg.Project, "mode": cfg.Mode, "identity_state": identityState, "connection": "unknown", "ready": false}
		repositoryReady := cfg.RepositoryURL == "" && S(cfg.Repository, "transport") == ""
		row["repository_state"] = "not_configured"
		if !repositoryReady {
			row["repository_state"] = "incomplete"
			if cfg.Checkout != "" && cfg.State == "ready" {
				if _, err := os.Stat(filepath.Join(cfg.Checkout, ".git")); err == nil {
					repositoryReady = true
					row["repository_state"] = "available"
				}
			}
		}
		teamClientContext(row, cfg)
		probe, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
		state, stateErr := pageLocalCall(probe, http.MethodGet, "/api/state?team="+url.QueryEscape(name), nil)
		cancel()
		if stateErr == nil {
			row["connection"], row["last_contact"], row["cursor"] = S(state, "connection"), state["last_contact"], state["cursor"]
			row["ready"] = S(state, "connection") == "online" && repositoryReady
		}
		teams = append(teams, row)
	}
	state, next := "not_joined", "otoha join --invite FILE"
	if len(teams) > 0 {
		state, next = "joined", "otoha team status "+S(teams[0].(Object), "team")
	}
	_, legacyErr := os.Stat(filepath.Join(Home(), "config.json"))
	return Object{"schema": 1, "state": state, "teams": teams, "daemon": daemon, "legacy_configured": legacyErr == nil, "next": next}, nil
}
