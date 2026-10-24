package otoha

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func HTTPSGateway(ctx context.Context, listen, cert, key, root, otohaUpstream, gitUpstream string, extraRoutes string) error {
	mux := http.NewServeMux()
	routes := map[string]string{"/otoha-api/": otohaUpstream, "/otoha-git/": gitUpstream}
	for _, entry := range strings.Split(extraRoutes, ",") {
		if entry == "" {
			continue
		}
		prefix, endpoint, ok := strings.Cut(entry, "=")
		if !ok || !strings.HasPrefix(prefix, "/") || !strings.HasSuffix(prefix, "/") || strings.Contains(prefix, "..") || prefix == "/" {
			return fmt.Errorf("invalid additional route")
		}
		if _, exists := routes[prefix]; exists {
			return fmt.Errorf("route already reserved")
		}
		routes[prefix] = endpoint
	}
	for prefix, endpoint := range routes {
		u, e := url.Parse(endpoint)
		if e != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" {
			return fmt.Errorf("loopback upstream required")
		}
		p := httputil.NewSingleHostReverseProxy(u)
		old := p.Director
		base := prefix
		p.Director = func(r *http.Request) {
			old(r)
			r.URL.Path = "/" + strings.TrimPrefix(r.URL.Path, base)
			r.URL.RawPath = ""
			r.Header.Set("X-Forwarded-Proto", "https")
			r.Header.Set("X-Forwarded-Prefix", strings.TrimRight(base, "/"))
			r.Header.Set("X-Forwarded-Host", r.Host)
		}
		p.Transport = &http.Transport{Proxy: nil, MaxIdleConns: 64, MaxIdleConnsPerHost: 32, IdleConnTimeout: 90 * time.Second, ResponseHeaderTimeout: 55 * time.Second}
		p.FlushInterval = -1
		mux.Handle(prefix, p)
	}
	files := http.FileServer(http.Dir(root))
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(filepath.Clean("/"+r.URL.Path), "/")))
		st, e := os.Stat(path)
		if e == nil && st.IsDir() {
			http.Error(w, "directory listing disabled", 403)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		files.ServeHTTP(w, r)
	}))
	srv := &http.Server{Addr: listen, Handler: mux, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 90 * time.Second, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12}}
	go func() { <-ctx.Done(); srv.Close() }()
	return srv.ListenAndServeTLS(cert, key)
}
