package otoha

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestEmptySuccessfulEventResponseDoesNotResetCursor(t *testing.T) {
	t.Setenv("OTOHA_HOME", t.TempDir())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	path := BindingPath("recovery-fixture", "project-general")
	b := Object{"thread": "recovery-fixture", "session": "recovery-fixture", "actor": "captain", "channel": "project-general", "cursor": 137, "profile": Object{"url": server.URL, "actor": "captain"}}
	if err := Save(path, b); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan struct{})
	go func() { receiveBinding(ctx, path); close(finished) }()
	defer func() {
		cancel()
		select {
		case <-finished:
		case <-time.After(3 * time.Second):
			t.Error("receiver did not stop")
		}
	}()
	deadline := time.Now().Add(3 * time.Second)
	for {
		state, err := Load(bindingStatePath(path))
		if err == nil {
			if N(state, "cursor") != 137 {
				t.Fatalf("empty HTTP 200 reset the existing cursor to %d", N(state, "cursor"))
			}
			if S(state, "last_error") == "" {
				t.Fatal("empty events response was accepted as progress")
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("receiver did not record the failed poll")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestReceiverRecoversPartialResponseAndDoesNotRequeueAfterRestart(t *testing.T) {
	t.Setenv("OTOHA_HOME", t.TempDir())
	executable := filepath.Join(Home(), "fixture-codex")
	if runtime.GOOS == "windows" {
		executable += ".exe"
	}
	copyQueueFixture(t, executable)
	history := filepath.Join(Home(), "fixture-queue-history.jsonl")
	t.Setenv("OTOHA_TEST_QUEUE_ARGV", filepath.Join(Home(), "fixture-argv.json"))
	t.Setenv("OTOHA_TEST_QUEUE_HISTORY", history)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/events" || r.Header.Get("Authorization") != "Bearer unchanged-credential" {
			t.Error("recovery used a different endpoint or credential")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Query().Get("after") == "140" {
			<-r.Context().Done()
			return
		}
		if r.URL.Query().Get("after") != "137" {
			t.Error("failed poll changed the durable cursor", r.URL.Query().Get("after"))
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		switch requests.Add(1) {
		case 1:
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte(`{"error":"isolated-network-outage"}`))
		case 2:
			w.Header().Set("Content-Length", "100")
			w.Write([]byte(`{"events":[`))
		case 3:
			w.WriteHeader(http.StatusOK)
		default:
			json.NewEncoder(w).Encode(Object{"events": []Object{{"id": 138, "kind": "message.created", "payload": Object{
				"id": 42, "channel": "project-general", "actor": "peer", "session": "01a135e4-9094-7dd0-b7d1-c2e57d6d4934",
				"payload": Object{"delivery": "both", "to_sessions": []string{"captain/recovery-fixture"}, "text": "after reconnection"},
			}}}, "cursor": 140, "has_more": false})
		}
	}))
	defer server.Close()
	path := BindingPath("recovery-fixture", "project-general")
	b := Object{"thread": "recovery-fixture", "session": "recovery-fixture", "actor": "captain", "channel": "project-general", "cursor": 137,
		"codex": executable, "allow_actors": []string{"peer"}, "profile": Object{"url": server.URL, "actor": "captain", "token": "unchanged-credential"}}
	if err := Save(path, b); err != nil {
		t.Fatal(err)
	}
	startAndWait := func() {
		t.Helper()
		ctx, cancel := context.WithCancel(context.Background())
		finished := make(chan struct{})
		go func() { receiveBinding(ctx, path); close(finished) }()
		defer func() {
			cancel()
			select {
			case <-finished:
			case <-time.After(3 * time.Second):
				t.Error("receiver failed to stop")
			}
		}()
		deadline := time.Now().Add(20 * time.Second)
		for {
			state, _ := Load(bindingStatePath(path))
			if N(state, "cursor") == 140 {
				if S(state, "last_error") != "" {
					t.Fatal("recovered receiver retained a stale error")
				}
				return
			}
			if time.Now().After(deadline) {
				t.Fatal("receiver did not resume after response failures")
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	started := time.Now()
	startAndWait()
	t.Logf("isolated recovery with three failed responses: %s", time.Since(started))
	journal, err := Load(filepath.Join(Home(), "deliveries", "recovery-fixture", "42.json"))
	if err != nil || S(journal, "status") != "queued" || B(journal, "agent_read") {
		t.Fatal("recovery did not record queue acceptance with the correct read boundary", err)
	}
	// Simulate interruption after admission was journaled but before the event
	// cursor was saved. A new worker must consult the existing durable journal.
	if err = Save(bindingStatePath(path), Object{"actor": "captain", "thread": "recovery-fixture", "channel": "project-general", "cursor": 137}); err != nil {
		t.Fatal(err)
	}
	startAndWait()
	data, err := os.ReadFile(history)
	if err != nil || len(strings.Split(strings.TrimSpace(string(data)), "\n")) != 1 {
		t.Fatal("restart submitted an already accepted message again", err)
	}
	configuration, err := Load(path)
	if err != nil || N(configuration, "cursor") != 137 || S(O(configuration, "profile"), "token") != "unchanged-credential" {
		t.Fatal("recovery changed original configuration or credentials", err)
	}
}

func TestEventPageValidationRejectsRollbackAndMalformedProgress(t *testing.T) {
	valid := `{"events":[{"id":138,"kind":"session.created","payload":{}}],"cursor":140,"has_more":false}`
	for _, input := range []string{
		`{}`, `{"cursor":140}`, `{"events":null,"cursor":140,"has_more":false}`,
		`{"events":[],"cursor":136,"has_more":false}`,
		`{"events":[],"cursor":140.5,"has_more":false}`,
		`{"events":[],"cursor":137,"has_more":true}`,
		`{"events":["invalid"],"cursor":140,"has_more":false}`,
		`{"events":[{"id":141,"kind":"session.created","payload":{}}],"cursor":140,"has_more":false}`,
		`{"events":[{"id":137,"kind":"session.created","payload":{}}],"cursor":140,"has_more":false}`,
		`{"events":[{"id":139,"kind":"session.created","payload":{}},{"id":138,"kind":"session.created","payload":{}}],"cursor":140,"has_more":false}`,
	} {
		var page Object
		if err := json.Unmarshal([]byte(input), &page); err != nil {
			t.Fatal(err)
		}
		if err := validateEventPage(page, 137); err == nil {
			t.Fatalf("malformed event page accepted: %s", input)
		}
	}
	var page Object
	json.Unmarshal([]byte(valid), &page)
	if err := validateEventPage(page, 137); err != nil {
		t.Fatal("valid filtered event page rejected", err)
	}
}
