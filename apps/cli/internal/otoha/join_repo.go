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
	"strings"
)

// The service implementation is shared with `otoha page`; tests replace this
// boundary without starting an unrelated receiver or changing a user's services.
var ensureJoinedPageService = EnsurePageService

// Relative credential-file paths belong to the gateway profile's directory,
// not to the invoking shell or the daemon's working directory. Only the new
// member identity receives normalized paths; the original profile is read-only.
func loadLocalTeamGateway(path string) (Object, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	gateway, err := Load(absolute)
	if err != nil {
		return nil, err
	}
	base := filepath.Dir(absolute)
	normalize := func(route Object) Object {
		route = Clone(route)
		for _, field := range []string{"key_file", "known_hosts_file"} {
			if value := S(route, field); value != "" && !filepath.IsAbs(value) {
				route[field] = filepath.Join(base, value)
			}
		}
		return route
	}
	gateway = normalize(gateway)
	if routes, ok := gateway["routes"].([]any); ok {
		normalized := make([]any, len(routes))
		for i, value := range routes {
			if route, ok := value.(map[string]any); ok {
				normalized[i] = normalize(Object(route))
			} else {
				normalized[i] = value
			}
		}
		gateway["routes"] = normalized
	}
	return gateway, nil
}

func validateTeamGateway(endpoint string, gateway Object) error {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost" && u.Hostname() != "::1") {
		return errors.New("--gateway-profile requires a loopback HTTP service URL; HTTPS keeps its direct transport")
	}
	if S(gateway, "host") == "" {
		return errors.New("local gateway profile is missing host")
	}
	if S(gateway, "key_file") == "" && S(gateway, "private_key") == "" {
		return errors.New("local gateway profile is missing key_file")
	}
	if S(gateway, "host_public_key") == "" && S(gateway, "known_hosts_file") == "" {
		return errors.New("local gateway profile requires host_public_key or known_hosts_file")
	}
	return nil
}

func verifyJoinedProject(ctx context.Context, cfg TeamConfig) error {
	profile, client, err := teamProfile(cfg)
	if err != nil {
		return err
	}
	defer client.Transport.Close()
	manifest, err := client.Call(ctx, http.MethodGet, "/v1/manifest", nil)
	if err != nil {
		return err
	}
	actor := O(manifest, "actor")
	if S(actor, "id") != S(profile, "actor") {
		return errors.New("authenticated actor differs from the joined identity")
	}
	if B(actor, "owner") {
		return nil
	}
	for _, grant := range Items(manifest, "grants") {
		if S(grant, "project") == cfg.Project && S(grant, "role") != "" {
			return nil
		}
	}
	return errors.New("the existing identity has no grant for this project")
}

// Invitations carry repository locations, never passwords, helper commands or
// SSH keys. Authentication stays in the member's existing Git/SSH environment.
func validateJoinRepository(repository Object) error {
	raw, branch := S(repository, "url"), S(repository, "branch")
	if raw == "" {
		if branch != "" {
			return errors.New("--branch requires --repository")
		}
		return nil
	}
	if strings.ContainsAny(raw, "\x00\r\n\t ") || strings.HasPrefix(raw, "-") {
		return errors.New("invalid repository URL")
	}
	if !strings.Contains(raw, "://") {
		// Git's conventional user@host:owner/repository.git SSH locator.
		parts := strings.SplitN(raw, ":", 2)
		if len(parts) != 2 || !strings.Contains(parts[0], "@") || strings.ContainsAny(parts[0], `/\\`) || parts[1] == "" || strings.Contains(parts[1], "..") {
			return errors.New("repository must use HTTPS, SSH, or loopback HTTP")
		}
	} else {
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" || u.Path == "" || u.RawQuery != "" || u.Fragment != "" {
			return errors.New("invalid repository URL")
		}
		if u.User != nil {
			_, password := u.User.Password()
			if u.Scheme != "ssh" || password {
				return errors.New("repository invitations cannot contain credentials")
			}
		}
		switch u.Scheme {
		case "https", "ssh":
		case "http":
			if u.Hostname() != "localhost" && u.Hostname() != "127.0.0.1" && u.Hostname() != "::1" {
				return errors.New("HTTP repository URLs require an authenticated loopback path; use HTTPS or SSH")
			}
		default:
			return errors.New("repository must use HTTPS, SSH, or loopback HTTP")
		}
	}
	if branch != "" {
		if strings.HasPrefix(branch, "-") || strings.ContainsAny(branch, "\x00\r\n") {
			return errors.New("invalid repository branch")
		}
		if err := exec.Command("git", "check-ref-format", "--branch", branch).Run(); err != nil {
			return errors.New("invalid repository branch")
		}
	}
	return nil
}

func checkoutTeamRepository(ctx context.Context, repository Object, directory string, giteaProfiles ...string) error {
	if err := validateJoinRepository(repository); err != nil {
		return err
	}
	raw := S(repository, "url")
	if raw == "" {
		return nil
	}
	helper := ""
	if len(giteaProfiles) > 0 && giteaProfiles[0] != "" {
		var err error
		helper, err = joinGiteaHelper(giteaProfiles[0], raw)
		if err != nil {
			return err
		}
	}
	return checkoutTeamRepositoryWithHelper(ctx, repository, directory, helper)
}

func checkoutTeamRepositoryWithHelper(ctx context.Context, repository Object, directory, helper string) error {
	if err := validateJoinRepository(repository); err != nil {
		return err
	}
	raw, branch := S(repository, "url"), S(repository, "branch")
	if raw == "" {
		return nil
	}
	if _, err := os.Stat(directory); err == nil {
		root, gitErr := exec.CommandContext(ctx, "git", "-C", directory, "rev-parse", "--show-toplevel").Output()
		if gitErr == nil {
			actualRoot, err := filepath.EvalSymlinks(strings.TrimSpace(string(root)))
			if err != nil {
				return err
			}
			expectedRoot, err := filepath.EvalSymlinks(directory)
			if err != nil || actualRoot != expectedRoot {
				return errors.New("destination belongs to another repository; refusing to modify it")
			}
			origin, err := exec.CommandContext(ctx, "git", "-C", directory, "remote", "get-url", "origin").Output()
			if err != nil || strings.TrimSpace(string(origin)) != raw {
				return errors.New("destination repository has another origin; refusing to modify it")
			}
			if err := verifyTeamCheckoutCommit(ctx, directory); err != nil {
				return err
			}
			if branch != "" {
				current, err := exec.CommandContext(ctx, "git", "-C", directory, "symbolic-ref", "--short", "HEAD").Output()
				if err != nil || strings.TrimSpace(string(current)) != branch {
					return errors.New("destination uses another branch; refusing to switch it")
				}
			}
			// A repeated join verifies the existing checkout without pulling,
			// resetting, changing original credentials or overwriting local work.
			if helper != "" {
				return saveJoinCredentialHelper(ctx, directory, helper)
			}
			return nil
		}
		entries, err := os.ReadDir(directory)
		if err != nil {
			return err
		}
		if len(entries) != 0 {
			return errors.New("destination is not empty; refusing to overwrite it")
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(directory), 0755); err != nil {
		return err
	}
	args := []string{"-c", "credential.interactive=never"}
	if helper != "" {
		args = append(args, "-c", "credential.helper=", "-c", "credential.helper="+helper, "-c", "credential.useHttpPath=true", "-c", "http.followRedirects=false")
	}
	args = append(args, "clone")
	if branch != "" {
		args = append(args, "--branch", branch)
	}
	args = append(args, "--", raw, directory)
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if err := cmd.Run(); err != nil {
		// Do not echo Git stderr: an existing credential helper or SSH wrapper
		// can include private configuration in diagnostics.
		return fmt.Errorf("Git clone failed (%w); check the member's existing Git/SSH access", err)
	}
	if err := verifyTeamCheckoutCommit(ctx, directory); err != nil {
		return err
	}
	if helper != "" {
		return saveJoinCredentialHelper(ctx, directory, helper)
	}
	return nil
}

func verifyTeamCheckoutCommit(ctx context.Context, directory string) error {
	if err := exec.CommandContext(ctx, "git", "-C", directory, "rev-parse", "--verify", "HEAD^{commit}").Run(); err != nil {
		return errors.New("repository checkout is incomplete: HEAD has no commit; existing directory preserved")
	}
	return nil
}
