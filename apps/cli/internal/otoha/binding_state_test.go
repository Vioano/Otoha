package otoha

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestLongPollDoesNotOverwriteConcurrentBindingUpdate(t *testing.T) {
	t.Setenv("OTOHA_HOME", t.TempDir())
	started, release := make(chan struct{}), make(chan struct{})
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			close(started)
			<-release
			json.NewEncoder(w).Encode(Object{"events": []any{}, "cursor": 8, "has_more": false})
			return
		}
		<-r.Context().Done()
	}))
	defer server.Close()
	path := BindingPath("thread", "project-general")
	b := Object{"thread": "thread", "session": "thread", "actor": "captain", "channel": "project-general", "cursor": 7, "codex": "previous-cli", "allow_actors": []string{"old-member"}, "profile": Object{"url": server.URL, "actor": "captain"}}
	if err := Save(path, b); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan struct{})
	go func() { receiveBinding(ctx, path); close(finished) }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("receiver did not enter long poll")
	}
	b["codex"] = "desktop-native-cli"
	b["allow_actors"] = []string{"old-member", "new-member"}
	if err := Save(path, b); err != nil {
		t.Fatal(err)
	}
	close(release)
	deadline := time.Now().Add(3 * time.Second)
	for {
		state, _ := Load(bindingStatePath(path))
		if N(state, "cursor") == 8 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("cursor did not advance in separate receiver state")
		}
		time.Sleep(5 * time.Millisecond)
	}
	updated, err := Load(path)
	if err != nil || S(updated, "codex") != "desktop-native-cli" || len(Array(updated["allow_actors"])) != 2 {
		t.Fatal("receiver overwrote a concurrent binding update", err)
	}
	if N(updated, "cursor") != 7 {
		t.Fatal("receiver wrote progress back over the configuration")
	}
	cancel()
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("receiver failed to stop")
	}
}

func TestBindingStateRetainsExistingCursorAndChecksIdentity(t *testing.T) {
	t.Setenv("OTOHA_HOME", t.TempDir())
	b := Object{"thread": "thread", "actor": "captain", "channel": "project-general", "cursor": 123}
	path := BindingPath("thread", "project-general")
	state, err := loadBindingState(path, b)
	if err != nil || N(state, "cursor") != 123 {
		t.Fatal("existing cursor was not retained", err)
	}
	state["cursor"] = 456
	if err = Save(bindingStatePath(path), state); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadBindingState(path, b)
	if err != nil || N(loaded, "cursor") != 456 {
		t.Fatal("saved progress was replaced with the old seed", err)
	}
	b["actor"] = "different-member"
	if _, err = loadBindingState(path, b); err == nil {
		t.Fatal("state belonging to another identity was reused")
	}
}
