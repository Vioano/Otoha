package otoha

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestTelegramConcurrentPollCannotOverwriteCompletedSendState(t *testing.T) {
	stateDir := t.TempDir()
	polling, release := make(chan struct{}), make(chan struct{})
	var pollOnce, releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch filepath.Base(r.URL.Path) {
		case "getMe":
			json.NewEncoder(w).Encode(Object{"ok": true, "result": Object{"id": 1, "is_bot": true}})
		case "getWebhookInfo":
			json.NewEncoder(w).Encode(Object{"ok": true, "result": Object{"url": ""}})
		case "sendMessage":
			json.NewEncoder(w).Encode(Object{"ok": true, "result": Object{"message_id": 20, "chat": Object{"id": 42}}})
		case "getUpdates":
			pollOnce.Do(func() { close(polling) })
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
			json.NewEncoder(w).Encode(Object{"ok": true, "result": []Object{{"update_id": 7, "message": Object{"message_id": 12, "chat": Object{"id": 42}, "from": Object{"id": 43}, "text": "OTOHA/1 {\"v\":1,\"uid\":\"parallel-inbound\"}\nreceived while sending"}}}})
		}
	}))
	defer server.Close()
	g, _ := NewTelegramGateway(TelegramConfig{Token: "test-token", APIBase: server.URL, StateDir: stateDir, MinInterval: -1})
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	if err := g.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	received := make(chan error, 1)
	go func() { _, err := g.ReceiveOnce(ctx, func(Inbound) error { return nil }); received <- err }()
	<-polling
	sent := make(chan error, 1)
	go func() {
		_, err := g.Send(ctx, Outbound{ID: "parallel-send", Recipient: "42", Text: "sent while polling"})
		sent <- err
	}()
	select {
	case err := <-sent:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		releaseOnce.Do(func() { close(release) })
		t.Fatal("send waited behind the long poll")
	}
	releaseOnce.Do(func() { close(release) })
	if err := <-received; err != nil {
		t.Fatal(err)
	}
	state, err := Load(filepath.Join(stateDir, "state.json"))
	if err != nil || len(Array(state["sent"])) != 1 || S(Items(state, "sent")[0], "status") != "sent" || len(Array(state["messages"])) != 1 || N(state, "offset") != 8 {
		t.Fatal("poll saved an old snapshot over completed send state", state, err)
	}
}

type telegramRoundTripFunc func(*http.Request) (*http.Response, error)

func (f telegramRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestTelegramTransportErrorsDoNotExposeBotToken(t *testing.T) {
	const token = "123456789:private-test-secret-token-abcdef"
	sentinel := errors.New("isolated network failure")
	g, _ := NewTelegramGateway(TelegramConfig{Token: token})
	g.client = &http.Client{Transport: telegramRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return nil, &url.Error{Op: "POST", URL: r.URL.String(), Err: fmt.Errorf("request %s: %w", r.URL.String(), sentinel)}
	})}
	err := g.Connect(context.Background())
	if err == nil || strings.Contains(err.Error(), token) || !errors.Is(err, sentinel) || !strings.Contains(err.Error(), "isolated network failure") {
		t.Fatal("network failure lost its cause or exposed the credential", err)
	}
}

func TestTelegramRecipientCannotFallBackFromTargetedToPublic(t *testing.T) {
	cfg := TelegramConfig{Recipients: map[string]string{"public": "public-chat", "alice/session": "private-chat", "alice/other": "private-chat", "bob/session": "other-private-chat"}}
	for _, targets := range [][]any{{"missing/session"}, {"alice/session", "missing/session"}, {"alice/session", "bob/session"}} {
		if chat, err := TelegramRecipient(cfg, Object{"payload": Object{"to_sessions": targets}}); err == nil || chat != "" {
			t.Fatal("targeted envelope used a public or incomplete route", targets, chat, err)
		}
	}
	if chat, err := TelegramRecipient(cfg, Object{"payload": Object{"to_sessions": []any{"alice/session", "alice/other"}}}); err != nil || chat != "private-chat" {
		t.Fatal(chat, err)
	}
	if chat, err := TelegramRecipient(cfg, Object{"payload": Object{}}); err != nil || chat != "public-chat" {
		t.Fatal(chat, err)
	}
}

func TestTelegramDefaultPreservesSubscriptionAndUnsupportedOriginal(t *testing.T) {
	stateDir := t.TempDir()
	var filters []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch filepath.Base(r.URL.Path) {
		case "getMe":
			json.NewEncoder(w).Encode(Object{"ok": true, "result": Object{"id": 1, "is_bot": true}})
		case "getWebhookInfo":
			json.NewEncoder(w).Encode(Object{"ok": true, "result": Object{"url": ""}})
		case "getUpdates":
			r.ParseForm()
			filters = append(filters, r.Form.Get("allowed_updates"))
			json.NewEncoder(w).Encode(Object{"ok": true, "result": []Object{{"update_id": 9, "callback_query": Object{"id": "inspect-this", "data": "unsupported-original"}}}})
		}
	}))
	defer server.Close()
	g, _ := NewTelegramGateway(TelegramConfig{Token: "test-token", APIBase: server.URL, StateDir: stateDir})
	called := false
	if _, err := g.ReceiveOnce(context.Background(), func(Inbound) error { called = true; return nil }); err == nil || !strings.Contains(err.Error(), "not a supported text message") {
		t.Fatal("unsupported update was silently consumed", err)
	}
	state, err := Load(filepath.Join(stateDir, "state.json"))
	if err != nil || N(state, "offset") != 0 || len(O(state, "seen_updates")) != 0 || S(O(O(O(state, "unsupported_updates"), "9"), "callback_query"), "data") != "unsupported-original" || called {
		t.Fatal("unsupported original or cursor was changed", state, err)
	}
	g, _ = NewTelegramGateway(TelegramConfig{Token: "test-token", APIBase: server.URL, StateDir: t.TempDir(), AllowedUpdates: []string{"message"}})
	g.ReceiveOnce(context.Background(), func(Inbound) error { return nil })
	if len(filters) != 2 || filters[0] != "" || filters[1] != `["message"]` {
		t.Fatal("default subscription changed or explicit filter was ignored", filters)
	}
}

func TestTelegramGatewayRetriesPersistedInboundAfterSinkFailureAndRestart(t *testing.T) {
	stateDir := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch filepath.Base(r.URL.Path) {
		case "getMe":
			json.NewEncoder(w).Encode(Object{"ok": true, "result": Object{"id": 1, "is_bot": true}})
		case "getWebhookInfo":
			json.NewEncoder(w).Encode(Object{"ok": true, "result": Object{"url": ""}})
		case "getUpdates":
			r.ParseForm()
			if r.Form.Get("offset") == "8" {
				json.NewEncoder(w).Encode(Object{"ok": true, "result": []Object{}})
				return
			}
			json.NewEncoder(w).Encode(Object{"ok": true, "result": []Object{{"update_id": 7, "message": Object{"message_id": 12, "chat": Object{"id": 42}, "from": Object{"id": 43}, "text": "OTOHA/1 {\"v\":1,\"uid\":\"stable-inbound\"}\nkeep this body"}}}})
		}
	}))
	defer server.Close()
	cfg := TelegramConfig{Token: "test-token", APIBase: server.URL, StateDir: stateDir}
	g, _ := NewTelegramGateway(cfg)
	failed := errors.New("isolated authority is offline")
	if _, err := g.ReceiveOnce(context.Background(), func(Inbound) error { return failed }); !errors.Is(err, failed) {
		t.Fatal("sink rejection was not reported", err)
	}
	state, err := Load(filepath.Join(stateDir, "state.json"))
	if err != nil || len(Array(state["messages"])) != 1 || N(state, "offset") != 0 {
		t.Fatal("original was lost or source cursor advanced after rejection", state, err)
	}
	// Recreate the gateway with no in-memory update or previous sink result.
	restarted, _ := NewTelegramGateway(cfg)
	var got []Inbound
	if _, err = restarted.ReceiveOnce(context.Background(), func(in Inbound) error { got = append(got, in); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].EnvelopeID != "stable-inbound" || got[0].Body != "keep this body" {
		t.Fatal("durable original was skipped instead of retried", got)
	}
	state, err = Load(filepath.Join(stateDir, "state.json"))
	if err != nil || len(Array(state["messages"])) != 1 || len(Array(state["pending"])) != 0 || N(state, "offset") != 8 {
		t.Fatal("pending delivery was not retired without duplicating the original", state, err)
	}
	got = nil
	if _, err = restarted.ReceiveOnce(context.Background(), func(in Inbound) error { got = append(got, in); return nil }); err != nil || len(got) != 0 {
		t.Fatal("accepted inbound was delivered twice", got, err)
	}
}

func TestTelegramGatewaySendReceivePersistsRoutesAndDeduplicates(t *testing.T) {
	stateDir := t.TempDir()
	var mu sync.Mutex
	var sent []Object
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := filepath.Base(r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch method {
		case "getMe":
			json.NewEncoder(w).Encode(Object{"ok": true, "result": Object{"id": 10, "is_bot": true, "username": "otoha_test_bot"}})
		case "getWebhookInfo":
			json.NewEncoder(w).Encode(Object{"ok": true, "result": Object{"url": ""}})
		case "sendMessage":
			_ = r.ParseForm()
			mu.Lock()
			sent = append(sent, Object{"chat_id": r.Form.Get("chat_id"), "text": r.Form.Get("text")})
			mu.Unlock()
			json.NewEncoder(w).Encode(Object{"ok": true, "result": Object{"message_id": 9, "chat": Object{"id": 8250132650}, "text": r.Form.Get("text")}})
		case "getUpdates":
			if r.URL.Query().Get("offset") == "8" {
				json.NewEncoder(w).Encode(Object{"ok": true, "result": []Object{}})
				return
			}
			json.NewEncoder(w).Encode(Object{"ok": true, "result": []Object{{
				"update_id": 7,
				"message":   Object{"message_id": 12, "chat": Object{"id": 8728299908}, "from": Object{"id": 8250132650, "username": "peer_bot", "is_bot": true}, "text": "OTOHA/1 {\"v\":1,\"uid\":\"u-1\"}\nhello"},
			}}})
		default:
			http.Error(w, "unknown method", http.StatusNotFound)
		}
	}))
	defer server.Close()

	g, err := NewTelegramGateway(TelegramConfig{Token: "test-token", APIBase: server.URL, StateDir: stateDir, Routes: map[string]string{"8728299908": "session-a"}})
	if err != nil {
		t.Fatal(err)
	}
	if result, err := g.Send(context.Background(), Outbound{ID: "u-1", Recipient: "@peer_bot", Text: "hello", Kind: "message"}); err != nil || result.ExternalID != 9 {
		t.Fatalf("send failed: %#v %v", result, err)
	}
	if result, err := g.Send(context.Background(), Outbound{ID: "u-1", Recipient: "@peer_bot", Text: "hello", Kind: "message"}); err != nil || result.ExternalID != 9 {
		t.Fatalf("idempotent resend failed: %#v %v", result, err)
	}
	mu.Lock()
	if len(sent) != 1 || sent[0]["chat_id"] != "@peer_bot" {
		t.Fatalf("unexpected send: %#v", sent)
	}
	mu.Unlock()
	state, err := Load(filepath.Join(stateDir, "state.json"))
	if err != nil || len(Array(state["sent"])) != 1 {
		t.Fatalf("outbound send was not persisted: %#v %v", state, err)
	}

	var got []Inbound
	progress, err := g.ReceiveOnce(context.Background(), func(in Inbound) error { got = append(got, in); return nil })
	if err != nil || !progress || len(got) != 1 {
		t.Fatalf("receive failed: progress=%v got=%#v err=%v", progress, got, err)
	}
	if got[0].Route != "session-a" || got[0].EnvelopeID != "u-1" || got[0].Body != "hello" {
		t.Fatalf("route/envelope decode failed: %#v", got[0])
	}
	state, err = Load(filepath.Join(stateDir, "state.json"))
	if err != nil || N(state, "offset") != 8 {
		t.Fatalf("state not persisted: %#v %v", state, err)
	}

	got = nil
	if _, err = g.ReceiveOnce(context.Background(), func(in Inbound) error { got = append(got, in); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("duplicate update was routed again: %#v", got)
	}
}

func TestTelegramGatewayRejectsWebhookAndOversizeText(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/bott/getMe" {
			json.NewEncoder(w).Encode(Object{"ok": true, "result": Object{"id": 1, "is_bot": true}})
			return
		}
		json.NewEncoder(w).Encode(Object{"ok": true, "result": Object{"url": "https://example.invalid/hook"}})
	}))
	defer server.Close()
	g, err := NewTelegramGateway(TelegramConfig{Token: "t", APIBase: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	if err = g.Connect(context.Background()); err == nil {
		t.Fatal("webhook conflict was accepted")
	}

	// Oversize is checked before any network send once the gateway is connected.
	server2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(Object{"ok": true, "result": Object{"id": 1, "is_bot": true}})
	}))
	defer server2.Close()
	g, _ = NewTelegramGateway(TelegramConfig{Token: "t", APIBase: server2.URL})
	if _, err = g.Send(context.Background(), Outbound{Recipient: "42", Text: string(make([]byte, 4097))}); err == nil {
		t.Fatal("oversize message was accepted")
	}
}
