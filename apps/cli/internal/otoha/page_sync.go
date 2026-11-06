package otoha

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"
)

func pagePause(ctx context.Context, delay time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(delay):
		return true
	}
}

func (m *pageManager) discoverTeams(ctx context.Context) {
	var centers sync.WaitGroup
	defer centers.Wait()
	for {
		paths, _ := filepath.Glob(filepath.Join(m.home, "teams", "*", "team.json"))
		for _, path := range paths {
			name := filepath.Base(filepath.Dir(path))
			if err := Name(name); err != nil {
				continue
			}
			m.mu.Lock()
			_, running := m.workers[name]
			if !running {
				worker := &pageWorker{wake: make(chan struct{}, 1)}
				m.workers[name] = worker
				centers.Add(1)
				go func(team string) {
					defer centers.Done()
					m.superviseLocalCenter(ctx, team)
				}(name)
				centers.Add(1)
				go func(team string) {
					defer centers.Done()
					m.superviseInitRuntime(ctx, team)
				}(name)
				go m.pollTeam(ctx, name)
				go m.flushTeam(ctx, name, worker.wake)
			}
			m.mu.Unlock()
		}
		if !pagePause(ctx, 2*time.Second) {
			return
		}
	}
}

func (m *pageManager) connectionError(team string, err error) {
	_ = m.changeState(team, func(state Object) error {
		state["connection"], state["last_error"], state["last_error_at"] = "offline", err.Error(), pageTime()
		return nil
	})
}

func (m *pageManager) pollTeam(ctx context.Context, team string) {
	backoff := time.Second
	for ctx.Err() == nil {
		cfg, err := loadTeamConfig(team)
		if err != nil {
			m.connectionError(team, err)
			if !pagePause(ctx, backoff) {
				return
			}
			continue
		}
		_, client, err := teamProfile(cfg)
		if err != nil {
			m.connectionError(team, err)
			if !pagePause(ctx, backoff) {
				return
			}
			continue
		}
		for ctx.Err() == nil {
			m.mu.Lock()
			state, stateErr := m.stateLocked(team)
			after := N(state, "cursor")
			wait := "10"
			if S(state, "connection") != "online" {
				// Confirm authorization and the current cursor immediately on
				// first connect/reconnect; an empty healthy channel is ready too.
				wait = "0"
			}
			m.mu.Unlock()
			if stateErr != nil {
				err = stateErr
				break
			}
			poll, cancel := context.WithTimeout(ctx, 15*time.Second)
			var page Object
			page, err = client.Call(poll, http.MethodGet, "/v1/events?channel="+url.QueryEscape(cfg.Channel)+"&after="+fmt.Sprint(after)+"&wait="+wait, nil)
			cancel()
			if err == nil {
				err = validateEventPage(page, after)
			}
			if err == nil {
				err = m.acceptEvents(team, cfg.Channel, page)
			}
			if err != nil {
				break
			}
			backoff = time.Second
		}
		client.Transport.Close()
		if ctx.Err() != nil {
			return
		}
		m.connectionError(team, err)
		if !pagePause(ctx, backoff) {
			return
		}
		if backoff < 8*time.Second {
			backoff *= 2
		}
	}
}

func (m *pageManager) acceptEvents(team, channel string, page Object) error {
	return m.changeState(team, func(state Object) error {
		if err := validateEventPage(page, N(state, "cursor")); err != nil {
			return err
		}
		for _, event := range Items(page, "events") {
			if S(event, "kind") != "message.created" {
				continue
			}
			message := O(event, "payload")
			if S(message, "channel") != channel {
				continue
			}
			applyChatMessage(state, message)
			payload := O(message, "payload")
			var content Object
			if json.Unmarshal([]byte(S(payload, "body")), &content) != nil || S(content, "schema") != "otoha-page/1" {
				continue
			}
			id := S(message, "client_id")
			if pageRequestID(id) != nil {
				continue
			}
			switch S(content, "operation") {
			case "publish":
				document, err := pageDocument(O(content, "document"))
				if err != nil {
					continue
				}
				state["page"] = Object{"request_id": id, "message_id": N(message, "id"), "event_id": N(event, "id"), "actor": message["actor"], "source": payload["source"], "document": document}
			case "feedback":
				text, pageID := S(content, "text"), S(content, "page_request_id")
				pageMessageID, validPageMessageID := eventInteger(content["page_message_id"])
				if text == "" || len(text) > 8000 || pageRequestID(pageID) != nil || !validPageMessageID || pageMessageID <= 0 {
					continue
				}
				feedback := Array(state["feedback"])
				// Accepted message identities distinguish two actors using the
				// same client request ID. They are scoped to this team's
				// configured authority, not globally across servers.
				feedback = append(feedback, Object{"request_id": id, "page_request_id": pageID, "page_message_id": pageMessageID, "text": text, "source": payload["source"], "actor": message["actor"], "message_id": N(message, "id"), "event_id": N(event, "id"), "created": message["created"]})
				if len(feedback) > 100 {
					feedback = feedback[len(feedback)-100:]
					state["feedback_truncated"] = true
				}
				state["feedback"] = feedback
			}
		}
		state["cursor"], state["connection"], state["last_contact"] = N(page, "cursor"), "online", pageTime()
		delete(state, "last_error")
		return nil
	})
}

func (m *pageManager) pending(team string) ([]Object, error) {
	paths, err := filepath.Glob(filepath.Join(m.home, "pages", "outbox", team, "*.json"))
	if err != nil {
		return nil, err
	}
	result := []Object{}
	for _, path := range paths {
		op, err := Load(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if S(op, "state") == "queued" {
			result = append(result, op)
		}
	}
	sortPageOperations(result)
	return result, nil
}

func pageReceiptMessage(receipt Object) Object {
	if nested := O(receipt, "message"); len(nested) > 0 {
		return nested
	}
	return receipt
}

func samePageSubmission(message, body Object) bool {
	payload := O(message, "payload")
	return S(message, "channel") == S(body, "channel") && S(message, "client_id") == S(body, "client_id") &&
		S(message, "session") == S(body, "session") && S(payload, "body") == S(body, "body") &&
		S(payload, "source") == S(body, "source") && S(payload, "kind") == S(body, "kind")
}

func pagePermanentError(err error) bool {
	var api *APIError
	return errors.As(err, &api) && api.Status >= 400 && api.Status < 500 && api.Status != 408 && api.Status != 429
}

func (m *pageManager) submitOperation(ctx context.Context, team string, client *Client, op Object) error {
	// A receipt-write failure must leave the caller's retryable operation
	// queued, even though the remote acceptance may already be known.
	op = Clone(op)
	id, body := S(op, "request_id"), O(op, "body")
	// The immutable client ID is checked before a retry. It is also the
	// authority's unique (actor,client_id), including after a lost response.
	receipt, err := client.Call(ctx, http.MethodGet, "/v1/messages/by-client-id?client_id="+url.QueryEscape(id), nil)
	if err == nil {
		message := pageReceiptMessage(receipt)
		if !samePageSubmission(message, body) {
			return &APIError{Status: 409, Code: "page_request_id_conflict"}
		}
		op["message_id"], op["duplicate"] = N(message, "id"), true
	} else if IsStatus(err, http.StatusNotFound) {
		receipt, err = client.Call(ctx, http.MethodPost, "/v1/messages", body)
		if err != nil {
			return err
		}
		if N(receipt, "message_id") <= 0 {
			return errors.New("message acceptance response lacked an identity; original request retained")
		}
		op["message_id"], op["duplicate"] = N(receipt, "message_id"), B(receipt, "duplicate")
	} else {
		return err
	}
	op["state"], op["accepted_at"] = "accepted", pageTime()
	delete(op, "last_error")
	// Save a durable receipt before retiring the pending entry. A crash in
	// between only causes a safe lookup of the same authority receipt.
	if err = Save(m.receiptPath(team, id), op); err != nil {
		return err
	}
	if err = os.Remove(m.pendingPath(team, id)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (m *pageManager) flushTeam(ctx context.Context, team string, wake <-chan struct{}) {
	backoff := time.Second
	var client *Client
	var identity string
	closeClient := func() {
		if client != nil {
			client.Transport.Close()
			client = nil
		}
		identity = ""
	}
	defer closeClient()
	for ctx.Err() == nil {
		operations, err := m.pending(team)
		if err == nil && len(operations) == 0 {
			backoff = time.Second
			select {
			case <-ctx.Done():
				return
			case <-wake:
				continue
			}
		}
		if err != nil {
			m.connectionError(team, err)
			if !pagePause(ctx, backoff) {
				return
			}
			continue
		}
		cfg, err := loadTeamConfig(team)
		if err == nil {
			var profile Object
			var candidate *Client
			profile, candidate, err = teamProfile(cfg)
			if err == nil {
				// Re-read configuration and identity before each batch, while
				// retaining the healthy SSH/HTTP pool across sparse sends.
				// Replacing referenced key/CA files at the same path requires a
				// daemon restart; this is not a filesystem watcher.
				var data []byte
				data, err = json.Marshal(struct {
					Config  TeamConfig
					Profile Object
				}{cfg, profile})
				if err != nil || client != nil && identity == string(data) {
					candidate.Transport.Close()
				} else {
					closeClient()
					client, identity = candidate, string(data)
				}
			}
		}
		if err != nil {
			// A missing or invalid identity must not keep using cached access.
			closeClient()
			m.connectionError(team, err)
			if !pagePause(ctx, backoff) {
				return
			}
			continue
		}
		failed := false
		for _, op := range operations {
			send, cancel := context.WithTimeout(ctx, 8*time.Second)
			err = m.submitOperation(send, team, client, op)
			cancel()
			if err != nil {
				op["last_error"] = err.Error()
				if pagePermanentError(err) {
					op["state"] = "needs_attention"
				}
				if saveErr := Save(m.pendingPath(team, S(op, "request_id")), op); saveErr != nil {
					m.connectionError(team, saveErr)
					failed = true
					break
				}
				if !pagePermanentError(err) {
					m.connectionError(team, err)
					failed = true
					break
				}
			} else {
				backoff = time.Second
			}
		}
		if failed {
			closeClient()
			if !pagePause(ctx, backoff) {
				return
			}
			if backoff < 8*time.Second {
				backoff *= 2
			}
		}
	}
}
