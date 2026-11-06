package otoha

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
)

func joinEndpoint(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawPath != "" || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("--endpoint must be an HTTPS origin or service base URL, such as https://team.example.com/t/TEAM")
	}
	u.Host = strings.ToLower(u.Host)
	if strings.Contains(u.Path, "//") {
		return "", errors.New("--endpoint has an invalid repeated slash in its service base path")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	if u.Path != "" && (path.Clean(u.Path) != u.Path || strings.ContainsAny(u.Path, "\\\x00\r\n")) {
		return "", errors.New("--endpoint has an invalid service base path")
	}
	u.RawPath = ""
	return u.String(), nil
}

// Resolve a short-lived key once, before enrolling. Retaining this private
// envelope lets the existing enrollment recovery use the same credential and
// enrollment ID after a lost response, even when the key has already been used.
func resolveJoinKey(ctx context.Context, endpoint, key, caFile string) (string, error) {
	endpoint, err := joinEndpoint(endpoint)
	if err != nil {
		return "", err
	}
	if key == "" || len(key) > 1024 || strings.TrimSpace(key) != key || strings.ContainsAny(key, "\x00\r\n") {
		return "", errors.New("--key requires a nonempty invitation key")
	}
	sum := sha256.Sum256([]byte(endpoint + "\x00" + key))
	path := filepath.Join(Home(), "invitations", hex.EncodeToString(sum[:])+".json")
	if cached, loadErr := Load(path); loadErr == nil {
		if S(cached, "invitation") != key || S(cached, "url") != endpoint || S(cached, "protocol") != "otoha-collaboration/1" {
			return "", errors.New("cached invitation does not match this endpoint and key; original state preserved")
		}
		return path, nil
	} else if !os.IsNotExist(loadErr) {
		return "", fmt.Errorf("read private invitation cache: %w", loadErr)
	}
	profile := Object{"url": endpoint}
	if caFile != "" {
		ca, readErr := os.ReadFile(caFile)
		if readErr != nil {
			return "", fmt.Errorf("read CA file: %w", readErr)
		}
		profile["ca_pem"] = string(ca)
	}
	client := NewClient(profile, nil, false)
	defer client.Transport.Close()
	if err = ConfigureClientTLS(client); err != nil {
		return "", err
	}
	// An invitation must never be redirected: the key belongs to the exact
	// HTTPS endpoint supplied by the person joining.
	client.Transport.HTTP.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	inv, err := client.Call(ctx, http.MethodPost, "/v1/invitation-info", Object{"invitation": key})
	if err != nil {
		return "", fmt.Errorf("resolve invitation: %w", err)
	}
	declaredEndpoint, endpointErr := joinEndpoint(S(inv, "url"))
	if S(inv, "protocol") != "otoha-collaboration/1" || endpointErr != nil || declaredEndpoint != endpoint {
		return "", errors.New("invitation authority differs from --endpoint; no identity was created")
	}
	for _, field := range []string{"actor", "project", "channel"} {
		if err = Name(S(inv, field)); err != nil {
			return "", fmt.Errorf("invalid invitation %s", field)
		}
	}
	role := S(O(inv, "grants"), S(inv, "project"))
	if role != "reader" && role != "member" && role != "admin" {
		return "", errors.New("invitation has no valid role for its project")
	}
	// Only allowlisted enrollment metadata enters the private envelope. In
	// particular, a response cannot inject a different CA or local route.
	envelope := Object{"protocol": "otoha-collaboration/1", "url": endpoint, "actor": inv["actor"], "project": inv["project"], "channel": inv["channel"], "grants": inv["grants"], "expires": inv["expires"], "invitation": key}
	if ca := S(profile, "ca_pem"); ca != "" {
		envelope["ca_pem"] = ca
	}
	if err = Save(path, envelope); err != nil {
		return "", fmt.Errorf("save private invitation: %w", err)
	}
	return path, nil
}
