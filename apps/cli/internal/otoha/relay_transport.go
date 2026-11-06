package otoha

import (
	"context"
	"crypto/tls"
	"encoding/hex"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"
)

const DefaultRelayEndpoint = "https://otoha.kessokuu.top"

// This address belongs to the product relay and was deployment-verified. It is
// never applied to an arbitrary user endpoint or written into its public URL.
var DefaultRelayDialIP = "52.76.76.10"

// frps returns an unstructured 404 while a team's proxy is disconnected.
// That is loss of transport, not a durable rejection by the team authority.
// Structured authority errors and every other endpoint retain their status.
func defaultRelayTeamUnavailable(profile Object, status int, response Object) bool {
	if status != http.StatusNotFound || S(response, "error") != "" || S(response, "message") != "" {
		return false
	}
	u, err := url.Parse(S(profile, "url"))
	if err != nil || u.Scheme != "https" || !strings.EqualFold(u.Hostname(), "otoha.kessokuu.top") || (u.Port() != "" && u.Port() != "443") {
		return false
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 2 || parts[0] != "t" || len(parts[1]) != 25 || parts[1][0] != 'r' {
		return false
	}
	_, err = hex.DecodeString(parts[1][1:])
	return err == nil
}

func configureDefaultRelayTransport(client *Client) {
	u, err := url.Parse(S(client.Profile, "url"))
	if err != nil || u.Scheme != "https" || !strings.EqualFold(u.Hostname(), "otoha.kessokuu.top") || (u.Port() != "" && u.Port() != "443") || net.ParseIP(DefaultRelayDialIP) == nil {
		return
	}
	transport, ok := client.Transport.HTTP.Transport.(*http.Transport)
	if !ok {
		return
	}
	var usePinned atomic.Bool
	transport.DialTLSContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		config := &tls.Config{MinVersion: tls.VersionTLS12}
		if transport.TLSClientConfig != nil {
			config = transport.TLSClientConfig.Clone()
		}
		config.ServerName = u.Hostname()
		config.InsecureSkipVerify = false
		dialer := tls.Dialer{NetDialer: &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 20 * time.Second}, Config: config}
		if !usePinned.Load() {
			first, cancel := context.WithTimeout(ctx, 3*time.Second)
			conn, firstErr := dialer.DialContext(first, network, address)
			cancel()
			if firstErr == nil {
				return conn, nil
			}
		}
		// Fallback occurs during TLS setup, before any HTTP request is sent.
		// It therefore does not replay an unknown POST or change its identity.
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(DefaultRelayDialIP, "443"))
		if err == nil {
			usePinned.Store(true)
		}
		return conn, err
	}
}
