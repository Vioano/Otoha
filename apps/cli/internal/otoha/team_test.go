package otoha

import (
	"context"
	"os"
	"syscall"
	"testing"
	"time"
)

func TestTeamLocalInitInviteJoinAndFakeChannel(t *testing.T) {
	if _, err := os.Stat(serverScript()); err != nil {
		t.Skipf("collaboration server source is unavailable: %v", err)
	}
	root := t.TempDir()
	t.Setenv("OTOHA_HOME", root+"/captain")
	t.Setenv("OTOHA_REPO_ROOT", repoRoot())
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	initialized, err := TeamCommand(ctx, []string{"init", "demo", "--min-free-bytes", "0"})
	if err != nil {
		t.Fatal(err)
	}
	pid := N(initialized, "server_pid")
	if pid < 1 {
		t.Fatalf("missing local server pid: %#v", initialized)
	}
	defer syscall.Kill(pid, syscall.SIGTERM)

	invited, err := TeamCommand(ctx, []string{"invite", "demo", "alice", "--out", root + "/alice.json"})
	if err != nil {
		t.Fatal(err)
	}
	if !B(invited, "invited") {
		t.Fatalf("unexpected invite result: %#v", invited)
	}

	memberHome := root + "/member"
	t.Setenv("OTOHA_HOME", memberHome)
	joined, err := TeamCommand(ctx, []string{"join", "--invite", root + "/alice.json", "--device", "member-device"})
	if err != nil {
		t.Fatal(err)
	}
	if !B(joined, "joined") || S(joined, "actor") != "alice" {
		t.Fatalf("unexpected join result: %#v", joined)
	}

	profile, err := Load(S(joined, "profile"))
	if err != nil {
		t.Fatal(err)
	}
	client := NewClient(profile, nil, false)
	defer client.Transport.Close()
	fake := &fakeChannel{}
	bridge, err := NewChannelBridge(fake, client, Object{"actor": "alice", "session": "member-session", "channel": "demo-general"}, func(Object) string { return "captain-chat" })
	if err != nil {
		t.Fatal(err)
	}
	if _, err = bridge.IngestInbound(ctx, Inbound{EnvelopeID: "fake-team-1", Body: "local team message", SenderUsername: "captain", Route: "member-session"}); err != nil {
		t.Fatal(err)
	}
	items, err := ListInbox("demo-general")
	if err != nil || len(items) != 1 || S(items[0], "uid") != "fake-team-1" || !B(items[0], "received") {
		t.Fatalf("local team FakeChannel message was not persisted: %#v %v", items, err)
	}
}
