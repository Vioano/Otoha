package otoha

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

type fakeChannel struct {
	mu       sync.Mutex
	sent     []Outbound
	received []Inbound
}

func (f *fakeChannel) Name() string { return "fake" }
func (f *fakeChannel) Capabilities() Capabilities {
	return Capabilities{DirectToMember: true, PrivateChat: true, Broadcast: true, MaxTextBytes: 4096}
}
func (f *fakeChannel) Connect(context.Context) error { return nil }
func (f *fakeChannel) Close() error                  { return nil }
func (f *fakeChannel) Send(_ context.Context, out Outbound) (SendResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, out)
	return SendResult{ID: out.ID, ExternalID: int64(len(f.sent)), ChatID: out.Recipient}, nil
}
func (f *fakeChannel) Receive(ctx context.Context, sink func(Inbound) error) error {
	for {
		f.mu.Lock()
		if len(f.received) > 0 {
			inbound := f.received[0]
			f.received = f.received[1:]
			f.mu.Unlock()
			if err := sink(inbound); err != nil {
				return err
			}
			continue
		}
		f.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func TestChannelBridgeFakeChannelRoundTripWithServerAndInbox(t *testing.T) {
	t.Setenv("OTOHA_HOME", t.TempDir())
	var eventCalls int
	var receivedReceipts int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/events":
			after := r.URL.Query().Get("after")
			if after == "0" && eventCalls == 0 {
				eventCalls++
				json.NewEncoder(w).Encode(Object{"events": []Object{{"id": 1, "kind": "message.created", "payload": Object{"id": 7, "channel": "general", "actor": "bob", "session": "bob-session", "payload": Object{"uid": "server-1", "text": "server to Telegram", "body": "server to Telegram", "summary": "server to Telegram", "kind": "message"}}}}, "cursor": 1, "has_more": false})
				return
			}
			json.NewEncoder(w).Encode(Object{"events": []Object{{"id": 2, "kind": "message.created", "payload": Object{"id": 8, "channel": "general", "actor": "alice", "session": "alice-session", "payload": Object{"uid": "transport-in-1", "text": "Telegram to server", "body": "Telegram to server", "summary": "Telegram to server", "kind": "message"}}}}, "cursor": 2, "has_more": false})
		case "/v1/messages":
			json.NewEncoder(w).Encode(Object{"message_id": 8, "uid": "transport-in-1", "event_id": 2, "duplicate": false})
		case "/v1/receipts":
			receivedReceipts++
			json.NewEncoder(w).Encode(Object{"message_id": 8, "received": 1, "read": nil})
		default:
			if r.Method == http.MethodPost && r.URL.Path == "/v1/sessions" {
				json.NewEncoder(w).Encode(Object{"id": "alice-session", "actor": "alice"})
				return
			}
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := NewClient(Object{"url": server.URL, "token": "token", "actor": "alice"}, nil, false)
	defer client.Transport.Close()
	fake := &fakeChannel{}
	binding := Object{"actor": "alice", "session": "alice-session", "channel": "general"}
	bridge, err := NewChannelBridge(fake, client, binding, func(Object) string { return "peer-chat" })
	if err != nil {
		t.Fatal(err)
	}
	forwarded, err := bridge.ForwardServerEvents(context.Background(), 0)
	if err != nil || N(forwarded, "sent") != 1 {
		t.Fatalf("server-to-channel forwarding failed: %#v %v", forwarded, err)
	}
	if len(fake.sent) != 1 || fake.sent[0].ID != "server-1" || fake.sent[0].Recipient != "peer-chat" {
		t.Fatalf("unexpected fake channel send: %#v", fake.sent)
	}

	result, err := bridge.IngestInbound(context.Background(), Inbound{EnvelopeID: "transport-in-1", Body: "Telegram to server", SenderUsername: "peer_bot", Route: "alice-session"})
	if err != nil || N(O(result, "server"), "message_id") != 8 || N(O(result, "inbox"), "stored") != 1 {
		t.Fatalf("channel-to-server/inbox failed: %#v %v", result, err)
	}
	items, err := ListInbox("general")
	if err != nil || len(items) != 1 || S(items[0], "uid") != "transport-in-1" || !B(items[0], "received") {
		t.Fatalf("inbox did not persist bridge message: %#v %v", items, err)
	}
	if receivedReceipts != 1 {
		t.Fatalf("expected one application receipt, got %d", receivedReceipts)
	}
	if _, err := os.Stat(filepath.Join(Home(), "transport-cursors", "fake-alice-session.json")); err != nil {
		t.Fatalf("transport cursor not persisted: %v", err)
	}
}
