package otoha

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// This carries the actual flusher's HTTP calls over real SSH direct-tcpip
// channels. It extends the pinned local forwarding fixture used by team join.
func pageFlusherSSHFixture(t *testing.T, handler http.Handler) (string, *atomic.Int32, *atomic.Int32) {
	t.Helper()
	upstream := httptest.NewServer(handler)
	t.Cleanup(upstream.Close)
	target, _ := net.ResolveTCPAddr("tcp", strings.TrimPrefix(upstream.URL, "http://"))
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := x509.MarshalPKCS8PrivateKey(private)
	keyFile := filepath.Join(t.TempDir(), "fixture-key")
	if err = os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encoded}), 0600); err != nil {
		t.Fatal(err)
	}
	signer, _ := ssh.NewSignerFromKey(private)
	config := &ssh.ServerConfig{PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
		if string(key.Marshal()) != string(signer.PublicKey().Marshal()) {
			return nil, io.EOF
		}
		return nil, nil
	}}
	config.AddHostKey(signer)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	handshakes, active := &atomic.Int32{}, &atomic.Int32{}
	var mu sync.Mutex
	connections := map[*ssh.ServerConn]bool{}
	t.Cleanup(func() {
		listener.Close()
		mu.Lock()
		defer mu.Unlock()
		for conn := range connections {
			conn.Close()
		}
	})
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				server, channels, requests, err := ssh.NewServerConn(conn, config)
				if err != nil {
					conn.Close()
					return
				}
				handshakes.Add(1)
				active.Add(1)
				mu.Lock()
				connections[server] = true
				mu.Unlock()
				defer func() {
					server.Close()
					mu.Lock()
					delete(connections, server)
					mu.Unlock()
					active.Add(-1)
				}()
				go func() {
					for request := range requests {
						if request.WantReply {
							request.Reply(true, nil)
						}
					}
				}()
				for channel := range channels {
					var requested struct {
						Host       string
						Port       uint32
						OriginHost string
						OriginPort uint32
					}
					if channel.ChannelType() != "direct-tcpip" || ssh.Unmarshal(channel.ExtraData(), &requested) != nil || requested.Host != "127.0.0.1" || int(requested.Port) != target.Port {
						channel.Reject(ssh.Prohibited, "outside test forwarding scope")
						continue
					}
					remote, err := net.Dial("tcp", target.String())
					if err != nil {
						channel.Reject(ssh.ConnectionFailed, "upstream unavailable")
						continue
					}
					forward, requests, err := channel.Accept()
					if err != nil {
						remote.Close()
						continue
					}
					go ssh.DiscardRequests(requests)
					go func() { io.Copy(forward, remote); forward.Close(); remote.Close() }()
					go func() { io.Copy(remote, forward); forward.Close(); remote.Close() }()
				}
			}()
		}
	}()
	localPort, err := freeLoopbackPort()
	if err != nil {
		t.Fatal(err)
	}
	team := pageTestTeam(t, "http://127.0.0.1:"+strconv.Itoa(localPort))
	cfg, _ := loadTeamConfig(team)
	profile, _ := Load(cfg.Profile)
	profile["gateway"] = Object{"host": "127.0.0.1", "port": listener.Addr().(*net.TCPAddr).Port, "user": "fixture", "key_file": keyFile,
		"host_public_key": string(ssh.MarshalAuthorizedKey(signer.PublicKey())),
		"forwards":        []any{Object{"local": localPort, "remote": target.Port}}}
	if err = Save(cfg.Profile, profile); err != nil {
		t.Fatal(err)
	}
	return team, handshakes, active
}

func pageFlusherWait(t *testing.T, condition func() bool, description string) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal(description)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestPageFlusherReusesSSHAcrossBatchesAndReloadsIdentity(t *testing.T) {
	t.Setenv("OTOHA_HOME", t.TempDir())
	var token atomic.Value
	token.Store("original-test-credential")
	var posts atomic.Int32
	team, handshakes, active := pageFlusherSSHFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token.Load().(string) {
			t.Error("flusher used stale credentials")
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(Object{"error": "stale_test_identity"})
			return
		}
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(Object{"error": "message_not_found"})
			return
		}
		json.NewEncoder(w).Encode(Object{"message_id": int(posts.Add(1)), "duplicate": false})
	}))
	m := newPageManager(Home())
	wake := make(chan struct{}, 1)
	m.workers[team] = &pageWorker{wake: wake}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.flushTeam(ctx, team, wake); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	send := func(id string) {
		t.Helper()
		if _, err := m.enqueueMessage(team, Object{"request_id": id, "text": "Synthetic sparse message"}); err != nil {
			t.Fatal(err)
		}
		pageFlusherWait(t, func() bool {
			op, _ := m.operation(team, id)
			return S(op, "state") == "accepted"
		}, "flusher did not accept "+id)
	}
	send("first-batch")
	if !m.mu.TryLock() {
		t.Fatal("idle flusher retained the manager lock")
	}
	m.mu.Unlock()
	send("second-batch")
	if handshakes.Load() != 1 || posts.Load() != 2 {
		t.Fatalf("healthy sparse batches recreated SSH: handshakes=%d posts=%d", handshakes.Load(), posts.Load())
	}
	cfg, _ := loadTeamConfig(team)
	profile, _ := Load(cfg.Profile)
	profile["token"] = "replacement-test-credential"
	if err := Save(cfg.Profile, profile); err != nil {
		t.Fatal(err)
	}
	token.Store("replacement-test-credential")
	send("new-identity")
	if handshakes.Load() != 2 || posts.Load() != 3 {
		t.Fatalf("changed identity did not replace transport: handshakes=%d posts=%d", handshakes.Load(), posts.Load())
	}
	if err := os.Remove(cfg.Profile); err != nil {
		t.Fatal(err)
	}
	if _, err := m.enqueueMessage(team, Object{"request_id": "identity-missing", "text": "Must remain pending"}); err != nil {
		t.Fatal(err)
	}
	pageFlusherWait(t, func() bool { return active.Load() == 0 }, "missing profile retained the old SSH identity")
	if posts.Load() != 3 {
		t.Fatal("flusher sent with a missing current profile")
	}
	op, _ := m.operation(team, "identity-missing")
	if S(op, "state") != "queued" {
		t.Fatal("configuration failure retired pending work", op)
	}
}

func TestPageFlusherTransientFailureDropsSSHButPreservesRequest(t *testing.T) {
	t.Setenv("OTOHA_HOME", t.TempDir())
	var fail atomic.Bool
	fail.Store(true)
	var posts atomic.Int32
	team, handshakes, _ := pageFlusherSSHFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail.Swap(false) {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(Object{"error": "temporary_test_failure"})
			return
		}
		if r.Method == http.MethodGet {
			if r.URL.Query().Get("client_id") != "same-original-request" {
				t.Error("retry changed the immutable client ID")
			}
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(Object{"error": "message_not_found"})
			return
		}
		var body Object
		json.NewDecoder(r.Body).Decode(&body)
		if S(body, "client_id") != "same-original-request" {
			t.Error("retry posted a different request")
		}
		posts.Add(1)
		json.NewEncoder(w).Encode(Object{"message_id": 55, "duplicate": false})
	}))
	m := newPageManager(Home())
	wake := make(chan struct{}, 1)
	if _, err := m.enqueueMessage(team, Object{"request_id": "same-original-request", "text": "Synthetic retry"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.flushTeam(ctx, team, wake); close(done) }()
	defer func() { cancel(); <-done }()
	pageFlusherWait(t, func() bool {
		op, _ := m.operation(team, "same-original-request")
		return S(op, "state") == "accepted"
	}, "transient failure did not recover")
	if handshakes.Load() != 2 || posts.Load() != 1 {
		t.Fatalf("transient retry did not replace connection with one effect: handshakes=%d posts=%d", handshakes.Load(), posts.Load())
	}
}
