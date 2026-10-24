package otoha

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ChannelBridge connects one transport to the authoritative collaboration
// service. Server events are sent through the channel; channel envelopes are
// accepted by the service and then pass through the normal local inbox path.
// The bridge does not treat a channel delivery as an application read.
type ChannelBridge struct {
	Channel    Channel
	Client     *Client
	Binding    Object
	CursorPath string
	Recipient  func(Object) string
}

func NewChannelBridge(channel Channel, client *Client, binding Object, recipient func(Object) string) (*ChannelBridge, error) {
	if channel == nil || client == nil {
		return nil, fmt.Errorf("channel and collaboration client are required")
	}
	if S(binding, "channel") == "" || S(binding, "session") == "" {
		return nil, fmt.Errorf("bridge binding requires channel and session")
	}
	if recipient == nil {
		recipient = func(Object) string { return "" }
	}
	return &ChannelBridge{
		Channel: channel, Client: client, Binding: binding, Recipient: recipient,
		CursorPath: filepath.Join(Home(), "transport-cursors", inboxKey(channel.Name())+"-"+inboxKey(S(binding, "session"))+".json"),
	}, nil
}

// IngestInbound accepts a channel envelope into the authoritative service.
// The authenticated local actor owns the write; the external sender remains
// transport metadata and never becomes an authorization claim.
func (b *ChannelBridge) IngestInbound(ctx context.Context, inbound Inbound) (Object, error) {
	if inbound.EnvelopeID == "" {
		return nil, fmt.Errorf("transport envelope id required")
	}
	text := inbound.Body
	if text == "" {
		text = inbound.Text
	}
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("transport envelope body required")
	}
	session := S(b.Binding, "session")
	if inbound.Route != "" {
		session = inbound.Route
	}
	summary := strings.TrimSpace(strings.SplitN(strings.ReplaceAll(text, "\r", " "), "\n", 2)[0])
	if len([]rune(summary)) > 280 {
		summary = string([]rune(summary)[:280])
	}
	clientID := "transport-" + inbound.EnvelopeID
	payload := Object{
		"channel": S(b.Binding, "channel"), "session": session, "client_id": clientID,
		"uid": inbound.EnvelopeID, "summary": summary, "body": text, "text": text,
		"class": "queued", "kind": "message", "delivery": "both", "source": "transport",
		"agent": "channel/" + b.Channel.Name(),
	}
	if inbound.SenderUsername != "" {
		payload["task"] = "external_sender=" + inbound.SenderUsername
	}
	serverResult, err := b.Client.Call(ctx, "POST", "/v1/messages", payload)
	if err != nil {
		return nil, err
	}
	// The authoritative event is then consumed through the same inbox path as
	// server-originated messages. This preserves one deduplication and receipt
	// implementation for both transports.
	inboxResult, inboxErr := InboxCheck(ctx, b.Client, Object{"actor": b.Binding["actor"], "session": session, "channel": S(b.Binding, "channel")}, 0)
	if inboxErr != nil {
		return nil, inboxErr
	}
	return Object{"server": serverResult, "inbox": inboxResult}, nil
}

// ForwardServerEvents sends visible server messages through this transport.
// A successful channel send is followed by a durable transport cursor update;
// the server message remains the source of truth for the body and uid.
func (b *ChannelBridge) ForwardServerEvents(ctx context.Context, wait int) (Object, error) {
	state, err := b.loadCursor()
	if err != nil {
		return nil, err
	}
	after := N(state, "cursor")
	if wait < 0 || wait > 45 {
		return nil, fmt.Errorf("wait must be between 0 and 45 seconds")
	}
	page, err := b.Client.Call(ctx, "GET", "/v1/events?channel="+urlQuery(S(b.Binding, "channel"))+"&after="+strconv.Itoa(after)+"&wait="+strconv.Itoa(wait), nil)
	if err != nil {
		return nil, err
	}
	if err = validateEventPage(page, after); err != nil {
		return nil, err
	}
	sent := 0
	for _, event := range Items(page, "events") {
		if S(event, "kind") == "message.created" {
			message := O(event, "payload")
			payload := O(message, "payload")
			recipient := b.Recipient(message)
			if recipient != "" && S(message, "actor") != S(b.Binding, "actor") {
				if _, err = b.Channel.Send(ctx, Outbound{ID: S(payload, "uid"), Recipient: recipient, Text: S(payload, "body"), Kind: S(payload, "kind")}); err != nil {
					return nil, err
				}
				sent++
			}
		}
		state["cursor"] = N(event, "id")
		if err = Save(b.CursorPath, state); err != nil {
			return nil, err
		}
	}
	state["cursor"] = N(page, "cursor")
	if err = Save(b.CursorPath, state); err != nil {
		return nil, err
	}
	return Object{"sent": sent, "cursor": N(state, "cursor"), "has_more": B(page, "has_more")}, nil
}

// Run starts both directions. The first failing direction cancels the other
// through the caller's context; production service supervision restarts it.
func (b *ChannelBridge) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	errs := make(chan error, 2)
	go func() {
		errs <- b.Channel.Receive(ctx, func(inbound Inbound) error {
			_, err := b.IngestInbound(ctx, inbound)
			return err
		})
	}()
	go func() {
		for {
			if _, err := b.ForwardServerEvents(ctx, 10); err != nil {
				errs <- err
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(250 * time.Millisecond):
			}
		}
	}()
	err := <-errs
	cancel()
	return err
}

func (b *ChannelBridge) loadCursor() (Object, error) {
	state, err := Load(b.CursorPath)
	if os.IsNotExist(err) {
		return Object{"cursor": 0}, nil
	}
	if err != nil {
		return nil, err
	}
	return state, nil
}

func urlQuery(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "%", "%25"), " ", "%20")
}
