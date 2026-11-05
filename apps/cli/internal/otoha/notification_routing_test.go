package otoha

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

const notificationThread = "11111111-2222-4333-8444-555555555555"
const notificationSource = "66666666-7777-4888-8999-aaaaaaaaaaaa"

// This speaks the installed adapter's framed IPC protocol. It controls only
// host busy/idle/unknown responses, not Otoha's notification text or routing.
type notificationHostFixture struct {
	mu      sync.Mutex
	methods []string
	texts   []string
	outcome string
}

func notificationHost(t *testing.T, outcome string) *notificationHostFixture {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("private Unix IPC fixture; queue primitive has cross-platform process tests")
	}
	home, err := os.MkdirTemp("", "otoha-ipc-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	t.Setenv("CODEX_HOME", home)
	dir := filepath.Join(home, "ipc")
	if err = os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "ipc.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	if err = os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	host := &notificationHostFixture{outcome: outcome}
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go host.handle(conn)
		}
	}()
	return host
}

func (host *notificationHostFixture) handle(conn net.Conn) {
	defer conn.Close()
	for {
		var header [4]byte
		if _, err := io.ReadFull(conn, header[:]); err != nil {
			return
		}
		length := binary.LittleEndian.Uint32(header[:])
		if length == 0 || length > 1<<20 {
			return
		}
		data := make([]byte, length)
		if _, err := io.ReadFull(conn, data); err != nil {
			return
		}
		var request Object
		if json.Unmarshal(data, &request) != nil {
			return
		}
		method := S(request, "method")
		host.mu.Lock()
		host.methods = append(host.methods, method)
		if method == "thread-follower-steer-turn" {
			inputs := Items(O(request, "params"), "input")
			if len(inputs) > 0 {
				host.texts = append(host.texts, S(inputs[0], "text"))
			}
		}
		host.mu.Unlock()
		response := Object{"requestId": request["requestId"], "resultType": "success", "result": Object{}}
		switch method {
		case "initialize":
			response["result"] = Object{"clientId": "fixture-client"}
		case "thread-owner-discovery":
			response["handledByClientId"] = "fixture-owner"
		case "thread-follower-steer-turn":
			switch host.outcome {
			case "busy":
				response["result"] = Object{"result": Object{"turnId": "existing-busy-turn"}}
			case "idle":
				response["resultType"], response["error"] = "error", "Conversation target has no active turn to steer"
			case "unknown":
				return // Host received the request, but its acknowledgement is lost.
			}
		}
		data, _ = json.Marshal(response)
		binary.LittleEndian.PutUint32(header[:], uint32(len(data)))
		if _, err := conn.Write(append(header[:], data...)); err != nil {
			return
		}
	}
}

func (host *notificationHostFixture) observed() ([]string, []string) {
	host.mu.Lock()
	defer host.mu.Unlock()
	return append([]string(nil), host.methods...), append([]string(nil), host.texts...)
}

func notificationMessage() Object {
	body := strings.Repeat("PRIVATE_FULL_BODY_MUST_STAY_IN_INBOX\n", 200)
	return Object{"id": 42, "channel": "test-general", "actor": "peer", "session": notificationSource, "client_id": "original-request",
		"payload": Object{"uid": "original-request", "delivery": "both", "summary": "Review the revised dependency", "text": body, "body": body}}
}

func notificationBinding(codex string) Object {
	return Object{"thread": notificationThread, "session": notificationThread, "actor": "receiver", "codex": codex,
		"profile": Object{"url": "https://authority.invalid"}}
}

func notificationQueueFixture(t *testing.T) (string, string) {
	t.Helper()
	executable := filepath.Join(t.TempDir(), "codex")
	copyQueueFixture(t, executable)
	argv := filepath.Join(t.TempDir(), "argv.json")
	t.Setenv("OTOHA_TEST_QUEUE_ARGV", argv)
	return executable, argv
}

func notificationJournal(t *testing.T) Object {
	t.Helper()
	record, err := Load(filepath.Join(Home(), "deliveries", notificationThread, "42.json"))
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func TestNonurgentNotificationsNeverSteerBusyOrIdleHosts(t *testing.T) {
	for _, activity := range []string{"busy", "idle"} {
		for _, class := range []string{"", "queued", "unrecognized-legacy-class"} {
			t.Run(activity+"/"+class, func(t *testing.T) {
				t.Setenv("OTOHA_HOME", t.TempDir())
				host := notificationHost(t, activity)
				executable, argv := notificationQueueFixture(t)
				binding, message := notificationBinding(executable), notificationMessage()
				if class != "" {
					O(message, "payload")["class"] = class
				}
				if _, err := PersistInboxMessageLocal(context.Background(), nil, binding, message); err != nil {
					t.Fatal(err)
				}
				if err := deliver(context.Background(), binding, message); err != nil {
					t.Fatal(err)
				}
				methods, _ := host.observed()
				if len(methods) != 0 {
					t.Fatal("nonurgent notification contacted Desktop's steer adapter", methods)
				}
				data, err := os.ReadFile(argv)
				if err != nil {
					t.Fatal(err)
				}
				var args []string
				json.Unmarshal(data, &args)
				if len(args) != 5 || args[0] != "queue" || args[1] != "--thread" || args[2] != notificationThread {
					t.Fatal("notification did not use the host queue interface", args)
				}
				notice := args[4]
				if strings.Contains(notice, "PRIVATE_FULL_BODY") || strings.Contains(notice, `"payload"`) || !strings.Contains(notice, "Review the revised dependency") || !strings.Contains(notice, `"message_id":42`) || !strings.Contains(notice, "original-request") || !strings.Contains(notice, "authority.invalid") {
					t.Fatal("host input was not bounded summary plus stable reference", notice)
				}
				if strings.Contains(notice, "receipt --read") {
					t.Fatal("reading a notification demanded a false original-body read receipt")
				}
				record := notificationJournal(t)
				if S(record, "class") != "queued" || S(record, "status") != "queued" || !B(record, "host_accepted") || B(record, "agent_read") || B(O(record, "runtime_result"), "interrupt_delivered") {
					t.Fatal("queue admission was confused with interruption or reading", record)
				}
				_, inbox, err := findInbox("original-request")
				if err != nil || S(O(O(inbox, "message"), "payload"), "body") != S(O(message, "payload"), "body") || B(inbox, "read") {
					t.Fatal("notification removed the original or marked it read", err)
				}
			})
		}
	}
}

func TestInterruptNotificationSteersBusyQueuesIdleAndStopsOnUnknown(t *testing.T) {
	for _, outcome := range []string{"busy", "idle", "unknown"} {
		t.Run(outcome, func(t *testing.T) {
			t.Setenv("OTOHA_HOME", t.TempDir())
			host := notificationHost(t, outcome)
			executable, argv := notificationQueueFixture(t)
			message := notificationMessage()
			O(message, "payload")["class"] = "interrupt"
			binding := notificationBinding(executable)
			err := deliver(context.Background(), binding, message)
			if (err != nil) != (outcome == "unknown") {
				t.Fatal("unexpected admission outcome", outcome, err)
			}
			methods, notices := host.observed()
			steers := 0
			for _, method := range methods {
				if method == "thread-follower-steer-turn" {
					steers++
				}
			}
			if steers != 1 || len(notices) != 1 || strings.Contains(notices[0], "PRIVATE_FULL_BODY") || !strings.Contains(notices[0], "original-request") {
				t.Fatal("urgent path did not send exactly one summary/reference steer", methods)
			}
			record := notificationJournal(t)
			_, queueErr := os.Stat(argv)
			switch outcome {
			case "busy":
				if S(record, "status") != "steered" || !B(O(record, "runtime_result"), "interrupt_delivered") || !os.IsNotExist(queueErr) {
					t.Fatal("successful steer was mislabeled or queued again", record)
				}
			case "idle":
				if S(record, "status") != "queued" || S(O(record, "runtime_result"), "fallback_reason") != "desktop_idle" || B(O(record, "runtime_result"), "interrupt_delivered") || queueErr != nil {
					t.Fatal("idle fallback was hidden or labeled interrupted", record, queueErr)
				}
			case "unknown":
				if S(record, "status") != "uncertain" || !os.IsNotExist(queueErr) {
					t.Fatal("unknown steer was retried through queue", record)
				}
				if err = deliver(context.Background(), binding, message); err == nil {
					t.Fatal("unknown delivery journal allowed a second admission")
				}
				_, notices = host.observed()
				if len(notices) != 1 {
					t.Fatal("unknown steer was submitted twice")
				}
			}
			if B(record, "agent_read") {
				t.Fatal("host transport result became a read receipt")
			}
		})
	}
}

func TestUnsupportedQueueLeavesUnreadOriginalForExplicitInboxCheck(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("argument-parser rejection fixture uses a shell")
	}
	t.Setenv("OTOHA_HOME", t.TempDir())
	program := filepath.Join(t.TempDir(), "old-codex")
	called := filepath.Join(t.TempDir(), "calls")
	script := "#!/bin/sh\nprintf 'called\\n' >> " + shellQuote(called) + "\nprintf \"error: unrecognized subcommand 'queue'\\n\" >&2\nexit 2\n"
	if err := os.WriteFile(program, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	message, binding := notificationMessage(), notificationBinding(program)
	if _, err := PersistInboxMessageLocal(context.Background(), nil, binding, message); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := deliver(context.Background(), binding, message); err != nil {
			t.Fatal("known unsupported host should leave a checkable inbox, not block local receipt", err)
		}
	}
	record := notificationJournal(t)
	if S(record, "status") != "awaiting_inbox" || B(record, "host_accepted") || B(record, "agent_read") || S(record, "queued_at") != "" {
		t.Fatal("unsupported queue was recorded as accepted", record)
	}
	data, _ := os.ReadFile(called)
	if strings.Count(string(data), "called\n") != 1 {
		t.Fatal("unsupported command was repeatedly invoked")
	}
	_, original, err := findInbox("original-request")
	if err != nil || B(original, "read") || S(O(O(original, "message"), "payload"), "body") != S(O(message, "payload"), "body") {
		t.Fatal("pending original was lost or declared read", err)
	}
}

func TestNotificationWithoutSummaryDoesNotFallBackToFullLegacyText(t *testing.T) {
	message := notificationMessage()
	delete(O(message, "payload"), "summary")
	notice := notificationEnvelope(notificationBinding("unused"), message)
	data, _ := json.Marshal(notice)
	if strings.Contains(string(data), "PRIVATE_FULL_BODY") || S(notice, "summary") == "" || S(notice, "class") != "queued" {
		t.Fatal("legacy message leaked full text into notification", notice)
	}
	O(message, "payload")["summary"] = strings.Repeat("😀", 300) + "\x00\n"
	if summary := S(notificationEnvelope(notificationBinding("unused"), message), "summary"); len(summary) > 1000 || len([]rune(summary)) > 280 || strings.ContainsAny(summary, "\x00\n") {
		t.Fatal("notification summary did not preserve a bounded single-line envelope")
	}
}

func TestUnsupportedQueueDoesNotBlockReceiverInboxCursor(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("argument-parser rejection fixture uses a shell")
	}
	t.Setenv("OTOHA_HOME", t.TempDir())
	program := filepath.Join(t.TempDir(), "old-codex")
	if err := os.WriteFile(program, []byte("#!/bin/sh\nprintf \"error: unrecognized subcommand 'queue'\\n\" >&2\nexit 2\n"), 0700); err != nil {
		t.Fatal(err)
	}
	message := notificationMessage()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("after") == "4" {
			<-r.Context().Done()
			return
		}
		json.NewEncoder(w).Encode(Object{"events": []Object{{"id": 4, "kind": "message.created", "payload": message}}, "cursor": 4, "has_more": false})
	}))
	defer server.Close()
	binding := notificationBinding(program)
	binding["channel"], binding["cursor"] = "test-general", 0
	binding["allow_actors"] = []string{"peer"}
	binding["profile"] = Object{"url": server.URL, "actor": "receiver"}
	path := BindingPath(notificationThread, "test-general")
	if err := Save(path, binding); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan struct{})
	go func() { receiveBinding(ctx, path); close(finished) }()
	defer func() { cancel(); <-finished }()
	deadline := time.Now().Add(3 * time.Second)
	for {
		state, _ := Load(bindingStatePath(path))
		if N(state, "cursor") == 4 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("unsupported host queue blocked durable inbox/event progress", state)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if record := notificationJournal(t); S(record, "status") != "awaiting_inbox" || B(record, "host_accepted") {
		t.Fatal("host rejection was recorded as notification delivery", record)
	}
	_, original, err := findInbox("original-request")
	if err != nil || S(O(O(original, "message"), "payload"), "body") != S(O(message, "payload"), "body") || B(original, "read") {
		t.Fatal("receiver did not preserve the complete unread original", err)
	}
}

func TestUnknownQueueOutcomeIsNotRetriedOrLabeledUnsupported(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process failure fixture uses a shell")
	}
	t.Setenv("OTOHA_HOME", t.TempDir())
	program, calls := filepath.Join(t.TempDir(), "uncertain-codex"), filepath.Join(t.TempDir(), "calls")
	script := "#!/bin/sh\nprintf 'called\\n' >> " + shellQuote(calls) + "\nprintf 'queue connection lost after possible admission\\n' >&2\nexit 1\n"
	if err := os.WriteFile(program, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := deliver(context.Background(), notificationBinding(program), notificationMessage()); err == nil {
			t.Fatal("unknown queue outcome was declared successful")
		}
	}
	record := notificationJournal(t)
	if S(record, "status") != "uncertain" || record["host_accepted"] != nil || B(record, "agent_read") {
		t.Fatal("unknown queue admission was assigned a known result", record)
	}
	data, _ := os.ReadFile(calls)
	if strings.Count(string(data), "called\n") != 1 {
		t.Fatal("unknown queue operation was submitted again")
	}
}
