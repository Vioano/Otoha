package otoha

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
)

// Repository access belongs to the existing Otoha project identity. A member
// receives this binding from the authenticated authority, never Gitea secrets.
func discoverJoinedRepository(ctx context.Context, cfg TeamConfig) (Object, error) {
	_, client, err := teamProfile(cfg)
	if err != nil {
		return nil, err
	}
	defer client.Transport.Close()
	binding, err := client.Call(ctx, http.MethodGet, "/v1/repository?project="+url.QueryEscape(cfg.Project), nil)
	if IsStatus(err, http.StatusNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	repository, err := pageRepositoryName(TeamConfig{Repository: binding})
	if err != nil {
		return nil, errors.New("authority returned an invalid repository binding")
	}
	expected := strings.TrimRight(cfg.URL, "/") + "/v1/git/" + cfg.Project + "/" + repository + ".git"
	if S(binding, "url") != expected {
		return nil, errors.New("repository binding differs from this project's authority; existing state preserved")
	}
	return Object{"transport": "otoha", "repository": repository, "url": expected, "access": S(binding, "access")}, nil
}

func finishManagedTeamJoin(ctx context.Context, cfg TeamConfig, binding Object, directory string, result, readiness Object) (Object, error) {
	if directory == "" {
		directory = cfg.Checkout
	}
	if directory == "" {
		directory = filepath.Join(teamRoot(cfg.Name), "checkout")
	}
	directory, err := filepath.Abs(directory)
	if err != nil {
		return result, err
	}
	if cfg.Checkout != "" && filepath.Clean(cfg.Checkout) != directory {
		return result, errors.New("team already has another checkout; existing directory preserved")
	}
	cfg.Checkout = directory
	teamClientContext(result, cfg)
	if S(cfg.Repository, "transport") == "otoha" && (S(cfg.Repository, "url") != S(binding, "url") || S(cfg.Repository, "repository") != S(binding, "repository")) {
		readiness["repository"] = "error"
		return result, errors.New("project repository binding changed; existing checkout preserved")
	}
	cfg.Repository = binding
	teamClientContext(result, cfg)
	if err := saveTeamConfig(cfg); err != nil {
		return result, err
	}
	// The local Git proxy reads only the durable binding, so starting it does
	// not require a clone, a checkout path or the member's Gitea credentials.
	page, err := ensureJoinedPageService(ctx, cfg.Name)
	if err != nil {
		readiness["service"] = "error"
		return result, fmt.Errorf("identity and project ready; local service is not ready: %w", err)
	}
	readiness["service"], readiness["communication"] = "ready", S(page, "connection")
	result["page"] = page
	local, err := url.Parse(S(page, "local_url"))
	if err != nil || local.Scheme != "http" || local.Hostname() != "127.0.0.1" || local.Port() == "" || local.User != nil || local.RawQuery != "" || local.Fragment != "" || (local.Path != "" && local.Path != "/") {
		return result, errors.New("local Otoha service returned an invalid Git endpoint")
	}
	repositoryURL := strings.TrimRight(local.String(), "/") + "/git/" + cfg.Name + "/" + S(binding, "repository") + ".git"
	if cfg.RepositoryURL != "" && cfg.RepositoryURL != repositoryURL {
		readiness["repository"] = "error"
		return result, errors.New("local repository origin changed; existing checkout preserved")
	}
	cfg.RepositoryURL, cfg.Checkout = repositoryURL, directory
	teamClientContext(result, cfg)
	if err = saveTeamConfig(cfg); err != nil {
		return result, err
	}
	executable, _, err := installedJoinExecutable()
	if err != nil {
		return result, err
	}
	helper := "!OTOHA_HOME=" + shellQuote(Home()) + " " + shellQuote(executable) + " page-git-credential --team " + shellQuote(cfg.Name)
	if err = checkoutTeamRepositoryWithHelper(ctx, Object{"url": repositoryURL}, directory, helper); err != nil {
		readiness["repository"] = "error"
		result["next"] = "inspect otoha team status, then repeat the same join command"
		return result, fmt.Errorf("collaboration joined; repository is not ready: %w", err)
	}
	if err = ensureJoinedRepositoryIdentity(ctx, directory, cfg.Profile); err != nil {
		readiness["repository"] = "error"
		return result, err
	}
	readiness["repository"], result["checkout"], result["ready"] = "ready", directory, B(page, "ready")
	result["next"] = S(O(result, "commands"), "page")
	if B(page, "ready") && cfg.Mode != "local" {
		cfg.State = "ready"
	}
	if err = saveTeamConfig(cfg); err != nil {
		return result, err
	}
	return result, nil
}
