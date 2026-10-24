package otoha

// Desktop's local IPC adapter is isolated from the Otoha protocol. It uses the
// installed application's owner discovery and existing-turn steering; no conversation
// is recreated, model setting changed, or public runtime port opened.
import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type Desktop struct {
	conn   net.Conn
	client string
	owner  string
	ctx    context.Context
}

func desktopPath() string {
	if h := os.Getenv("CODEX_HOME"); h != "" {
		return filepath.Join(h, "ipc", "ipc.sock")
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".codex", "ipc", "ipc.sock")
}
func OpenDesktop(ctx context.Context, thread string) (*Desktop, error) {
	conn, err := dialDesktop(ctx, desktopPath())
	if err != nil {
		return nil, err
	}
	d := &Desktop{conn: conn, ctx: ctx}
	r, err := d.call("initialize", Object{"clientType": "otoha-cli"}, 0)
	if err != nil {
		conn.Close()
		return nil, err
	}
	d.client = S(r, "clientId")
	r, err = d.call("thread-owner-discovery", Object{"hostId": "local", "conversationId": thread}, 1)
	if err != nil {
		conn.Close()
		return nil, err
	}
	return d, nil
}
func (d *Desktop) Close() { d.conn.Close() }
func (d *Desktop) call(method string, params Object, version int) (Object, error) {
	id := ID()
	q := Object{"type": "request", "requestId": id, "method": method, "params": params, "version": version, "timeoutMs": 8000}
	if d.client != "" {
		q["sourceClientId"] = d.client
	}
	if d.owner != "" && method != "initialize" {
		q["targetClientId"] = d.owner
	}
	b, err := json.Marshal(q)
	if err != nil {
		return nil, err
	}
	if len(b) > 1<<20 {
		return nil, fmt.Errorf("IPC request too large")
	}
	deadline := time.Now().Add(10 * time.Second)
	if d.ctx != nil {
		if limit, ok := d.ctx.Deadline(); ok && limit.Before(deadline) {
			deadline = limit
		}
	}
	d.conn.SetDeadline(deadline)
	header := make([]byte, 4)
	binary.LittleEndian.PutUint32(header, uint32(len(b)))
	if _, err = d.conn.Write(append(header, b...)); err != nil {
		return nil, err
	}
	for {
		if _, err = io.ReadFull(d.conn, header); err != nil {
			return nil, err
		}
		n := binary.LittleEndian.Uint32(header)
		if n == 0 || n > 4<<20 {
			return nil, fmt.Errorf("invalid Desktop IPC frame")
		}
		b = make([]byte, n)
		if _, err = io.ReadFull(d.conn, b); err != nil {
			return nil, err
		}
		var r Object
		if err = json.Unmarshal(b, &r); err != nil {
			return nil, err
		}
		if S(r, "requestId") != id {
			continue
		}
		if S(r, "resultType") != "success" {
			return nil, fmt.Errorf("Desktop %s: %s", method, S(r, "error"))
		}
		if method == "thread-owner-discovery" {
			d.owner = S(r, "handledByClientId")
		}
		return O(r, "result"), nil
	}
}
func (d *Desktop) Settings(thread string) (Object, error) {
	return d.call("thread-follower-read-model-settings", Object{"conversationId": thread}, 1)
}

var ErrDesktopIdle = errors.New("Desktop has no active turn to steer")

func knownIdleRejection(message string) bool {
	return strings.Contains(message, "no active turn to steer") || strings.Contains(message, "is not being streamed") || strings.Contains(message, "because its active turn already ended")
}

// This is Desktop's existing steer operation. It updates the current turn;
// it never submits turn/start or creates an optimistic new turn.
func (d *Desktop) Steer(thread, text string, messageID int) (Object, error) {
	h := sha256.Sum256([]byte("otoha/" + thread + "/" + fmt.Sprint(messageID)))
	id := hex.EncodeToString(h[:16])
	clientID := id[:8] + "-" + id[8:12] + "-" + id[12:16] + "-" + id[16:20] + "-" + id[20:]
	return d.SteerWithID(thread, text, clientID)
}

// Existing receiver owners can reuse their already-persisted wake identity.
func (d *Desktop) SteerWithID(thread, text, clientID string) (Object, error) {
	if !threadRE.MatchString(clientID) {
		return nil, fmt.Errorf("persisted client message UUID required")
	}
	r, err := d.call("thread-follower-steer-turn", Object{
		"conversationId": thread, "clientUserMessageId": clientID,
		"input":          []Object{{"type": "text", "text": text, "text_elements": []any{}}},
		"restoreMessage": Object{"context": Object{}},
	}, 1)
	if err != nil {
		message := err.Error()
		if knownIdleRejection(message) {
			return nil, ErrDesktopIdle
		}
		return nil, err
	}
	if S(O(r, "result"), "turnId") == "" {
		return nil, fmt.Errorf("Desktop steer did not confirm a turn ID")
	}
	return Object{"transport": "desktop-steer", "result": r, "agent_read": false}, nil
}

// BusyAdmit performs one local admission attempt. It never polls Otoha, saves
// an identity, starts a receiver, or creates an idle queue item. The original
// receiver owns its ledger and retains its existing idle queue policy.
func BusyAdmit(ctx context.Context, thread, sourceThread, requestID, text string) (Object, error) {
	if !threadRE.MatchString(thread) || !threadRE.MatchString(requestID) || (sourceThread != "" && !threadRE.MatchString(sourceThread)) {
		return nil, fmt.Errorf("target thread, persisted request ID and optional source thread must be UUIDs")
	}
	if len(text) == 0 || len(text) > 65536 {
		return nil, fmt.Errorf("admission text must contain 1–65536 bytes")
	}
	result := Object{"request_id": requestID, "thread": thread, "admitted": nil, "agent_read": false, "safe_for_idle_queue": false}
	desktop, err := OpenDesktop(ctx, thread)
	if err != nil {
		result["status"] = "unavailable"
		result["admitted"] = false
		result["safe_for_idle_queue"] = true
		return result, fmt.Errorf("Desktop admission not submitted: %w", err)
	}
	defer desktop.Close()
	accepted, err := desktop.SteerWithID(thread, DeliveryText(sourceThread, text), requestID)
	if errors.Is(err, ErrDesktopIdle) {
		result["status"] = "idle"
		result["admitted"] = false
		result["safe_for_idle_queue"] = true
		return result, nil
	}
	if err != nil {
		result["status"] = "uncertain"
		return result, fmt.Errorf("Desktop admission outcome uncertain; reconcile the original receiver journal before retrying: %w", err)
	}
	result["status"] = "admitted"
	result["admitted"] = true
	result["runtime_result"] = accepted
	return result, nil
}

func DeliveryText(sourceThread, text string) string {
	if threadRE.MatchString(sourceThread) {
		escaped := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(text)
		return "<codex_delegation>\n<source_thread_id>" + sourceThread + "</source_thread_id>\n<input>" + escaped + "</input>\n</codex_delegation>"
	}
	return text
}

func AdmitCodex(ctx context.Context, codex, thread, sourceThread, text string, messageID int) (Object, error) {
	if threadRE.MatchString(thread) {
		if desktop, err := OpenDesktop(ctx, thread); err == nil {
			result, err := desktop.Steer(thread, DeliveryText(sourceThread, text), messageID)
			desktop.Close()
			if err == nil {
				return result, nil
			}
			if !errors.Is(err, ErrDesktopIdle) {
				return nil, err
			}
		}
	}
	return QueueCodex(ctx, codex, thread, sourceThread, text)
}
func QueueCodex(ctx context.Context, codex, thread, sourceThread, text string) (Object, error) {
	// Raw Desktop start can leave an optimistic turn without an ID when a
	// running turn is returned. The supported queue lets Desktop manage its
	// own turn transitions. Unknown outcomes are stopped by the delivery journal.
	text = DeliveryText(sourceThread, text)
	output, err := exec.CommandContext(ctx, codex, "queue", "--thread", thread, "--message", text).Output()
	if err != nil {
		return nil, fmt.Errorf("Codex queue failed: %w", err)
	}
	h := sha256.Sum256(output)
	return Object{"transport": "codex-queue", "queue_output_sha256": hex.EncodeToString(h[:]), "agent_read": false}, nil
}
