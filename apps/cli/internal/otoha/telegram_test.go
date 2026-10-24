package otoha

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
)

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
