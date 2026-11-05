package otoha

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// enqueueMessage uses the same durable queue and authority receipt contract as
// page publication. Joining a team is sufficient; legacy config and chat-host
// bindings are not prerequisites for tool-based communication.
func (m *pageManager) enqueueMessage(team string, input Object) (Object, error) {
	if err := Name(team); err != nil {
		return nil, err
	}
	cfg, err := loadTeamConfig(team)
	if err != nil {
		return nil, err
	}
	text := S(input, "text")
	if strings.TrimSpace(text) == "" || !utf8.ValidString(text) {
		return nil, errors.New("message text must contain nonempty UTF-8 text")
	}
	id := S(input, "request_id")
	if id == "" {
		id = ID()
	}
	if err := pageRequestID(id); err != nil {
		return nil, fmt.Errorf("invalid request_id: %w", err)
	}
	summary := messageSummary(text)
	if value, explicit := input["summary"]; explicit {
		var ok bool
		summary, ok = value.(string)
		if !ok || !utf8.ValidString(summary) || strings.TrimSpace(summary) == "" || len(summary) > 1000 {
			return nil, errors.New("message summary must contain nonempty UTF-8 text within 1000 bytes")
		}
	}
	body := Object{"channel": cfg.Channel, "session": "otoha-cli", "client_id": id,
		"uid": id, "summary": summary, "text": text, "body": text,
		"kind": "message", "source": "agent", "agent": "otoha-cli",
		"class": "queued", "delivery": "tool"}
	encoded, err := json.Marshal(body)
	if err != nil || len(encoded) > 65536 {
		return nil, errors.New("encoded message exceeds the service's 65536 byte request limit; reduce the text")
	}
	op := Object{"request_id": id, "team": team, "operation": "message",
		"state": "queued", "created_at": pageTime(), "body": body}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, path := range []string{m.receiptPath(team, id), m.pendingPath(team, id)} {
		old, loadErr := Load(path)
		if loadErr == nil {
			a, _ := json.Marshal(O(old, "body"))
			b, _ := json.Marshal(body)
			if string(a) != string(b) {
				return nil, errors.New("request_id already belongs to a different operation")
			}
			return pageOperationSummary(old), nil
		}
		if !os.IsNotExist(loadErr) {
			return nil, loadErr
		}
	}
	if err := Save(m.pendingPath(team, id), op); err != nil {
		return nil, err
	}
	if worker := m.workers[team]; worker != nil {
		select {
		case worker.wake <- struct{}{}:
		default:
		}
	}
	result := pageOperationSummary(op)
	result["queued"] = true
	result["next"] = "otoha message status --team " + team + " --request-id " + id
	return result, nil
}

func messageSummary(text string) string {
	summary := strings.TrimSpace(strings.SplitN(strings.TrimSpace(text), "\n", 2)[0])
	runes := []rune(summary)
	if len(runes) > 280 {
		summary = string(runes[:280])
	}
	for len(summary) > 1000 {
		_, size := utf8.DecodeLastRuneInString(summary)
		summary = summary[:len(summary)-size]
	}
	return summary
}

// applyChatMessage is a bounded derived cache, not a second message authority.
// The caller has already checked channel scope and monotonic event progress.
func applyChatMessage(state Object, message Object) {
	payload := O(message, "payload")
	var artifact Object
	if json.Unmarshal([]byte(S(payload, "body")), &artifact) == nil && S(artifact, "schema") == "otoha-page/1" {
		return
	}
	id := N(message, "id")
	if id <= 0 {
		return
	}
	list := Array(state["messages"])
	for _, previous := range list {
		if N(messageObject(previous), "id") == id {
			return
		}
	}
	text := S(payload, "body")
	if _, exists := payload["body"]; !exists {
		text = S(payload, "text")
	}
	uid := S(payload, "uid")
	if uid == "" {
		uid = S(message, "client_id")
	}
	list = append(list, Object{"id": id, "message_id": id, "uid": uid,
		"client_id": message["client_id"], "actor": message["actor"],
		"source": payload["source"], "text": text, "summary": payload["summary"],
		"kind": payload["kind"], "session": message["session"], "created": message["created"]})
	if len(list) > 100 {
		list = list[len(list)-100:]
		state["messages_truncated"] = true
	}
	state["messages"] = list
}

func messageObject(value any) Object {
	switch row := value.(type) {
	case Object:
		return row
	case map[string]any:
		return Object(row)
	default:
		return nil
	}
}

func messageSummaries(team string, cached any) []any {
	messages := []any{}
	for _, value := range Array(cached) {
		message := messageObject(value)
		row := Object{}
		for _, key := range []string{"id", "message_id", "uid", "client_id", "actor", "source", "summary", "kind", "session", "created"} {
			if value, exists := message[key]; exists {
				row[key] = value
			}
		}
		if S(row, "summary") == "" {
			row["summary"] = messageSummary(S(message, "text"))
		}
		id := N(row, "message_id")
		row["ref"] = Object{"team": team, "message_id": id}
		row["get_command"] = fmt.Sprintf("otoha message get %d --team %s", id, team)
		messages = append(messages, row)
	}
	return messages
}

func (m *pageManager) messageSnapshot(team string) (Object, error) {
	state, err := m.snapshot(team)
	if err != nil {
		return nil, err
	}
	messages := messageSummaries(team, state["messages"])
	pending := []any{}
	for _, value := range Array(state["pending"]) {
		op := messageObject(value)
		if S(op, "operation") == "message" {
			pending = append(pending, op)
		}
	}
	return Object{"schema": 1, "team": team, "messages": messages, "content": "summaries",
		"messages_truncated": B(state, "messages_truncated"), "limit": 100,
		"pending": pending, "cursor": state["cursor"], "connection": state["connection"]}, nil
}

func messageID(value string) (int64, error) {
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != value {
		return 0, errors.New("message ID must be a positive decimal integer")
	}
	return id, nil
}

// Fetch from the authority even when the ID is outside the bounded local cache.
// Reusing current project credentials rechecks access; fetching never records a
// read receipt or implies the caller adopted the message's contents.
func (m *pageManager) messageByID(ctx context.Context, team, value string) (Object, error) {
	id, err := messageID(value)
	if err != nil {
		return nil, err
	}
	cfg, err := loadTeamConfig(team)
	if err != nil {
		return nil, err
	}
	_, client, err := teamProfile(cfg)
	if err != nil {
		return nil, err
	}
	defer client.Transport.Close()
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	result, err := client.Call(ctx, http.MethodGet, "/v1/messages/by-id?id="+strconv.FormatInt(id, 10)+"&channel="+url.QueryEscape(cfg.Channel), nil)
	if err != nil {
		var apiError *APIError
		if errors.As(err, &apiError) {
			return nil, apiError
		}
		return nil, &APIError{Status: http.StatusServiceUnavailable, Code: "message_authority_unavailable: " + err.Error()}
	}
	message := O(result, "message")
	if N(message, "id") != int(id) || S(message, "channel") != cfg.Channel {
		return nil, &APIError{Status: http.StatusBadGateway, Code: "message_authority_response_mismatch"}
	}
	return Object{"schema": 1, "team": team, "message": message,
		"ref": Object{"team": team, "message_id": id}, "source": "authority", "read_receipt_recorded": false}, nil
}

func messageCommandHelp() Object {
	return Object{"schema": 1, "commands": []string{
		"otoha message send --team TEAM --text TEXT [--summary SUMMARY] [--request-id ID]",
		"otoha message list --team TEAM",
		"otoha message get MESSAGE_ID --team TEAM",
		"otoha message status --team TEAM --request-id ID",
	}, "example": "otoha message send --team demo --text 'Ready for review'",
		"note": "List returns summaries and retrieval commands for the latest 100 cached messages. Get fetches the complete original from the authority, including older messages, under current access permissions; it does not record a read receipt. Queued, accepted and read are distinct."}
}

func MessageCommand(ctx context.Context, args []string) (Object, error) {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		return messageCommandHelp(), nil
	}
	switch args[0] {
	case "send", "list", "get", "status":
	default:
		return nil, fmt.Errorf("unknown message action %q; use otoha message help", args[0])
	}
	f := flag.NewFlagSet("message "+args[0], flag.ContinueOnError)
	f.SetOutput(io.Discard)
	team := f.String("team", "", "joined team")
	text := f.String("text", "", "complete message text")
	summary := f.String("summary", "", "short notification summary; otherwise derived from the first line")
	id := f.String("request-id", "", "stable operation ID")
	flags := args[1:]
	lookupID := ""
	if args[0] == "get" && len(flags) > 0 && !strings.HasPrefix(flags[0], "-") {
		lookupID, flags = flags[0], flags[1:]
	}
	if err := f.Parse(flags); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return messageCommandHelp(), nil
		}
		return nil, err
	}
	if args[0] == "get" && lookupID == "" && len(f.Args()) == 1 {
		lookupID = f.Args()[0]
	} else if len(f.Args()) != 0 {
		return nil, errors.New("unexpected message arguments; use otoha message help")
	}
	if args[0] == "send" && strings.TrimSpace(*text) == "" {
		return nil, errors.New("message send requires --text TEXT")
	}
	if args[0] == "status" && *id == "" {
		return nil, errors.New("message status requires --request-id ID")
	}
	if args[0] == "get" {
		if _, err := messageID(lookupID); err != nil {
			return nil, fmt.Errorf("message get requires MESSAGE_ID: %w", err)
		}
	}
	if *team == "" {
		name, err := discoverTeamName()
		if err != nil {
			return nil, err
		}
		*team = name
	}
	if _, err := EnsurePageService(ctx, *team); err != nil {
		return nil, fmt.Errorf("local Otoha service is not ready; use otoha daemon status: %w", err)
	}
	switch args[0] {
	case "send":
		input := Object{"team": *team, "request_id": *id, "text": *text}
		f.Visit(func(value *flag.Flag) {
			if value.Name == "summary" {
				input["summary"] = *summary
			}
		})
		return pageLocalCall(ctx, http.MethodPost, "/api/messages", input)
	case "list":
		result, err := pageLocalCall(ctx, http.MethodGet, "/api/messages?team="+url.QueryEscape(*team), nil)
		if err == nil {
			// A newer CLI may temporarily talk to an older installed daemon.
			// Keep the attention boundary even when its API returns full rows.
			result["messages"] = messageSummaries(*team, result["messages"])
			result["content"] = "summaries"
		}
		return result, err
	case "get":
		return pageLocalCall(ctx, http.MethodGet, "/api/messages/by-id?team="+url.QueryEscape(*team)+"&id="+url.QueryEscape(lookupID), nil)
	default:
		return pageLocalCall(ctx, http.MethodGet, "/api/operations?team="+url.QueryEscape(*team)+"&id="+url.QueryEscape(*id), nil)
	}
}
