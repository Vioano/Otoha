package otoha

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type relayFixtureRoundTrip func(*http.Request) (*http.Response, error)

func (f relayFixtureRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestRelayDisconnectedProxyKeepsOriginalQueuedMessageRecoverable(t *testing.T) {
	t.Setenv("OTOHA_HOME", t.TempDir())
	client := NewClient(Object{"url": DefaultRelayEndpoint + "/t/r0123456789abcdef01234567", "token": "fixture-member"}, nil, false)
	defer client.Transport.Close()
	online, posts := false, 0
	client.Transport.HTTP.Transport = relayFixtureRoundTrip(func(r *http.Request) (*http.Response, error) {
		status, body := 404, "<html><body>no route</body></html>"
		if online {
			body = `{"error":"message_not_found"}`
			if r.Method == http.MethodPost {
				posts++
				data, _ := io.ReadAll(r.Body)
				if !strings.Contains(string(data), `"client_id":"same-original-request"`) {
					t.Fatal("recovery replaced the original message identity", string(data))
				}
				status, body = 200, `{"message_id":17,"duplicate":false}`
			}
		}
		return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	m := newPageManager(Home())
	op := Object{"request_id": "same-original-request", "state": "queued", "body": Object{"client_id": "same-original-request", "body": "the complete original"}}
	if err := Save(m.pendingPath("fixture", "same-original-request"), op); err != nil {
		t.Fatal(err)
	}
	err := m.submitOperation(context.Background(), "fixture", client, op)
	var api *APIError
	if !errors.As(err, &api) || api.Status != 503 || api.Code != "relay_center_unavailable" || pagePermanentError(err) || posts != 0 {
		t.Fatal("disconnected relay became a permanent message rejection or caused a POST", err, posts)
	}
	if saved, err := Load(m.pendingPath("fixture", "same-original-request")); err != nil || S(saved, "state") != "queued" {
		t.Fatal("disconnected relay discarded the original queued message", saved, err)
	}
	online = true
	if err := m.submitOperation(context.Background(), "fixture", client, op); err != nil || posts != 1 {
		t.Fatal("same original did not recover after the authority returned", err, posts)
	}
	if receipt, err := Load(m.receiptPath("fixture", "same-original-request")); err != nil || N(receipt, "message_id") != 17 {
		t.Fatal("recovery lacked an accepted authority receipt", receipt, err)
	}
}

func TestRelayDoesNotReclassifyBusinessOrOtherEndpoint404(t *testing.T) {
	for _, fixture := range []struct{ endpoint, body string }{
		{DefaultRelayEndpoint + "/t/r0123456789abcdef01234567", `{"error":"project_forbidden"}`},
		{DefaultRelayEndpoint, "<html>not found</html>"},
		{"https://team.example.com/t/r0123456789abcdef01234567", "<html>not found</html>"},
	} {
		client := NewClient(Object{"url": fixture.endpoint}, nil, false)
		client.Transport.HTTP.Transport = relayFixtureRoundTrip(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 404, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(fixture.body))}, nil
		})
		_, err := client.Call(context.Background(), http.MethodGet, "/v1/manifest", nil)
		client.Transport.Close()
		if !IsStatus(err, 404) || !pagePermanentError(err) {
			t.Fatal("business error or unrelated endpoint was silently reclassified", fixture, err)
		}
	}
}

func TestDefaultRelayFallbackIsLimitedToItsHTTPS443Authority(t *testing.T) {
	for _, scenario := range []struct {
		url  string
		git  bool
		want bool
	}{
		{"https://otoha.kessokuu.top", false, true},
		{"https://otoha.kessokuu.top:443/t/example", false, true},
		{"https://OTOHA.KESSOKUU.TOP/t/example", false, true},
		{"https://team.example.com/t/example", false, false},
		{"https://otoha.kessokuu.top:8443/t/example", false, false},
		{"http://otoha.kessokuu.top/t/example", false, false},
		{"https://52.76.76.10/t/example", false, false},
		{"https://otoha.kessokuu.top.other.example/t/example", false, false},
		{"https://otoha.kessokuu.top/t/example", true, false},
	} {
		profile := Object{"url": scenario.url, "token": "synthetic-owner-token"}
		client := NewClient(profile, nil, scenario.git)
		transport := client.Transport.HTTP.Transport.(*http.Transport)
		got := transport.DialTLSContext != nil
		client.Transport.Close()
		if got != scenario.want {
			t.Fatalf("URL %q git=%v got fallback=%v", scenario.url, scenario.git, got)
		}
		if S(profile, "url") != scenario.url || S(profile, "token") != "synthetic-owner-token" {
			t.Fatal("fallback rewrote the public endpoint or personal credential")
		}
	}
}

func TestDefaultRelayInvalidDeploymentIPDoesNotInstallFallback(t *testing.T) {
	previous := DefaultRelayDialIP
	DefaultRelayDialIP = "not-an-ip"
	t.Cleanup(func() { DefaultRelayDialIP = previous })
	client := NewClient(Object{"url": DefaultRelayEndpoint}, nil, false)
	defer client.Transport.Close()
	if client.Transport.HTTP.Transport.(*http.Transport).DialTLSContext != nil {
		t.Fatal("invalid deployment address became a network fallback")
	}
}
