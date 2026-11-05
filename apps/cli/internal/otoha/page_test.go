package otoha

import (
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

func pageTestTeam(t *testing.T, endpoint string) string {
	t.Helper()
	team := "page-fixture"
	profile := filepath.Join(Home(), "fixture-identity.json")
	if err := Save(profile, Object{"url": endpoint, "actor": "fixture", "device": "test", "token": "original-test-credential"}); err != nil {
		t.Fatal(err)
	}
	if err := saveTeamConfig(TeamConfig{Name: team, Protocol: "otoha-team/1", Mode: "member", URL: endpoint, Project: team, Channel: team + "-general", Profile: profile}); err != nil {
		t.Fatal(err)
	}
	return team
}

func testPageDocument(title string) Object {
	return Object{"title": title, "summary": "Synthetic page", "items": []any{Object{"id": "checkout", "title": "Checkout", "status": "testing", "detail": "Synthetic content"}}}
}

func pageTestMessage(body Object, id int) Object {
	return Object{"id": id, "client_id": body["client_id"], "channel": body["channel"], "session": body["session"], "actor": "fixture", "created": 1,
		"payload": Object{"body": body["body"], "text": body["text"], "kind": body["kind"], "source": body["source"]}}
}

func TestPageRecoversAcceptedRequestAfterLostResponseAndRestart(t *testing.T) {
	t.Setenv("OTOHA_HOME", t.TempDir())
	var mu sync.Mutex
	var accepted Object
	var posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer original-test-credential" {
			t.Error("request changed the configured credential")
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
				json.NewEncoder(w).Encode(Object{"message": accepted, "message_id": 17, "duplicate": true})
			}
			return
		}
		var body Object
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		posts.Add(1)
		accepted = pageTestMessage(body, 17)
		// Commit succeeds. Only this test response is lost; no machine network
		// settings or other service is changed.
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		conn.Close()
	}))
	defer server.Close()
	team := pageTestTeam(t, server.URL)
	identityPath := filepath.Join(Home(), "fixture-identity.json")
	identityBefore, _ := os.ReadFile(identityPath)
	m := newPageManager(Home())
	result, err := m.enqueue(team, "publish", Object{"request_id": "stable-publish", "document": testPageDocument("Before restart")})
	if err != nil || S(result, "state") != "queued" {
		t.Fatal(result, err)
	}
	cfg, _ := loadTeamConfig(team)
	_, client, err := teamProfile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Transport.Close()
	op, _ := Load(m.pendingPath(team, "stable-publish"))
	if err = m.submitOperation(context.Background(), team, client, op); err == nil {
		t.Fatal("lost acceptance response was reported as known success")
	}
	if _, err = Load(m.pendingPath(team, "stable-publish")); err != nil {
		t.Fatal("unknown operation was not durably retained", err)
	}
	// A new runtime has no in-memory operation or response. Its only basis is
	// the original immutable request in the private outbox and server lookup.
	restarted := newPageManager(Home())
	op, _ = Load(restarted.pendingPath(team, "stable-publish"))
	if err = restarted.submitOperation(context.Background(), team, client, op); err != nil {
		t.Fatal(err)
	}
	if posts.Load() != 1 {
		t.Fatalf("lost response produced %d mutations, expected exactly one", posts.Load())
	}
	operation, err := restarted.operation(team, "stable-publish")
	if err != nil || S(operation, "state") != "accepted" || N(operation, "message_id") != 17 || !B(operation, "duplicate") {
		t.Fatal(operation, err)
	}
	if _, err = os.Stat(restarted.pendingPath(team, "stable-publish")); !os.IsNotExist(err) {
		t.Fatal("accepted operation remained in pending outbox")
	}
	identityAfter, _ := os.ReadFile(identityPath)
	if string(identityBefore) != string(identityAfter) {
		t.Fatal("resume modified the original identity")
	}
}

func TestPageOfflineQueueAutomaticallyFlushesAfterRuntimeRestart(t *testing.T) {
	t.Setenv("OTOHA_HOME", t.TempDir())
	var online atomic.Bool
	var posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !online.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(Object{"error": "isolated-test-outage"})
			return
		}
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(Object{"error": "message_not_found"})
			return
		}
		posts.Add(1)
		json.NewEncoder(w).Encode(Object{"message_id": 23, "duplicate": false})
	}))
	defer server.Close()
	team := pageTestTeam(t, server.URL)
	m := newPageManager(Home())
	if _, err := m.enqueue(team, "feedback", Object{"request_id": "offline-feedback", "page_request_id": "observed-v1", "page_message_id": 11, "text": "Keep the visible source revision"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan struct{})
	go func() { m.flushTeam(ctx, team, make(chan struct{})); close(finished) }()
	deadline := time.Now().Add(3 * time.Second)
	for {
		op, _ := m.operation(team, "offline-feedback")
		if S(op, "last_error") != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("offline operation was not retained with its failure")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-finished
	if posts.Load() != 0 {
		t.Fatal("offline service accepted an operation")
	}
	online.Store(true)
	restarted := newPageManager(Home())
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	finished = make(chan struct{})
	go func() { restarted.flushTeam(ctx, team, make(chan struct{})); close(finished) }()
	defer func() { cancel(); <-finished }()
	deadline = time.Now().Add(3 * time.Second)
	for {
		op, _ := restarted.operation(team, "offline-feedback")
		if S(op, "state") == "accepted" {
			if N(op, "message_id") != 23 || posts.Load() != 1 || S(op, "page_request_id") != "observed-v1" || N(op, "page_message_id") != 11 {
				t.Fatal(op, posts.Load())
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("queue required manual retry after restart", op)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func pageTestEvent(id int, request, operation string, content Object) Object {
	payload := Clone(content)
	payload["schema"], payload["operation"] = "otoha-page/1", operation
	raw, _ := json.Marshal(payload)
	return Object{"id": id, "kind": "message.created", "payload": Object{"id": id * 10, "channel": "page-fixture-general", "actor": "fixture", "client_id": request, "payload": Object{"body": string(raw), "source": "agent"}}}
}

func TestPageProjectionKeepsFeedbackSourceAndDurableCursor(t *testing.T) {
	t.Setenv("OTOHA_HOME", t.TempDir())
	team := pageTestTeam(t, "http://127.0.0.1:1")
	m := newPageManager(Home())
	page := Object{"events": []Object{
		pageTestEvent(1, "revision-one", "publish", Object{"document": testPageDocument("Version one")}),
		pageTestEvent(2, "feedback-one", "feedback", Object{"page_request_id": "revision-one", "page_message_id": 10, "text": "Feedback about version one"}),
		pageTestEvent(3, "revision-two", "publish", Object{"document": testPageDocument("Version two")}),
	}, "cursor": 3, "has_more": false}
	// Decode once, as an actual HTTP response is decoded, so event collection
	// validation uses the real wire representation rather than test-only types.
	data, _ := json.Marshal(page)
	json.Unmarshal(data, &page)
	if err := m.acceptEvents(team, "page-fixture-general", page); err != nil {
		t.Fatal(err)
	}
	restarted := newPageManager(Home())
	state, err := restarted.snapshot(team)
	if err != nil || N(state, "cursor") != 3 || S(O(state, "page"), "request_id") != "revision-two" {
		t.Fatal(state, err)
	}
	feedback := Items(state, "feedback")
	if len(feedback) != 1 || S(feedback[0], "page_request_id") != "revision-one" || N(feedback[0], "page_message_id") != 10 {
		t.Fatal("feedback silently moved to a newer page", feedback)
	}
	if err = restarted.acceptEvents(team, "page-fixture-general", page); err == nil {
		t.Fatal("replayed event page was accepted again")
	}
	state, _ = restarted.snapshot(team)
	if len(Items(state, "feedback")) != 1 || N(state, "cursor") != 3 {
		t.Fatal("replay changed accepted projection", state)
	}
}

func TestPageFeedbackDistinguishesActorsReusingTheSameRequestID(t *testing.T) {
	t.Setenv("OTOHA_HOME", t.TempDir())
	team := pageTestTeam(t, "http://127.0.0.1:1")
	m := newPageManager(Home())
	fromA := pageTestEvent(1, "page-v1", "publish", Object{"document": testPageDocument("Actor A's page")})
	O(fromA, "payload")["actor"] = "actor-a"
	fromB := pageTestEvent(2, "page-v1", "publish", Object{"document": testPageDocument("Actor B's page")})
	O(fromB, "payload")["actor"] = "actor-b"
	page := Object{"events": []Object{fromA, fromB}, "cursor": 2, "has_more": false}
	data, _ := json.Marshal(page)
	json.Unmarshal(data, &page)
	if err := m.acceptEvents(team, "page-fixture-general", page); err != nil {
		t.Fatal(err)
	}
	state, _ := m.snapshot(team)
	if N(O(state, "page"), "message_id") != 20 || S(O(state, "page"), "request_id") != "page-v1" {
		t.Fatal("fixture did not advance to actor B's distinct page", state)
	}
	// The current page has the same request ID, but this feedback was written
	// against actor A's already accepted message 10. Do not silently bind it
	// to the currently displayed message 20.
	op, err := m.enqueue(team, "feedback", Object{"request_id": "reply-a", "page_request_id": "page-v1", "page_message_id": 10, "text": "About A's version"})
	if err != nil || N(op, "page_message_id") != 10 {
		t.Fatal("enqueue lost the original accepted page identity", op, err)
	}
	feedbackA := pageTestEvent(3, "reply-a", "feedback", Object{"page_request_id": "page-v1", "page_message_id": 10, "text": "About A's version"})
	feedbackB := pageTestEvent(4, "reply-b", "feedback", Object{"page_request_id": "page-v1", "page_message_id": 20, "text": "About B's version"})
	page = Object{"events": []Object{feedbackA, feedbackB}, "cursor": 4, "has_more": false}
	data, _ = json.Marshal(page)
	json.Unmarshal(data, &page)
	if err = m.acceptEvents(team, "page-fixture-general", page); err != nil {
		t.Fatal(err)
	}
	state, _ = newPageManager(Home()).snapshot(team)
	feedback := Items(state, "feedback")
	if len(feedback) != 2 || S(feedback[0], "page_request_id") != "page-v1" || S(feedback[1], "page_request_id") != "page-v1" || N(feedback[0], "page_message_id") != 10 || N(feedback[1], "page_message_id") != 20 {
		t.Fatal("feedback identities were merged by actor-local client ID", feedback)
	}
}

func TestPageFeedbackRequiresExplicitAcceptedMessageIdentity(t *testing.T) {
	t.Setenv("OTOHA_HOME", t.TempDir())
	team := pageTestTeam(t, "http://127.0.0.1:1")
	m := newPageManager(Home())
	for _, messageID := range []any{nil, 0, -1, 1.5, "10", true} {
		if _, err := m.enqueue(team, "feedback", Object{"request_id": "invalid-source", "page_request_id": "page-v1", "page_message_id": messageID, "text": "Ambiguous source"}); err == nil {
			t.Fatal("feedback accepted a missing or nonpositive/noninteger source identity", messageID)
		}
	}
	if _, err := os.Stat(m.pendingPath(team, "invalid-source")); !os.IsNotExist(err) {
		t.Fatal("invalid feedback entered the durable outbox")
	}
}

func TestPageLocalAPIRequiresAuthenticationAndSameOriginWrites(t *testing.T) {
	t.Setenv("OTOHA_HOME", t.TempDir())
	team := pageTestTeam(t, "http://127.0.0.1:1")
	m := newPageManager(Home())
	m.token = "private-fixture-token"
	server := httptest.NewServer(http.HandlerFunc(m.serveHTTP))
	m.baseURL = server.URL
	defer server.Close()
	response, err := server.Client().Get(server.URL + "/api/state?team=" + team)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatal("private state was readable without authentication")
	}
	request, _ := http.NewRequest(http.MethodPost, server.URL+"/api/session", strings.NewReader("{}"))
	request.Header.Set("Authorization", "Bearer "+m.token)
	request.Header.Set("Origin", "https://unrelated.test")
	response, err = server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatal("cross-origin session establishment was accepted")
	}
	request, _ = http.NewRequest(http.MethodPost, server.URL+"/api/session", strings.NewReader("{}"))
	request.Header.Set("Authorization", "Bearer "+m.token)
	request.Header.Set("Origin", server.URL)
	response, err = server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	cookies := response.Cookies()
	if response.StatusCode != http.StatusOK || len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatal("browser did not receive a private same-site session")
	}
	request, _ = http.NewRequest(http.MethodGet, server.URL+"/api/state?team="+team, nil)
	request.AddCookie(cookies[0])
	response, err = server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatal("authenticated browser could not read its page")
	}
}

func TestPageValidatesRendererAndActualEncodedRequestBeforeQueue(t *testing.T) {
	t.Setenv("OTOHA_HOME", t.TempDir())
	team := pageTestTeam(t, "http://127.0.0.1:1")
	m := newPageManager(Home())
	invalid := []Object{
		{"title": "Bad summary", "summary": 1},
		{"title": "Missing ID", "items": []any{Object{"title": "A"}}},
		{"title": "Missing title", "items": []any{Object{"id": "a"}}},
		{"title": "Duplicate ID", "items": []any{Object{"id": "a", "title": "A"}, Object{"id": "a", "title": "B"}}},
		{"title": "Bad status", "items": []any{Object{"id": "a", "title": "A", "status": true}}},
	}
	for _, document := range invalid {
		if _, err := m.enqueue(team, "publish", Object{"request_id": "invalid-renderer", "document": document}); err == nil {
			t.Fatal("invalid renderer document entered the durable queue", document)
		}
	}
	large := Object{"title": "Fits the document limit", "summary": strings.Repeat("x", 34000)}
	if _, err := pageDocument(large); err != nil {
		t.Fatal("fixture should fit the document limit", err)
	}
	if _, err := m.enqueue(team, "publish", Object{"request_id": "too-large-on-wire", "document": large}); err == nil || !strings.Contains(err.Error(), "65536") {
		t.Fatal("actual duplicated/escaped HTTP body was not checked before queueing", err)
	}
	if _, err := os.Stat(m.pendingPath(team, "too-large-on-wire")); !os.IsNotExist(err) {
		t.Fatal("oversized request was persisted as an apparently valid operation")
	}
	valid := Object{"request_id": "stable-id", "document": testPageDocument("Valid")}
	if _, err := m.enqueue(team, "publish", valid); err != nil {
		t.Fatal(err)
	}
	if _, err := m.enqueue(team, "publish", valid); err != nil {
		t.Fatal("same operation was not idempotent", err)
	}
	if _, err := m.enqueue(team, "publish", Object{"request_id": "stable-id", "document": testPageDocument("Different")}); err == nil {
		t.Fatal("request identity was silently reused for different content")
	}
}

func TestPagePermanentAuthorizationFailureStopsAutomaticResubmission(t *testing.T) {
	t.Setenv("OTOHA_HOME", t.TempDir())
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(Object{"error": "project_forbidden"})
	}))
	defer server.Close()
	team := pageTestTeam(t, server.URL)
	m := newPageManager(Home())
	if _, err := m.enqueue(team, "publish", Object{"request_id": "forbidden-publish", "document": testPageDocument("No authority")}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan struct{})
	go func() { m.flushTeam(ctx, team, make(chan struct{})); close(finished) }()
	defer func() { cancel(); <-finished }()
	deadline := time.Now().Add(3 * time.Second)
	for {
		op, _ := m.operation(team, "forbidden-publish")
		if S(op, "state") == "needs_attention" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("permission rejection was hidden in automatic retry", op)
		}
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(30 * time.Millisecond)
	if calls.Load() != 1 {
		t.Fatal("permission rejection was retried without a changed authorization", calls.Load())
	}
}

func TestPageEmptyHealthyChannelBecomesReadyBeforeLongPoll(t *testing.T) {
	t.Setenv("OTOHA_HOME", t.TempDir())
	first := make(chan string, 1)
	longPoll := make(chan string, 1)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			first <- r.URL.Query().Get("wait")
			json.NewEncoder(w).Encode(Object{"events": []any{}, "cursor": 0, "has_more": false})
			return
		}
		longPoll <- r.URL.Query().Get("wait")
		<-r.Context().Done()
	}))
	defer server.Close()
	team := pageTestTeam(t, server.URL)
	m := newPageManager(Home())
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan struct{})
	go func() { m.pollTeam(ctx, team); close(finished) }()
	defer func() { cancel(); <-finished }()
	select {
	case wait := <-first:
		if wait != "0" {
			t.Fatal("initial readiness blocked on a long poll", wait)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("initial readiness request did not run")
	}
	select {
	case wait := <-longPoll:
		if wait != "10" {
			t.Fatal("healthy idle channel did not use a long poll", wait)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("idle receiver did not switch to a long poll")
	}
	state, err := m.snapshot(team)
	if err != nil || S(state, "connection") != "online" || N(state, "cursor") != 0 {
		t.Fatal("empty healthy channel was not ready", state, err)
	}
}

func TestPageRequestIDsUseProtocolAlphabetAndKeepPathsLocal(t *testing.T) {
	t.Setenv("OTOHA_HOME", t.TempDir())
	team := pageTestTeam(t, "http://127.0.0.1:1")
	m := newPageManager(Home())
	valid := []string{
		"1fa8c1be-782d-44b0-b44b-2efb1d3eac96",
		"aDefaultGeneratedABC_0123-xyz",
		"Page.Revision.1",
		ID(),
	}
	events := []Object{}
	for i, id := range valid {
		op, err := m.enqueue(team, "publish", Object{"request_id": id, "document": testPageDocument("Protocol IDs")})
		if err != nil || S(op, "request_id") != id {
			t.Fatal("valid server client_id was rejected before queueing", id, op, err)
		}
		if _, err = m.operation(team, id); err != nil {
			t.Fatal("valid browser/default ID could not be looked up", id, err)
		}
		events = append(events, pageTestEvent(i+1, id, "publish", Object{"document": testPageDocument("Protocol IDs")}))
		if _, err = m.enqueue(team, "feedback", Object{"request_id": fmt.Sprint("reply-", i), "page_request_id": id, "page_message_id": i + 1, "text": "Retain the exact source ID"}); err != nil {
			t.Fatal("feedback rejected a valid source request ID", id, err)
		}
	}
	// Wire decoding and event application must retain the same protocol IDs,
	// rather than filtering a valid source page out during synchronization.
	page := Object{"events": events, "cursor": len(events), "has_more": false}
	data, _ := json.Marshal(page)
	json.Unmarshal(data, &page)
	if err := m.acceptEvents(team, "page-fixture-general", page); err != nil {
		t.Fatal(err)
	}
	state, _ := m.snapshot(team)
	if S(O(state, "page"), "request_id") != valid[len(valid)-1] {
		t.Fatal("valid protocol source ID was discarded from projection", state)
	}
	for _, id := range []string{".", "..", "../escape", ".hidden", "a/b", `a\b`, "a b", strings.Repeat("a", 81)} {
		if _, err := m.enqueue(team, "publish", Object{"request_id": id, "document": testPageDocument("Path attempt")}); err == nil {
			t.Fatal("invalid/path request ID entered the queue", id)
		}
		if _, err := m.operation(team, id); err == nil {
			t.Fatal("invalid/path operation lookup was allowed", id)
		}
	}
}

func TestPageGeneratedRequestIDsSubmitToRealCollaborationServer(t *testing.T) {
	if _, err := os.Stat(serverScript()); err != nil {
		t.Skipf("collaboration server source is unavailable: %v", err)
	}
	t.Setenv("OTOHA_HOME", t.TempDir())
	t.Setenv("OTOHA_REPO_ROOT", repoRoot())
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	initialized, err := TeamCommand(ctx, []string{"init", "page-id-server", "--min-free-bytes", "0"})
	if err != nil {
		t.Fatal(err)
	}
	defer stopProcess(N(initialized, "server_pid"))
	cfg, err := loadTeamConfig("page-id-server")
	if err != nil {
		t.Fatal(err)
	}
	_, client, err := teamProfile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Transport.Close()
	m := newPageManager(Home())
	// Both operations omit request_id, as a normal CLI caller may do. The
	// default ID alphabet must survive the local outbox and real server parser.
	published, err := m.enqueue(cfg.Name, "publish", Object{"document": testPageDocument("Generated operation identity")})
	if err != nil {
		t.Fatal(err)
	}
	pageID := S(published, "request_id")
	op, err := Load(m.pendingPath(cfg.Name, pageID))
	if err != nil {
		t.Fatal(err)
	}
	if err = m.submitOperation(ctx, cfg.Name, client, op); err != nil {
		t.Fatal("real service rejected the automatically generated publish ID", err)
	}
	receipt, err := m.operation(cfg.Name, pageID)
	if err != nil || S(receipt, "state") != "accepted" || N(receipt, "message_id") <= 0 {
		t.Fatal(receipt, err)
	}
	feedback, err := m.enqueue(cfg.Name, "feedback", Object{"page_request_id": pageID, "page_message_id": N(receipt, "message_id"), "text": "Feedback with an automatically generated request ID"})
	if err != nil {
		t.Fatal(err)
	}
	feedbackID := S(feedback, "request_id")
	op, err = Load(m.pendingPath(cfg.Name, feedbackID))
	if err != nil {
		t.Fatal(err)
	}
	if err = m.submitOperation(ctx, cfg.Name, client, op); err != nil {
		t.Fatal("real service rejected the automatically generated feedback ID", err)
	}
	html := `<!doctype html><html><head><style>body{background:#111;color:#eee}</style></head><body><canvas id="scene"></canvas><button>任意界面</button></body></html>`
	htmlDocument := Object{"format": "html", "html": html}
	artifact, err := m.enqueue(cfg.Name, "publish", Object{"document": htmlDocument})
	if err != nil {
		t.Fatal("HTML artifact without work-template fields was rejected", err)
	}
	op, err = Load(m.pendingPath(cfg.Name, S(artifact, "request_id")))
	if err != nil {
		t.Fatal(err)
	}
	if err = m.submitOperation(ctx, cfg.Name, client, op); err != nil {
		t.Fatal("HTML artifact did not reach the real service", err)
	}
	accepted, err := client.Call(ctx, http.MethodGet, "/v1/messages?channel="+cfg.Channel, nil)
	if err != nil || len(Items(accepted, "messages")) != 3 {
		t.Fatal("default-ID operations and opaque HTML did not reach the actual persistent service", accepted, err)
	}
	page, err := client.Call(ctx, http.MethodGet, "/v1/events?channel="+cfg.Channel+"&after=0&wait=0", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = m.acceptEvents(cfg.Name, cfg.Channel, page); err != nil {
		t.Fatal(err)
	}
	state, err := newPageManager(Home()).snapshot(cfg.Name)
	document := O(O(state, "page"), "document")
	if err != nil || S(document, "format") != "html" || S(document, "html") != html {
		t.Fatal("synchronized HTML was not preserved verbatim", state, err)
	}
	if _, exists := document["items"]; exists {
		t.Fatal("an opaque HTML artifact acquired the work-list template")
	}
	if _, exists := document["title"]; exists {
		t.Fatal("an opaque HTML artifact was forced to have a template title")
	}
}

func TestPageHTMLFileInferenceAndExplicitFormat(t *testing.T) {
	html := "<!doctype html><html><body><main>自由呈现</main></body></html>"
	directory := t.TempDir()
	for _, filename := range []string{"presentation.html", "presentation.HTM", "no-extension"} {
		path := filepath.Join(directory, filename)
		if err := os.WriteFile(path, []byte(html), 0600); err != nil {
			t.Fatal(err)
		}
		format := ""
		if filename == "no-extension" {
			format = "html"
		}
		document, err := pageDocumentFile(path, format)
		if err != nil || S(document, "format") != "html" || S(document, "html") != html {
			t.Fatal("HTML file was forced through the work JSON template", filename, document, err)
		}
	}
	invalid := filepath.Join(directory, "invalid.html")
	if err := os.WriteFile(invalid, []byte{0xff, 0xfe}, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := pageDocumentFile(invalid, "html"); err == nil {
		t.Fatal("non-UTF-8 artifact was silently rewritten")
	}
	if _, err := pageDocument(Object{"format": "html", "html": []any{"not a string"}}); err == nil {
		t.Fatal("HTML artifact did not require a string")
	}
}
