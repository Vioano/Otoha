package otoha

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

type APIError struct {
	Status int
	Code   string
}

func (e *APIError) Error() string    { return fmt.Sprintf("HTTP %d: %s", e.Status, e.Code) }
func IsStatus(err error, s int) bool { e, ok := err.(*APIError); return ok && e.Status == s }

// A single authenticated SSH connection carries pooled HTTP connections for
// Git and Otoha. No system proxy, route, DNS, firewall or old profile is changed.
type Transport struct {
	Gateway Object
	active  Object
	mu      sync.Mutex
	ssh     *ssh.Client
	HTTP    *http.Client
	Mode    string
}

func NewTransport(g Object) *Transport {
	t := &Transport{Gateway: g, Mode: "direct"}
	tr := &http.Transport{Proxy: nil, MaxIdleConns: 32, MaxIdleConnsPerHost: 8, IdleConnTimeout: 90 * time.Second, ResponseHeaderTimeout: 60 * time.Second, ForceAttemptHTTP2: true}
	if S(g, "host") != "" {
		t.Mode = "ssh"
		tr.DialContext = t.dial
	} else {
		tr.DialContext = (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 20 * time.Second}).DialContext
	}
	t.HTTP = &http.Client{Transport: tr, Timeout: 65 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 5 {
			return fmt.Errorf("too many redirects")
		}
		if len(via) > 0 && req.URL.Host != via[0].URL.Host {
			return fmt.Errorf("cross-host redirect refused")
		}
		return nil
	}}
	return t
}
func (t *Transport) connect(ctx context.Context) (*ssh.Client, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.ssh != nil {
		return t.ssh, nil
	}
	routes := Items(t.Gateway, "routes")
	routes = append(routes, t.Gateway)
	var errors []string
	for i, g := range routes {
		merged := Clone(t.Gateway)
		delete(merged, "routes")
		for k, v := range g {
			merged[k] = v
		}
		timeout := 8 * time.Second
		if i < len(routes)-1 {
			// Local discovery and the first WiFi TCP handshake can exceed
			// 800 ms. Premature fallback puts a local bulk transfer on WAN.
			timeout = 2 * time.Second
		}
		c, e := t.connectGateway(ctx, merged, timeout)
		if e != nil {
			errors = append(errors, e.Error())
			continue
		}
		t.ssh = c
		t.active = merged
		t.Mode = "ssh:" + S(merged, "host")
		go t.keepAlive(c)
		return c, nil
	}
	return nil, fmt.Errorf("all configured routes failed: %s", strings.Join(errors, "; "))
}
func (t *Transport) keepAlive(client *ssh.Client) {
	for {
		time.Sleep(5 * time.Second)
		answer := make(chan error, 1)
		go func() { _, _, err := client.SendRequest("keepalive@openssh.com", true, nil); answer <- err }()
		var err error
		select {
		case err = <-answer:
		case <-time.After(4 * time.Second):
			err = fmt.Errorf("SSH keepalive timed out")
		}
		if err != nil {
			client.Close()
			t.mu.Lock()
			if t.ssh == client {
				t.ssh = nil
			}
			t.mu.Unlock()
			return
		}
	}
}
func (t *Transport) connectGateway(ctx context.Context, g Object, timeout time.Duration) (*ssh.Client, error) {
	key := S(g, "private_key")
	if key == "" {
		b, err := os.ReadFile(S(g, "key_file"))
		if err != nil {
			return nil, err
		}
		key = string(b)
	}
	signer, err := ssh.ParsePrivateKey([]byte(key))
	if err != nil {
		return nil, fmt.Errorf("invalid personal SSH key: %w", err)
	}
	var callback ssh.HostKeyCallback
	var hostAlgorithms []string
	if pin := S(g, "host_public_key"); pin != "" {
		p, _, _, _, err := ssh.ParseAuthorizedKey([]byte(pin))
		if err != nil {
			return nil, err
		}
		callback = func(_ string, _ net.Addr, k ssh.PublicKey) error {
			if !bytes.Equal(k.Marshal(), p.Marshal()) {
				return fmt.Errorf("server host key mismatch")
			}
			return nil
		}
		hostAlgorithms = []string{p.Type()}
	} else {
		callback, err = knownhosts.New(S(g, "known_hosts_file"))
		if err != nil {
			return nil, err
		}
	}
	port := N(g, "port")
	if port == 0 {
		port = 22
	}
	addr := net.JoinHostPort(S(g, "host"), strconv.Itoa(port))
	conn, err := (&net.Dialer{Timeout: timeout, KeepAlive: 20 * time.Second}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("SSH network connection: %w", err)
	}
	deadline := time.Now().Add(timeout + 4*time.Second)
	if limit, ok := ctx.Deadline(); ok && limit.Before(deadline) {
		deadline = limit
	}
	conn.SetDeadline(deadline)
	user := S(g, "user")
	if user == "" {
		user = "otoha-access"
	}
	c, ch, rq, err := ssh.NewClientConn(conn, addr, &ssh.ClientConfig{User: user, Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)}, HostKeyCallback: callback, HostKeyAlgorithms: hostAlgorithms, Timeout: 12 * time.Second})
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("SSH handshake/authentication: %w", err)
	}
	conn.SetDeadline(time.Time{})
	return ssh.NewClient(c, ch, rq), nil
}
func (t *Transport) dial(ctx context.Context, network, addr string) (net.Conn, error) {
	host, _, e := net.SplitHostPort(addr)
	if e == nil && host != "127.0.0.1" && host != "localhost" && host != "::1" {
		return (&net.Dialer{Timeout: 8 * time.Second, KeepAlive: 20 * time.Second}).DialContext(ctx, network, addr)
	}
	client, err := t.connect(ctx)
	if err != nil {
		return nil, err
	}
	_, p, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	local, _ := strconv.Atoi(p)
	remote := 0
	for _, f := range Items(t.active, "forwards") {
		if N(f, "local") == local {
			remote = N(f, "remote")
			break
		}
	}
	if remote == 0 {
		return nil, fmt.Errorf("port %d is outside the configured forwarding scope", local)
	}
	conn, err := client.DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(remote)))
	if err != nil {
		t.mu.Lock()
		if t.ssh == client {
			client.Close()
			t.ssh = nil
		}
		t.mu.Unlock()
	}
	return conn, err
}
func (t *Transport) Close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.ssh != nil {
		t.ssh.Close()
		t.ssh = nil
	}
	t.HTTP.CloseIdleConnections()
}

type Client struct {
	Profile   Object
	Transport *Transport
	Git       bool
}

func NewClient(p Object, t *Transport, git bool) *Client {
	if t == nil {
		u, _ := url.Parse(S(p, "url"))
		if u != nil && u.Scheme == "https" {
			t = NewTransport(nil)
			t.Mode = "https"
		} else {
			t = NewTransport(O(p, "gateway"))
		}
	}
	return &Client{p, t, git}
}
func (c *Client) Request(ctx context.Context, method, path string, body any) (*http.Response, error) {
	u, err := url.Parse(S(c.Profile, "url"))
	if err != nil {
		return nil, err
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("invalid service URL")
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost" || u.Hostname() == "::1")) {
		return nil, fmt.Errorf("HTTPS or authenticated loopback transport required")
	}
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(u.String(), "/")+path, r)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Otoha-CLI/0.1.0")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token := S(c.Profile, "token"); token != "" {
		prefix := "Bearer "
		if c.Git {
			prefix = "token "
		}
		req.Header.Set("Authorization", prefix+token)
	} else if c.Git {
		req.SetBasicAuth(S(c.Profile, "username"), S(c.Profile, "password"))
	}
	return c.Transport.HTTP.Do(req)
}
func (c *Client) Call(ctx context.Context, method, path string, body any) (Object, error) {
	resp, err := c.Request(ctx, method, path, body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var d Object
		json.Unmarshal(b, &d)
		code := S(d, "error")
		if code == "" {
			code = S(d, "message")
		}
		if code == "" {
			code = http.StatusText(resp.StatusCode)
		}
		return nil, &APIError{resp.StatusCode, code}
	}
	if len(b) == 0 {
		return Object{}, nil
	}
	var d Object
	if err = json.Unmarshal(b, &d); err != nil {
		var a []any
		if e := json.Unmarshal(b, &a); e != nil {
			return nil, err
		}
		return Object{"items": a}, nil
	}
	return d, nil
}
