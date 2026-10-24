package otoha

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSteerRetainsOriginalReceiverRequestIdentity(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	requestID := "01a135e4-9094-7dd0-b7d1-c2e57d6d4934"
	text := "原接收器未读提醒\n原文和游标仍由原接收器管理。"
	checked := make(chan Object, 1)
	go func() {
		var header [4]byte
		if _, err := io.ReadFull(server, header[:]); err != nil {
			return
		}
		data := make([]byte, binary.LittleEndian.Uint32(header[:]))
		if _, err := io.ReadFull(server, data); err != nil {
			return
		}
		var request Object
		json.Unmarshal(data, &request)
		checked <- request
		response, _ := json.Marshal(Object{"requestId": request["requestId"], "resultType": "success", "result": Object{"result": Object{"turnId": "existing-turn"}}})
		binary.LittleEndian.PutUint32(header[:], uint32(len(response)))
		server.Write(append(header[:], response...))
	}()
	desktop := &Desktop{conn: client}
	result, err := desktop.SteerWithID("target", text, requestID)
	if err != nil || S(result, "transport") != "desktop-steer" || B(result, "agent_read") {
		t.Fatal("existing-turn admission failed or became an actual read", err)
	}
	request := <-checked
	params := O(request, "params")
	if S(request, "method") != "thread-follower-steer-turn" || S(params, "clientUserMessageId") != requestID || S(Items(params, "input")[0], "text") != text {
		t.Fatal("original receiver identity or text changed, or a different admission operation was used")
	}
}

func TestDesktopOperationRespectsOverallDeadline(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	desktop := &Desktop{conn: client, ctx: ctx}
	started := time.Now()
	if _, err := desktop.SteerWithID("target", "deadline probe", "01a135e4-9094-7dd0-b7d1-c2e57d6d4934"); err == nil {
		t.Fatal("unresponsive IPC did not time out")
	}
	if time.Since(started) > time.Second {
		t.Fatal("one IPC operation ignored the overall admission deadline")
	}
}

func TestBusyAdmissionRejectsInvalidIdentityBeforeCreatingState(t *testing.T) {
	private := filepath.Join(t.TempDir(), "unused-client-home")
	t.Setenv("OTOHA_HOME", private)
	if _, err := BusyAdmit(context.Background(), "target", "", "new-id", "text"); err == nil {
		t.Fatal("admission accepted an unpersisted/non-UUID identity")
	}
	if _, err := os.Stat(private); !os.IsNotExist(err) {
		t.Fatal("single admission created client state")
	}
}
