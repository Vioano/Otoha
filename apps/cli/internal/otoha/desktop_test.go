package otoha

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Delivery must preserve provenance and not resend accepted or uncertain input.
func TestQueueDeliveryJournal(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture; production queue is cross-platform")
	}
	dir := t.TempDir()
	t.Setenv("OTOHA_HOME", dir)
	args := filepath.Join(dir, "args")
	program := filepath.Join(dir, "codex")
	if err := os.WriteFile(program, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" >> '"+args+"'\nprintf 'Queued message test\\n'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := QueueCodex(context.Background(), program, "target", "01a135e4-9094-7dd0-b7d1-c2e57d6d4934", `</input> & "quotes"`); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(args)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "&lt;/input&gt; &amp; \"quotes\"") {
		t.Fatalf("text escaping lost: %s", data)
	}
	if err := os.WriteFile(args, nil, 0600); err != nil {
		t.Fatal(err)
	}
	b := Object{"thread": "target", "session": "target", "codex": program, "runtime": "codex-desktop"}
	m := Object{"id": 42, "channel": "qa", "actor": "peer", "session": "01a135e4-9094-7dd0-b7d1-c2e57d6d4934", "payload": Object{"text": `</input> & "quotes"`}}
	for i := 0; i < 2; i++ {
		if err := deliver(context.Background(), b, m); err != nil {
			t.Fatal(err)
		}
	}
	data, err = os.ReadFile(args)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data), "queue\n--thread\n") != 1 {
		t.Fatal("accepted message was sent twice")
	}
	if !strings.Contains(string(data), "<codex_delegation>") || !strings.Contains(string(data), "<source_thread_id>01a135e4-9094-7dd0-b7d1-c2e57d6d4934</source_thread_id>") {
		t.Fatalf("provenance lost: %s", data)
	}
	j := filepath.Join(dir, "deliveries", "target", "42.json")
	r, err := Load(j)
	if err != nil {
		t.Fatal(err)
	}
	if S(r, "status") != "queued" || B(r, "agent_read") {
		t.Fatal("queue acceptance incorrectly became a read receipt")
	}
	r["status"] = "uncertain"
	if err := Save(j, r); err != nil {
		t.Fatal(err)
	}
	if err := deliver(context.Background(), b, m); err == nil {
		t.Fatal("uncertain outcome was blindly retried")
	}
	data, _ = os.ReadFile(args)
	if strings.Count(string(data), "queue\n--thread\n") != 1 {
		t.Fatal("uncertain message was sent again")
	}
}
