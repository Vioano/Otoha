package otoha

import (
	"bufio"
	"crypto/subtle"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
)

var pageRepositoryPartRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

func pageRepositoryName(cfg TeamConfig) (string, error) {
	repository := S(cfg.Repository, "repository")
	parts := strings.Split(repository, "/")
	if S(cfg.Repository, "transport") != "otoha" || len(parts) != 2 || !pageRepositoryPartRE.MatchString(parts[0]) || !pageRepositoryPartRE.MatchString(parts[1]) {
		return "", errors.New("team has no Otoha-authorized repository binding")
	}
	return repository, nil
}

func pageGitTarget(cfg TeamConfig, profile Object) (*url.URL, error) {
	repository, err := pageRepositoryName(cfg)
	if err != nil {
		return nil, err
	}
	authority, err := url.Parse(S(profile, "url"))
	if err != nil || authority.Host == "" || authority.User != nil || authority.RawQuery != "" || authority.Fragment != "" {
		return nil, errors.New("invalid team authority URL")
	}
	if authority.Scheme != "https" && !(authority.Scheme == "http" && (authority.Hostname() == "127.0.0.1" || authority.Hostname() == "localhost" || authority.Hostname() == "::1")) {
		return nil, errors.New("repository authority requires HTTPS or authenticated loopback transport")
	}
	target, err := url.Parse(S(cfg.Repository, "url"))
	expectedPath := strings.TrimRight(authority.Path, "/") + "/v1/git/" + cfg.Project + "/" + repository + ".git"
	if err != nil || target.User != nil || target.RawQuery != "" || target.Fragment != "" || target.RawPath != "" || target.Scheme != authority.Scheme || target.Host != authority.Host || target.Path != expectedPath {
		return nil, errors.New("repository endpoint does not match the team's authority, project and repository")
	}
	return target, nil
}

func pageGitSuffixAllowed(method, suffix, service string) bool {
	return method == http.MethodGet && suffix == "/info/refs" && (service == "git-upload-pack" || service == "git-receive-pack") ||
		method == http.MethodPost && (suffix == "/git-upload-pack" || suffix == "/git-receive-pack")
}

func (m *pageManager) serveGit(w http.ResponseWriter, r *http.Request) {
	username, password, ok := r.BasicAuth()
	if !ok || username != "otoha-local" || m.token == "" || subtle.ConstantTimeCompare([]byte(password), []byte(m.token)) != 1 {
		w.Header().Set("WWW-Authenticate", `Basic realm="otoha-local"`)
		http.Error(w, "local Git credential required", http.StatusUnauthorized)
		return
	}
	parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/git/"), "/", 4)
	if len(parts) != 4 || Name(parts[0]) != nil || !strings.HasSuffix(parts[2], ".git") {
		http.NotFound(w, r)
		return
	}
	cfg, err := loadTeamConfig(parts[0])
	if err != nil {
		http.Error(w, "team configuration unavailable", http.StatusNotFound)
		return
	}
	repository, err := pageRepositoryName(cfg)
	if err != nil || parts[1]+"/"+strings.TrimSuffix(parts[2], ".git") != repository {
		http.Error(w, "repository is outside this team's binding", http.StatusForbidden)
		return
	}
	suffix := "/" + parts[3]
	if !pageGitSuffixAllowed(r.Method, suffix, r.URL.Query().Get("service")) {
		http.Error(w, "only smart HTTP Git is supported by this repository channel", http.StatusNotFound)
		return
	}
	profile, client, err := teamProfile(cfg)
	if err != nil {
		http.Error(w, "team identity unavailable", http.StatusServiceUnavailable)
		return
	}
	defer client.Transport.Close()
	target, err := pageGitTarget(cfg, profile)
	if err != nil || S(profile, "token") == "" {
		http.Error(w, "repository authority binding is invalid", http.StatusBadGateway)
		return
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Transport = client.Transport.HTTP.Transport
	director := proxy.Director
	proxy.Director = func(request *http.Request) {
		// The matched local repository prefix is replaced, not concatenated
		// with an arbitrary client path. Only the three smart-Git RPCs above
		// are admitted; LFS is not claimed by this minimal channel.
		request.URL.Path, request.URL.RawPath = suffix, ""
		director(request)
		request.Host = target.Host
		request.Header.Del("Authorization")
		request.Header.Del("Proxy-Authorization")
		request.Header.Del("Cookie")
		request.Header.Set("Authorization", "Bearer "+S(profile, "token"))
		request.Header.Set("User-Agent", "Otoha-CLI/0.1.0")
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, _ error) {
		http.Error(w, "repository transport unavailable; retry after the team reconnects", http.StatusBadGateway)
	}
	proxy.ModifyResponse = func(response *http.Response) error {
		if response.StatusCode >= 300 && response.StatusCode < 400 {
			return errors.New("repository redirect is not an authorized endpoint")
		}
		return nil
	}
	proxy.ServeHTTP(w, r)
}

// PageGitCredential returns only this Home's local proxy credential, for the
// exact runtime host and the configured team's repository. It never stores a
// credential or reads/discloses the server's private Gitea identity.
func PageGitCredential(args []string, input io.Reader, output io.Writer) error {
	f := flag.NewFlagSet("page-git-credential", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	team := f.String("team", "", "joined team")
	if err := f.Parse(args); err != nil {
		return err
	}
	if len(f.Args()) == 0 || f.Args()[0] != "get" {
		return nil // Git store/erase have no effect on existing credentials.
	}
	if err := Name(*team); err != nil {
		return err
	}
	fields := map[string]string{}
	scan := bufio.NewScanner(io.LimitReader(input, 8193))
	for scan.Scan() {
		line := scan.Text()
		if line == "" {
			break
		}
		if key, value, ok := strings.Cut(line, "="); ok {
			fields[key] = value
		}
	}
	if err := scan.Err(); err != nil {
		return err
	}
	cfg, err := loadTeamConfig(*team)
	if err != nil {
		return err
	}
	repository, err := pageRepositoryName(cfg)
	if err != nil {
		return err
	}
	local, err := Load(filepath.Join(Home(), "page-runtime.json"))
	if err != nil {
		return err
	}
	runtimeURL, err := url.Parse(S(local, "url"))
	if err != nil || runtimeURL.Scheme != "http" || runtimeURL.Hostname() != "127.0.0.1" || runtimeURL.Port() == "" || runtimeURL.User != nil || runtimeURL.RawQuery != "" || runtimeURL.Fragment != "" || S(local, "token") == "" {
		return errors.New("invalid local page runtime endpoint")
	}
	expectedPath := "git/" + *team + "/" + repository + ".git"
	path := strings.TrimPrefix(fields["path"], "/")
	if fields["protocol"] == "http" && fields["host"] == runtimeURL.Host && path == expectedPath && (fields["username"] == "" || fields["username"] == "otoha-local") {
		_, err = fmt.Fprintf(output, "username=otoha-local\npassword=%s\n\n", S(local, "token"))
		return err
	}
	return nil
}
