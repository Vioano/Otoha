package otoha

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

// A saved HTTPS profile must never dial its legacy, unavailable SSH gateway.
func TestHTTPSDoesNotUseLegacySSHAfterReload(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("authorization lost")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer srv.Close()
	p := Object{"url": srv.URL, "token": "test-token", "gateway": Object{"host": "127.0.0.1", "port": 9, "key_file": "missing"}}
	path := filepath.Join(t.TempDir(), "identity.json")
	if err := Save(path, p); err != nil {
		t.Fatal(err)
	}
	saved, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	c := NewClient(saved, nil, false)
	defer c.Transport.Close()
	roots := x509.NewCertPool()
	roots.AddCert(srv.Certificate())
	c.Transport.HTTP.Transport.(*http.Transport).TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	if _, err = c.Call(context.Background(), "GET", "/health", nil); err != nil {
		t.Fatal(err)
	}
	if c.Transport.Mode != "https" || S(c.Transport.Gateway, "host") != "" {
		t.Fatal("HTTPS retained SSH transport")
	}
}

func TestHTTPSRejectsUntrustedCertificate(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{}`)) }))
	defer srv.Close()
	c := NewClient(Object{"url": srv.URL}, nil, false)
	defer c.Transport.Close()
	if _, err := c.Call(context.Background(), "GET", "/health", nil); err == nil {
		t.Fatal("untrusted TLS certificate accepted")
	}
}
