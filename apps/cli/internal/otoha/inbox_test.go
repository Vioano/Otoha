package otoha

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestInboxPersistsDeduplicatesAndRecordsReadReceipt(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OTOHA_HOME", home)
	receipts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/sessions":
			json.NewEncoder(w).Encode(Object{"id": "alice-session", "actor": "alice"})
		case "/v1/events":
			if r.URL.Query().Get("after") == "0" {
				json.NewEncoder(w).Encode(Object{"events": []Object{{"id": 1, "kind": "message.created", "payload": Object{"id": 7, "channel": "general", "actor": "bob", "session": "bob-session", "payload": Object{"uid": "u-1", "text": "hello", "summary": "hello", "body": "hello"}}}}, "cursor": 1, "has_more": false})
			} else {
				json.NewEncoder(w).Encode(Object{"events": []Object{}, "cursor": 1, "has_more": false})
			}
		case "/v1/receipts":
			receipts++
			json.NewEncoder(w).Encode(Object{"message_id": 7, "received": 1, "read": 1})
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer server.Close()
	c := NewClient(Object{"url": server.URL, "token": "token", "actor": "alice"}, nil, false)
	defer c.Transport.Close()
	binding := Object{"actor": "alice", "session": "alice-session", "channel": "general"}
	first, err := InboxCheck(context.Background(), c, binding, 0)
	if err != nil || N(first, "stored") != 1 {
		t.Fatalf("first inbox check failed: %#v %v", first, err)
	}
	second, err := InboxCheck(context.Background(), c, binding, 0)
	if err != nil || N(second, "stored") != 0 {
		t.Fatalf("duplicate inbox check failed: %#v %v", second, err)
	}
	items, err := ListInbox("general")
	if err != nil || len(items) != 1 || !B(items[0], "received") || B(items[0], "read") {
		t.Fatalf("local inbox state incorrect: %#v %v", items, err)
	}
	message, err := InboxRead(context.Background(), c, binding, "u-1")
	if err != nil || N(message, "id") != 7 {
		t.Fatalf("inbox read failed: %#v %v", message, err)
	}
	items, err = ListInbox("general")
	if err != nil || len(items) != 1 || !B(items[0], "read") || receipts != 2 {
		t.Fatalf("read receipt or local state incorrect: %#v receipts=%d err=%v", items, receipts, err)
	}
	if _, err = os.Stat(filepath.Join(home, "inbox", "u-1.json")); err != nil {
		t.Fatalf("message was not durably stored: %v", err)
	}
}
