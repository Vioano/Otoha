package otoha

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestMessageJoinedHomeUsesSharedLocalAPIQueueAndReceipt(t *testing.T) {
	t.Setenv("OTOHA_HOME", t.TempDir())
	var mu sync.Mutex
	var accepted Object
	var posts atomic.Int32
	authority := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer original-test-credential" {
			t.Error("message did not use the joined project identity")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if r.Method == http.MethodGet {
			if accepted == nil {
				w.WriteHeader(http.StatusNotFound)
				json.NewEncoder(w).Encode(Object{"error": "message_not_found"})
			} else {
				json.NewEncoder(w).Encode(Object{"message": accepted})
			}
			return
		}
		var body Object
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		posts.Add(1)
		accepted = pageTestMessage(body, 31)
		O(accepted, "payload")["summary"] = body["summary"]
		json.NewEncoder(w).Encode(Object{"message_id": 31, "duplicate": false})
	}))
	defer authority.Close()
	team := pageTestTeam(t, authority.URL)
	identityPath := filepath.Join(Home(), "fixture-identity.json")
	identityBefore, _ := os.ReadFile(identityPath)
	if _, err := os.Stat(filepath.Join(Home(), "config.json")); !os.IsNotExist(err) {
		t.Fatal("test must start without legacy config")
	}
	m := newPageManager(Home())
	m.token = "local-message-test-token"
	local := httptest.NewServer(http.HandlerFunc(m.serveHTTP))
	defer local.Close()
	m.baseURL = local.URL
	call := func(method, path string, body Object) Object {
		t.Helper()
		var data []byte
		if body != nil {
			data, _ = json.Marshal(body)
		}
		req, _ := http.NewRequest(method, local.URL+path, bytes.NewReader(data))
		req.Header.Set("Authorization", "Bearer "+m.token)
		req.Header.Set("Content-Type", "application/json")
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var result Object
		if err = json.NewDecoder(response.Body).Decode(&result); err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != http.StatusOK {
			t.Fatalf("%s %s: %d %v", method, path, response.StatusCode, result)
		}
		return result
	}
	input := Object{"team": team, "text": "Ready for review\nFull context remains here.", "request_id": "4b20e012-7290-4c99-8877-10f8d8f55a11"}
	queued := call(http.MethodPost, "/api/messages", input)
	if S(queued, "state") != "queued" || posts.Load() != 0 {
		t.Fatal("local queue was confused with authority acceptance", queued)
	}
	before := call(http.MethodGet, "/api/messages?team="+team, nil)
	if len(Array(before["pending"])) != 1 || len(Array(before["messages"])) != 0 {
		t.Fatal(before)
	}
	// Repeating the caller's original request cannot add a second queue item.
	call(http.MethodPost, "/api/messages", input)
	paths, _ := filepath.Glob(filepath.Join(Home(), "pages", "outbox", team, "*.json"))
	if len(paths) != 1 {
		t.Fatal("same ID created multiple pending messages")
	}
	// Recreate the runtime and flush using only its durable outbox.
	restarted := newPageManager(Home())
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan struct{})
	go func() { restarted.flushTeam(ctx, team, make(chan struct{})); close(finished) }()
	defer func() { cancel(); <-finished }()
	deadline := time.Now().Add(3 * time.Second)
	for {
		op, _ := restarted.operation(team, S(input, "request_id"))
		if S(op, "state") == "accepted" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("restarted shared queue did not flush", op)
		}
		time.Sleep(5 * time.Millisecond)
	}
	mu.Lock()
	message := Clone(accepted)
	mu.Unlock()
	events := Object{"events": []any{Object{"id": 1, "kind": "message.created", "payload": message}}, "cursor": 1, "has_more": false}
	raw, _ := json.Marshal(events)
	json.Unmarshal(raw, &events)
	if err := m.acceptEvents(team, team+"-general", events); err != nil {
		t.Fatal(err)
	}
	after := call(http.MethodGet, "/api/messages?team="+team, nil)
	rows := Items(after, "messages")
	if len(rows) != 1 || S(rows[0], "summary") != "Ready for review" || N(rows[0], "message_id") != 31 || posts.Load() != 1 || len(Array(after["pending"])) != 0 {
		t.Fatal(after, posts.Load())
	}
	if _, exists := rows[0]["text"]; exists {
		t.Fatal("default list exposed full message text", rows[0])
	}
	if _, exists := rows[0]["body"]; exists {
		t.Fatal("default list exposed full message body", rows[0])
	}
	if S(rows[0], "get_command") != "otoha message get 31 --team "+team || N(O(rows[0], "ref"), "message_id") != 31 {
		t.Fatal("summary did not expose an actionable stable reference", rows[0])
	}
	if _, exists := after["read"]; exists {
		t.Fatal("listing a cache must not manufacture a read receipt")
	}
	identityAfter, _ := os.ReadFile(identityPath)
	if !bytes.Equal(identityBefore, identityAfter) {
		t.Fatal("message flow modified original project credentials")
	}
	if _, err := os.Stat(filepath.Join(Home(), "config.json")); !os.IsNotExist(err) {
		t.Fatal("message flow introduced legacy configuration")
	}
}

func TestMessageCacheIsBoundedAndExcludesPageArtifacts(t *testing.T) {
	state := Object{}
	page := Object{"id": 1, "actor": "other", "client_id": "page", "payload": Object{"body": `{"schema":"otoha-page/1","operation":"publish","document":{"format":"html","html":"<h1>Work</h1>"}}`}}
	applyChatMessage(state, page)
	if len(Array(state["messages"])) != 0 {
		t.Fatal("page document was mixed into the ordinary chat cache")
	}
	for i := 2; i <= 103; i++ {
		applyChatMessage(state, Object{"id": i, "actor": "teammate", "client_id": fmt.Sprint(i), "payload": Object{"text": fmt.Sprintf("message %d", i), "body": fmt.Sprintf("message %d", i), "source": "agent", "kind": "message"}})
	}
	if len(Array(state["messages"])) != 100 || !B(state, "messages_truncated") {
		t.Fatal(state)
	}
	last := Object{"id": 103, "actor": "teammate", "client_id": "103", "payload": Object{"body": "message 103"}}
	applyChatMessage(state, last)
	if len(Array(state["messages"])) != 100 {
		t.Fatal("duplicate accepted message changed cache size")
	}
	first := messageObject(Array(state["messages"])[0])
	if N(first, "id") != 4 {
		t.Fatal("bounded cache kept an unexpected source window", first)
	}
}

func TestMessageValidatesWireSizeAndImmutableRequestID(t *testing.T) {
	t.Setenv("OTOHA_HOME", t.TempDir())
	team := pageTestTeam(t, "http://127.0.0.1:1")
	m := newPageManager(Home())
	for _, text := range []string{"", " \n\t", strings.Repeat("<", 10000)} {
		if _, err := m.enqueueMessage(team, Object{"text": text}); err == nil {
			t.Fatal("invalid or wire-oversized text was queued")
		}
	}
	text := strings.Repeat("中", 300) + "\nfull body"
	result, err := m.enqueueMessage(team, Object{"text": text, "request_id": "9-uppercase-ID"})
	if err != nil || S(result, "state") != "queued" {
		t.Fatal(result, err)
	}
	op, _ := Load(m.pendingPath(team, "9-uppercase-ID"))
	if len([]rune(S(O(op, "body"), "summary"))) != 280 || S(O(op, "body"), "body") != text {
		t.Fatal("summary changed the full body", op)
	}
	if _, err := m.enqueueMessage(team, Object{"text": "different", "request_id": "9-uppercase-ID"}); err == nil {
		t.Fatal("same request ID was allowed to change message")
	}
	if _, err := m.enqueueMessage(team, Object{"text": strings.Repeat("😀", 280), "request_id": "emoji-summary"}); err != nil {
		t.Fatal(err)
	}
	emoji, _ := Load(m.pendingPath(team, "emoji-summary"))
	if summary := S(O(emoji, "body"), "summary"); len(summary) > 1000 || len([]rune(summary)) != 250 {
		t.Fatal("summary exceeded the authority UTF-8 byte limit")
	}
}

func TestMessageExplicitSummaryPreservesBodyAndRequestIdentity(t *testing.T) {
	t.Setenv("OTOHA_HOME", t.TempDir())
	team := pageTestTeam(t, "http://127.0.0.1:1")
	m := newPageManager(Home())
	input := Object{"text": "Original full body\n第二段 👋", "summary": "Map v3 ready", "request_id": "explicit-summary"}
	if _, err := m.enqueueMessage(team, input); err != nil {
		t.Fatal(err)
	}
	op, err := Load(m.pendingPath(team, "explicit-summary"))
	if err != nil || S(O(op, "body"), "body") != S(input, "text") || S(O(op, "body"), "summary") != S(input, "summary") {
		t.Fatal("explicit summary changed or replaced the original body", op, err)
	}
	if _, err := m.enqueueMessage(team, input); err != nil {
		t.Fatal("same summary/body did not preserve request identity", err)
	}
	changed := Clone(input)
	changed["summary"] = "Different notification"
	if _, err := m.enqueueMessage(team, changed); err == nil {
		t.Fatal("same request ID was allowed to change its summary")
	}
	for _, summary := range []any{"", " \n", strings.Repeat("中", 334), string([]byte{0xff}), 12} {
		if _, err := m.enqueueMessage(team, Object{"text": "body", "summary": summary}); err == nil {
			t.Fatal("invalid explicit summary was silently accepted")
		}
	}
}

func TestMessageGetFetchesOriginalOutsideCacheAndRechecksAuthority(t *testing.T) {
	t.Setenv("OTOHA_HOME", t.TempDir())
	const original = "Full original\n中文 😀\nDo not auto-adopt this message."
	var forbidden atomic.Bool
	var getCalls atomic.Int32
	authority := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/messages/by-id" || r.URL.Query().Get("id") != "1" || r.URL.Query().Get("channel") != "page-fixture-general" || r.Header.Get("Authorization") != "Bearer original-test-credential" {
			t.Error("get did not use the scoped read-only authority lookup", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		getCalls.Add(1)
		if forbidden.Load() {
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(Object{"error": "channel_forbidden"})
			return
		}
		json.NewEncoder(w).Encode(Object{"message_id": 1, "message": Object{"id": 1, "channel": "page-fixture-general", "actor": "teammate", "payload": Object{"summary": "Short notification", "body": original, "text": original, "uid": "original-uid"}}})
	}))
	defer authority.Close()
	team := pageTestTeam(t, authority.URL)
	m := newPageManager(Home())
	m.token = "local-lookup-test-token"
	if err := m.changeState(team, func(state Object) error {
		state["connection"] = "online"
		for i := 1; i <= 102; i++ {
			applyChatMessage(state, Object{"id": i, "actor": "teammate", "payload": Object{"body": original, "summary": "Short notification"}})
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	snapshot, _ := m.messageSnapshot(team)
	if len(Array(snapshot["messages"])) != 100 || N(messageObject(Array(snapshot["messages"])[0]), "message_id") != 3 || !B(snapshot, "messages_truncated") {
		t.Fatal("test failed to exclude the original ID from the bounded cache", snapshot)
	}
	local := httptest.NewServer(http.HandlerFunc(m.serveHTTP))
	defer local.Close()
	m.baseURL = local.URL
	lookup := func(status int) Object {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, local.URL+"/api/messages/by-id?team="+team+"&id=1", nil)
		req.Header.Set("Authorization", "Bearer "+m.token)
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var result Object
		if err = json.NewDecoder(response.Body).Decode(&result); err != nil || response.StatusCode != status {
			t.Fatalf("lookup: HTTP %d expected %d: %v %v", response.StatusCode, status, result, err)
		}
		return result
	}
	result := lookup(http.StatusOK)
	if S(O(O(result, "message"), "payload"), "body") != original || S(result, "source") != "authority" || result["read_receipt_recorded"] != false {
		t.Fatal("get lost original contents or manufactured a read receipt", result)
	}
	if getCalls.Load() != 1 {
		t.Fatal("get was not read from the authority")
	}
	if err := Save(filepath.Join(Home(), "page-runtime.json"), Object{"url": local.URL, "token": m.token}); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"get", "1", "--team", team}, {"get", "--team", team, "1"}} {
		got, err := MessageCommand(context.Background(), args)
		if err != nil || S(O(O(got, "message"), "payload"), "body") != original {
			t.Fatal("documented get command did not retrieve the original", args, got, err)
		}
	}
	if err := m.changeState(team, func(state Object) error {
		state["messages"] = []any{Object{"id": 1, "message_id": 1, "text": original, "summary": "Short notification"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	forbidden.Store(true)
	lookup(http.StatusForbidden)
	// Even a message present in the cache cannot bypass a revoked authority.
	if _, err := m.messageByID(context.Background(), team, "1"); !IsStatus(err, http.StatusForbidden) {
		t.Fatal("get returned cached content after access was revoked", err)
	}
	forbidden.Store(false)
	authority.Close()
	lookup(http.StatusServiceUnavailable)
}

func TestMessageGetRejectsInvalidIDsBeforeStartingDaemon(t *testing.T) {
	t.Setenv("OTOHA_HOME", t.TempDir())
	for _, args := range [][]string{{"get"}, {"get", "0", "--team", "unused"}, {"get", "-1"}, {"get", "01"}, {"get", "1.5"}, {"get", "9223372036854775808"}, {"get", "--team", "unused", "bad"}, {"get", "1", "--team", "unused", "extra"}} {
		if _, err := MessageCommand(context.Background(), args); err == nil {
			t.Fatal("invalid get arguments were accepted", args)
		}
	}
	if _, err := os.Stat(filepath.Join(Home(), "page-runtime.json")); !os.IsNotExist(err) {
		t.Fatal("invalid get arguments started a daemon")
	}
}

func TestMessageListKeepsSummaryBoundaryWithOlderDaemon(t *testing.T) {
	t.Setenv("OTOHA_HOME", t.TempDir())
	team := pageTestTeam(t, "http://127.0.0.1:1")
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer legacy-local-test-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/teams":
			json.NewEncoder(w).Encode(Object{"teams": []any{Object{"name": team}}})
		case "/api/state":
			json.NewEncoder(w).Encode(Object{"team": team, "connection": "online"})
		case "/api/messages":
			json.NewEncoder(w).Encode(Object{"team": team, "messages": []any{Object{"message_id": 42, "uid": "original-id", "summary": "Short summary", "text": "Long original body not intended for notification", "body": "Long original body not intended for notification", "unknown_field": "must not leak"}}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer local.Close()
	if err := Save(filepath.Join(Home(), "page-runtime.json"), Object{"url": local.URL, "token": "legacy-local-test-token"}); err != nil {
		t.Fatal(err)
	}
	result, err := MessageCommand(context.Background(), []string{"list", "--team", team})
	if err != nil || S(result, "content") != "summaries" || len(Array(result["messages"])) != 1 {
		t.Fatal(result, err)
	}
	message := messageObject(Array(result["messages"])[0])
	for _, key := range []string{"text", "body", "unknown_field"} {
		if _, exists := message[key]; exists {
			t.Fatal("legacy daemon content entered the summary list", key)
		}
	}
	if S(message, "summary") != "Short summary" || S(message, "get_command") != "otoha message get 42 --team "+team {
		t.Fatal(message)
	}
}
