package otoha

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var inboxKeyRE = regexp.MustCompile(`^[A-Za-z0-9_-]{1,160}$`)

// PersistInboxMessage makes the local copy durable before the event cursor
// advances. The precise server receipt is retried when the local record says
// it has not been acknowledged yet.
func PersistInboxMessage(ctx context.Context, c *Client, binding Object, message Object) (Object, error) {
	return persistInboxMessage(ctx, c, binding, message, true)
}

// PersistInboxMessageLocal is the compatibility path for bindings created
// before per-message receipts existed. It still persists and deduplicates the
// message; an explicit inbox check/read can add the receipt later.
func PersistInboxMessageLocal(ctx context.Context, c *Client, binding Object, message Object) (Object, error) {
	return persistInboxMessage(ctx, c, binding, message, false)
}

func persistInboxMessage(ctx context.Context, c *Client, binding Object, message Object, sendReceipt bool) (Object, error) {
	payload := O(message, "payload")
	messageID := N(message, "id")
	if messageID < 1 || S(message, "channel") == "" || S(payload, "text") == "" {
		return nil, fmt.Errorf("invalid message event")
	}
	uid := S(payload, "uid")
	if uid == "" {
		uid = S(payload, "client_id")
	}
	if uid == "" {
		uid = strconv.Itoa(messageID)
	}
	key := inboxKey(uid)
	path := filepath.Join(Home(), "inbox", key+".json")
	record, err := Load(path)
	if os.IsNotExist(err) {
		record = Object{
			"uid": uid, "message_id": messageID, "channel": S(message, "channel"),
			"message": Clone(message), "received": false, "read": false,
			"stored_at": time.Now().UTC().Format(time.RFC3339),
		}
		if err = Save(path, record); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	} else if N(record, "message_id") != messageID || S(record, "uid") != uid {
		return nil, fmt.Errorf("local inbox key collision for message %d", messageID)
	}
	if !sendReceipt || B(record, "received") {
		return record, nil
	}
	if !B(record, "received") {
		result, receiptErr := c.Call(ctx, "POST", "/v1/receipts", Object{
			"channel": S(message, "channel"), "session": S(binding, "session"),
			"message_id": messageID, "received": true,
		})
		if receiptErr != nil {
			record["receipt_pending"] = true
			record["receipt_error"] = receiptErr.Error()
			if saveErr := Save(path, record); saveErr != nil {
				return nil, saveErr
			}
			return record, nil
		}
		record["received"] = true
		delete(record, "receipt_pending")
		delete(record, "receipt_error")
		record["received_at"] = time.Now().UTC().Format(time.RFC3339)
		record["receipt"] = result
		if err = Save(path, record); err != nil {
			return nil, err
		}
	}
	return record, nil
}

// InboxRead returns a durable local message and records the application-level
// read receipt only after the caller has selected that message.
func InboxRead(ctx context.Context, c *Client, binding Object, key string) (Object, error) {
	path, record, err := findInbox(key)
	if err != nil {
		return nil, err
	}
	message := O(record, "message")
	messageID := N(record, "message_id")
	result, err := c.Call(ctx, "POST", "/v1/receipts", Object{
		"channel": S(record, "channel"), "session": S(binding, "session"),
		"message_id": messageID, "read": true,
	})
	if err != nil {
		return nil, fmt.Errorf("message is local but read receipt is uncertain: %w", err)
	}
	record["received"] = true
	record["read"] = true
	record["read_at"] = time.Now().UTC().Format(time.RFC3339)
	record["receipt"] = result
	if err = Save(path, record); err != nil {
		return nil, err
	}
	message["local"] = Object{"uid": S(record, "uid"), "received": B(record, "received"), "read": B(record, "read")}
	return message, nil
}

func ListInbox(channel string) ([]Object, error) {
	entries, err := os.ReadDir(filepath.Join(Home(), "inbox"))
	if os.IsNotExist(err) {
		return []Object{}, nil
	}
	if err != nil {
		return nil, err
	}
	var records []Object
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		record, e := Load(filepath.Join(Home(), "inbox", entry.Name()))
		if e != nil {
			return nil, e
		}
		if channel != "" && S(record, "channel") != channel {
			continue
		}
		record["message"] = O(record, "message")
		record["uid"] = S(record, "uid")
		record["message_id"] = N(record, "message_id")
		record["channel"] = S(record, "channel")
		delete(record, "receipt")
		records = append(records, record)
	}
	sort.Slice(records, func(i, j int) bool { return N(records[i], "message_id") < N(records[j], "message_id") })
	return records, nil
}

// InboxCheck polls one channel and persists every visible message event. It
// uses a separate durable cursor so a one-shot CLI check and the background
// receiver cannot overwrite each other's progress.
func InboxCheck(ctx context.Context, c *Client, binding Object, wait int) (Object, error) {
	if err := PrivateDir(Home()); err != nil {
		return nil, err
	}
	cursorPath := filepath.Join(Home(), "inbox-cursors", inboxKey(S(binding, "session"))+"-"+inboxKey(S(binding, "channel"))+".json")
	state, err := Load(cursorPath)
	if os.IsNotExist(err) {
		state = Object{"cursor": 0}
	} else if err != nil {
		return nil, err
	}
	after := N(state, "cursor")
	if wait < 0 || wait > 45 {
		return nil, fmt.Errorf("wait must be between 0 and 45 seconds")
	}
	page, err := c.Call(ctx, "GET", "/v1/events?channel="+url.QueryEscape(S(binding, "channel"))+"&after="+strconv.Itoa(after)+"&wait="+strconv.Itoa(wait), nil)
	if err != nil {
		return nil, err
	}
	if err = validateEventPage(page, after); err != nil {
		return nil, err
	}
	stored := 0
	for _, event := range Items(page, "events") {
		if S(event, "kind") == "message.created" {
			if _, err = PersistInboxMessage(ctx, c, binding, O(event, "payload")); err != nil {
				return nil, err
			}
			stored++
		}
		state["cursor"] = N(event, "id")
		if err = Save(cursorPath, state); err != nil {
			return nil, err
		}
	}
	state["cursor"] = N(page, "cursor")
	if err = Save(cursorPath, state); err != nil {
		return nil, err
	}
	return Object{"stored": stored, "cursor": N(state, "cursor"), "has_more": B(page, "has_more")}, nil
}

func inboxKey(value string) string {
	if inboxKeyRE.MatchString(value) {
		return value
	}
	digest := sha256.Sum256([]byte(value))
	return "sha256-" + hex.EncodeToString(digest[:])
}

func findInbox(key string) (string, Object, error) {
	if key == "" {
		return "", nil, fmt.Errorf("message uid or id required")
	}
	if inboxKeyRE.MatchString(key) {
		if record, err := Load(filepath.Join(Home(), "inbox", key+".json")); err == nil {
			return filepath.Join(Home(), "inbox", key+".json"), record, nil
		}
	}
	entries, err := os.ReadDir(filepath.Join(Home(), "inbox"))
	if os.IsNotExist(err) {
		return "", nil, fmt.Errorf("message %s not found", key)
	}
	if err != nil {
		return "", nil, err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(Home(), "inbox", entry.Name())
		record, e := Load(path)
		if e != nil {
			return "", nil, e
		}
		if S(record, "uid") == key || strconv.Itoa(N(record, "message_id")) == key {
			return path, record, nil
		}
	}
	return "", nil, fmt.Errorf("message %s not found", key)
}
