package otoha

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"testing"
)

func TestSteerUsesExistingTurnAndClassifiesIdle(t *testing.T) {
	for _, outcome := range []string{"accepted", "idle", "idle-ended", "uncertain"} {
		t.Run(outcome, func(t *testing.T) {
			client, server := net.Pipe()
			defer client.Close()
			defer server.Close()
			go func() {
				var h [4]byte
				if _, e := io.ReadFull(server, h[:]); e != nil {
					return
				}
				b := make([]byte, binary.LittleEndian.Uint32(h[:]))
				if _, e := io.ReadFull(server, b); e != nil {
					return
				}
				var q Object
				json.Unmarshal(b, &q)
				if S(q, "method") != "thread-follower-steer-turn" {
					t.Error("delivery attempted a new turn")
				}
				params := O(q, "params")
				if S(params, "clientUserMessageId") == "" {
					t.Error("missing stable message ID")
				}
				r := Object{"requestId": S(q, "requestId"), "resultType": "success", "result": Object{"result": Object{"turnId": "existing-turn"}}}
				if outcome != "accepted" {
					r["resultType"] = "error"
					r["error"] = "timeout; outcome unknown"
					if outcome == "idle" {
						r["error"] = "Conversation target is not being streamed."
					}
					if outcome == "idle-ended" {
						r["error"] = "Cannot steer conversation target because its active turn already ended"
					}
				}
				b, _ = json.Marshal(r)
				binary.LittleEndian.PutUint32(h[:], uint32(len(b)))
				server.Write(append(h[:], b...))
			}()
			d := &Desktop{conn: client}
			r, e := d.Steer("target", "probe", 42)
			switch outcome {
			case "accepted":
				if e != nil || S(r, "transport") != "desktop-steer" {
					t.Fatal(e)
				}
			case "idle", "idle-ended":
				if !errors.Is(e, ErrDesktopIdle) {
					t.Fatal(e)
				}
			case "uncertain":
				if e == nil || errors.Is(e, ErrDesktopIdle) {
					t.Fatal("uncertain submission may not fall back to queue")
				}
			}
		})
	}
}
