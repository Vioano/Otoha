package otoha

import (
	"bufio"
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func proxyConfig() (Object, error) {
	path := filepath.Join(Home(), "git-proxy.json")
	p, e := Load(path)
	if os.IsNotExist(e) {
		l, e := net.Listen("tcp", "127.0.0.1:0")
		if e != nil {
			return nil, e
		}
		address := l.Addr().String()
		l.Close()
		p = Object{"address": address, "username": "otoha-local", "password": ID() + ID()}
		e = Save(path, p)
		return p, e
	}
	return p, e
}
func GitProxy(ctx context.Context) error {
	local, e := proxyConfig()
	if e != nil {
		return e
	}
	settings, e := Load(filepath.Join(Home(), "config.json"))
	if e != nil {
		return e
	}
	p, c, e := OpenProfile(settings)
	if e != nil {
		return e
	}
	defer c.Transport.Close()
	g := O(p, "gitea")
	if gp := S(settings, "gitea_profile"); gp != "" {
		g, e = Load(gp)
		if e != nil {
			return e
		}
	}
	target, e := url.Parse(S(g, "url"))
	if e != nil || target.Host == "" {
		return fmt.Errorf("Gitea URL missing")
	}
	rp := NewGitReverseProxy(target, g, c.Transport.HTTP.Transport, local)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok || u != S(local, "username") || subtle.ConstantTimeCompare([]byte(p), []byte(S(local, "password"))) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="otoha-local"`)
			http.Error(w, "local credential required", 401)
			return
		}
		rp.ServeHTTP(w, r)
	})
	server := &http.Server{Addr: S(local, "address"), Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	go func() { <-ctx.Done(); server.Close() }()
	return server.ListenAndServe()
}

// NewGitReverseProxy keeps both CLI proxy entrances on the same Git/LFS
// authentication and action-URL rewriting path. An absent local username
// preserves the original anonymous-loopback proxy contract.
func NewGitReverseProxy(target *url.URL, g Object, transport http.RoundTripper, local Object) *httputil.ReverseProxy {
	rp := httputil.NewSingleHostReverseProxy(target)
	rp.Transport = transport
	old := rp.Director
	rp.Director = func(r *http.Request) {
		old(r)
		r.Host = target.Host
		r.Header.Del("Authorization")
		r.Header.Set("User-Agent", "Otoha-CLI/0.1.0")
		password := S(g, "password")
		if tok := S(g, "token"); tok != "" {
			// Git smart HTTP and LFS use HTTP Basic, including when the
			// password is an existing Gitea access token. The API-specific
			// Authorization: token scheme is rejected by the LFS endpoint.
			password = tok
		}
		r.SetBasicAuth(S(g, "username"), password)
	}
	// Gitea's old ROOT_URL is kept for existing clients. Translate only LFS
	// action URLs in this client's response, so file transfer stays on TLS.
	rp.ModifyResponse = func(resp *http.Response) error {
		if resp.StatusCode != 200 || !strings.HasSuffix(resp.Request.URL.Path, "/objects/batch") {
			return nil
		}
		raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		resp.Body.Close()
		if err != nil {
			return err
		}
		var body Object
		if err = json.Unmarshal(raw, &body); err != nil {
			return err
		}
		for _, object := range Items(body, "objects") {
			for _, value := range O(object, "actions") {
				action, ok := value.(map[string]any)
				if !ok {
					continue
				}
				u, err := url.Parse(S(Object(action), "href"))
				if err != nil {
					return err
				}
				if u.Host != target.Host && u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost" {
					return fmt.Errorf("LFS returned an unexpected origin")
				}
				path := u.Path
				if target.Path != "" {
					path = strings.TrimPrefix(path, strings.TrimRight(target.Path, "/"))
				}
				u.Scheme = "http"
				u.Host = S(local, "address")
				u.Path = path
				u.RawPath = ""
				u.User = nil
				action["href"] = u.String()
				headers := O(Object(action), "header")
				if S(local, "username") != "" {
					headers["Authorization"] = "Basic " + base64.StdEncoding.EncodeToString([]byte(S(local, "username")+":"+S(local, "password")))
				}
				action["header"] = headers
			}
		}
		raw, err = json.Marshal(body)
		if err != nil {
			return err
		}
		resp.Body = io.NopCloser(bytes.NewReader(raw))
		resp.ContentLength = int64(len(raw))
		resp.Header.Set("Content-Length", fmt.Sprint(len(raw)))
		return nil
	}
	return rp
}
func Credential(args []string, input io.Reader, output io.Writer) error {
	if len(args) == 0 || args[0] != "get" {
		return nil
	}
	fields := map[string]string{}
	scan := bufio.NewScanner(input)
	for scan.Scan() {
		line := scan.Text()
		if line == "" {
			break
		}
		key, value, ok := strings.Cut(line, "=")
		if ok {
			fields[key] = value
		}
	}
	p, e := Load(filepath.Join(Home(), "git-proxy.json"))
	if e != nil {
		return e
	}
	if fields["protocol"] == "http" && fields["host"] == S(p, "address") {
		fmt.Fprintf(output, "username=%s\npassword=%s\n\n", S(p, "username"), S(p, "password"))
	}
	return scan.Err()
}
func CloneRepo(ctx context.Context, args []string) error {
	if len(args) < 1 || len(args) > 2 || strings.Count(args[0], "/") != 1 || strings.Contains(args[0], "..") {
		return fmt.Errorf("otoha clone OWNER/REPO [DIRECTORY]")
	}
	parts := strings.Split(args[0], "/")
	for _, part := range parts {
		if e := Name(part); e != nil {
			return e
		}
	}
	p, e := proxyConfig()
	if e != nil {
		return e
	}
	if e = StartReceiver(); e != nil {
		return e
	}
	address := S(p, "address")
	ready := false
	for i := 0; i < 30; i++ {
		conn, e := net.DialTimeout("tcp", address, 100*time.Millisecond)
		if e == nil {
			conn.Close()
			ready = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !ready {
		return fmt.Errorf("Git proxy did not start; inspect otoha doctor and receiver.log")
	}
	executable, e := os.Executable()
	if e != nil {
		return e
	}
	helper := "!" + shellQuote(executable) + " credential"
	repoURL := "http://" + address + "/" + args[0] + ".git"
	gitArgs := []string{"-c", "credential.helper=", "-c", "credential.helper=" + helper, "clone", repoURL}
	directory := parts[1]
	if len(args) == 2 {
		directory = args[1]
		gitArgs = append(gitArgs, directory)
	}
	cmd := exec.CommandContext(ctx, "git", gitArgs...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if e = cmd.Run(); e != nil {
		return e
	}
	// Pin this repository to the same client home; no private server credential
	// or invitation is embedded in the remote URL or repository configuration.
	helper = "!OTOHA_HOME=" + shellQuote(Home()) + " " + shellQuote(executable) + " credential"
	for _, v := range [][]string{{"-C", directory, "config", "--local", "credential.helper", ""}, {"-C", directory, "config", "--local", "--add", "credential.helper", helper}} {
		if e = exec.CommandContext(ctx, "git", v...).Run(); e != nil {
			return e
		}
	}
	if _, err := exec.LookPath("git-lfs"); err == nil {
		for _, action := range []string{"install", "pull"} {
			gitArgs := []string{"-C", directory, "lfs", action}
			if action == "install" {
				gitArgs = append(gitArgs, "--local")
			}
			cmd := exec.CommandContext(ctx, "git", gitArgs...)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if err = cmd.Run(); err != nil {
				return fmt.Errorf("repository cloned; LFS %s failed: %w", action, err)
			}
		}
	} else if attrs, err := os.ReadFile(filepath.Join(directory, ".gitattributes")); err == nil && bytes.Contains(attrs, []byte("filter=lfs")) {
		return fmt.Errorf("repository cloned; install Git LFS to download its assets")
	}
	return nil
}
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'" }
