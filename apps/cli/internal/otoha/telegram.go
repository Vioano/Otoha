package otoha

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Channel is the transport boundary used by Otoha. Adapters move messages;
// routing, authorization and envelope policy stay above this layer.
type Channel interface {
	Name() string
	Capabilities() Capabilities
	Connect(context.Context) error
	Close() error
	Send(context.Context, Outbound) (SendResult, error)
	Receive(context.Context, func(Inbound) error) error
}

type Capabilities struct {
	DirectToMember  bool
	PrivateChat     bool
	Broadcast       bool
	Retention       time.Duration
	History         bool
	MaxTextBytes    int
	DeliveryReceipt bool
}

type Outbound struct {
	ID        string
	ChatID    string
	Recipient string
	Text      string
	Kind      string
}

type SendResult struct {
	ID         string `json:"id"`
	ExternalID int64  `json:"external_id"`
	ChatID     string `json:"chat_id"`
}

type Inbound struct {
	EventID        string    `json:"event_id"`
	EnvelopeID     string    `json:"envelope_id,omitempty"`
	ChatID         string    `json:"chat_id"`
	MessageID      int64     `json:"message_id"`
	SenderID       int64     `json:"sender_id"`
	SenderUsername string    `json:"sender_username,omitempty"`
	Text           string    `json:"text"`
	Body           string    `json:"body"`
	Route          string    `json:"route,omitempty"`
	ReceivedAt     time.Time `json:"received_at"`
}

type TelegramConfig struct {
	Token        string            `json:"token,omitempty"`
	TokenFile    string            `json:"token_file,omitempty"`
	APIBase      string            `json:"api_base,omitempty"`
	StateDir     string            `json:"state_dir,omitempty"`
	DefaultRoute string            `json:"default_route,omitempty"`
	Routes       map[string]string `json:"routes,omitempty"`     // chat ID -> local session
	Recipients   map[string]string `json:"recipients,omitempty"` // local session/member -> chat ID
	MinInterval  time.Duration     `json:"-"`
}

type telegramSent struct {
	ID         string `json:"id"`
	Status     string `json:"status"`
	ExternalID int64  `json:"external_id,omitempty"`
	ChatID     string `json:"chat_id"`
}

type telegramState struct {
	Offset      int64           `json:"offset"`
	SeenUpdates map[string]bool `json:"seen_updates"`
	SeenIDs     map[string]bool `json:"seen_ids"`
	Messages    []Inbound       `json:"messages"`
	Sent        []telegramSent  `json:"sent"`
}

type TelegramGateway struct {
	cfg       TelegramConfig
	token     string
	base      string
	client    *http.Client
	connected bool
	me        Object
	mu        sync.Mutex
	lastSend  map[string]time.Time
}

var _ Channel = (*TelegramGateway)(nil)

func NewTelegramGateway(cfg TelegramConfig) (*TelegramGateway, error) {
	if cfg.APIBase == "" {
		cfg.APIBase = "https://api.telegram.org"
	}
	if cfg.StateDir == "" {
		cfg.StateDir = filepath.Join(Home(), "telegram")
	}
	if cfg.Routes == nil {
		cfg.Routes = map[string]string{}
	}
	if cfg.Recipients == nil {
		cfg.Recipients = map[string]string{}
	}
	if cfg.MinInterval == 0 {
		cfg.MinInterval = time.Second
	} else if cfg.MinInterval < 0 {
		cfg.MinInterval = 0
	}
	token := strings.TrimSpace(cfg.Token)
	if token == "" && cfg.TokenFile != "" {
		if st, err := os.Stat(cfg.TokenFile); err != nil {
			return nil, fmt.Errorf("stat Telegram token file: %w", err)
		} else if st.Mode().Perm()&0077 != 0 {
			return nil, errors.New("Telegram token file must be private (0600 or stricter)")
		}
		b, err := os.ReadFile(cfg.TokenFile)
		if err != nil {
			return nil, fmt.Errorf("read Telegram token file: %w", err)
		}
		token = strings.TrimSpace(string(b))
	}
	if token == "" {
		return nil, errors.New("Telegram token or token_file required")
	}
	if strings.ContainsAny(token, "\r\n") {
		return nil, errors.New("Telegram token contains a newline")
	}
	return &TelegramGateway{cfg: cfg, token: token, client: &http.Client{Timeout: 65 * time.Second}, lastSend: map[string]time.Time{}}, nil
}

func LoadTelegramConfig(path string) (TelegramConfig, error) {
	if path == "" {
		path = filepath.Join(Home(), "telegram.json")
	}
	b, err := Load(path)
	if err != nil {
		return TelegramConfig{}, err
	}
	routes := map[string]string{}
	for k, v := range O(b, "routes") {
		if s, ok := v.(string); ok {
			routes[k] = s
		}
	}
	recipients := map[string]string{}
	for k, v := range O(b, "recipients") {
		if s, ok := v.(string); ok {
			recipients[k] = s
		}
	}
	return TelegramConfig{Token: S(b, "token"), TokenFile: S(b, "token_file"), APIBase: S(b, "api_base"), StateDir: S(b, "state_dir"), DefaultRoute: S(b, "default_route"), Routes: routes, Recipients: recipients}, nil
}

func (g *TelegramGateway) Name() string { return "telegram" }

func (g *TelegramGateway) Identity() Object { return Clone(g.me) }

func (g *TelegramGateway) StatePath() string { return g.statePath() }

func (g *TelegramGateway) Capabilities() Capabilities {
	return Capabilities{DirectToMember: true, PrivateChat: true, Broadcast: true, Retention: 24 * time.Hour, History: false, MaxTextBytes: 4096}
}

func (g *TelegramGateway) Connect(ctx context.Context) error {
	if g.connected {
		return nil
	}
	var me Object
	if err := g.call(ctx, "getMe", nil, &me); err != nil {
		return err
	}
	var webhook Object
	if err := g.call(ctx, "getWebhookInfo", nil, &webhook); err != nil {
		return err
	}
	if S(webhook, "url") != "" {
		return errors.New("Telegram webhook is configured; refusing to delete or compete with it")
	}
	g.me = me
	g.connected = true
	return nil
}

func (g *TelegramGateway) Close() error {
	g.mu.Lock()
	g.connected = false
	g.mu.Unlock()
	return nil
}

func (g *TelegramGateway) Send(ctx context.Context, out Outbound) (SendResult, error) {
	if err := g.Connect(ctx); err != nil {
		return SendResult{}, err
	}
	if out.ID == "" {
		out.ID = ID()
	}
	state, err := g.loadState()
	if err != nil {
		return SendResult{}, err
	}
	for _, previous := range state.Sent {
		if previous.ID == out.ID {
			if previous.Status != "sent" {
				return SendResult{}, fmt.Errorf("Telegram send outcome for %s is unknown; do not resend blindly", out.ID)
			}
			return SendResult{ID: previous.ID, ExternalID: previous.ExternalID, ChatID: previous.ChatID}, nil
		}
	}
	chat := strings.TrimSpace(out.ChatID)
	if chat == "" {
		chat = strings.TrimSpace(out.Recipient)
	}
	if chat == "" {
		return SendResult{}, errors.New("Telegram recipient required")
	}
	text := encodeTelegramEnvelope(out)
	if len([]byte(text)) > g.Capabilities().MaxTextBytes {
		return SendResult{}, fmt.Errorf("Telegram message exceeds %d bytes", g.Capabilities().MaxTextBytes)
	}
	if err := g.waitChat(ctx, chat); err != nil {
		return SendResult{}, err
	}
	state.Sent = append(state.Sent, telegramSent{ID: out.ID, Status: "pending", ChatID: chat})
	if err := g.saveState(state); err != nil {
		return SendResult{}, err
	}
	var message Object
	err = g.call(ctx, "sendMessage", map[string]any{"chat_id": chat, "text": text}, &message)
	if err != nil {
		return SendResult{}, err
	}
	chatObject := O(message, "chat")
	chatID := telegramNumber(chatObject, "id")
	g.mu.Lock()
	g.lastSend[chat] = time.Now()
	g.mu.Unlock()
	result := SendResult{ID: out.ID, ExternalID: int64(N(message, "message_id")), ChatID: chatID}
	for i := range state.Sent {
		if state.Sent[i].ID == out.ID {
			state.Sent[i].Status, state.Sent[i].ExternalID, state.Sent[i].ChatID = "sent", result.ExternalID, chatID
		}
	}
	if err = g.saveState(state); err != nil {
		return SendResult{}, err
	}
	return result, nil
}

func (g *TelegramGateway) Receive(ctx context.Context, sink func(Inbound) error) error {
	if sink == nil {
		return errors.New("Telegram receive sink required")
	}
	if err := g.Connect(ctx); err != nil {
		return err
	}
	for {
		progress, err := g.receiveOnce(ctx, sink)
		if err != nil {
			return err
		}
		if !progress {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(250 * time.Millisecond):
			}
		}
	}
}

// ReceiveOnce is intentionally exposed for a daemon loop and deterministic
// integration tests. It persists every accepted update before advancing offset.
func (g *TelegramGateway) ReceiveOnce(ctx context.Context, sink func(Inbound) error) (bool, error) {
	if sink == nil {
		return false, errors.New("Telegram receive sink required")
	}
	if err := g.Connect(ctx); err != nil {
		return false, err
	}
	return g.receiveOnce(ctx, sink)
}

func (g *TelegramGateway) receiveOnce(ctx context.Context, sink func(Inbound) error) (bool, error) {
	state, err := g.loadState()
	if err != nil {
		return false, err
	}
	params := map[string]any{"timeout": 10, "allowed_updates": `["message"]`}
	if state.Offset > 0 {
		params["offset"] = state.Offset
	}
	var updates []Object
	if err = g.call(ctx, "getUpdates", params, &updates); err != nil {
		return false, err
	}
	if len(updates) == 0 {
		return false, nil
	}
	maxID := state.Offset - 1
	for _, update := range updates {
		updateID := int64(N(update, "update_id"))
		if updateID > maxID {
			maxID = updateID
		}
		key := strconv.FormatInt(updateID, 10)
		if state.SeenUpdates[key] {
			continue
		}
		message := O(update, "message")
		if len(message) == 0 {
			state.SeenUpdates[key] = true
			continue
		}
		chatID := telegramNumber(O(message, "chat"), "id")
		inbound := decodeTelegramUpdate(update, g.route(chatID))
		if inbound.EnvelopeID != "" && state.SeenIDs[inbound.EnvelopeID] {
			state.SeenUpdates[key] = true
			continue
		}
		state.Messages = append(state.Messages, inbound)
		state.SeenUpdates[key] = true
		if inbound.EnvelopeID != "" {
			state.SeenIDs[inbound.EnvelopeID] = true
		}
		if err = g.saveState(state); err != nil {
			return false, err
		}
		if err = sink(inbound); err != nil {
			return false, err
		}
	}
	if maxID >= state.Offset {
		state.Offset = maxID + 1
		if err = g.saveState(state); err != nil {
			return false, err
		}
	}
	return true, nil
}

func (g *TelegramGateway) route(chatID string) string {
	if route := g.cfg.Routes[chatID]; route != "" {
		return route
	}
	return g.cfg.DefaultRoute
}

func (g *TelegramGateway) waitChat(ctx context.Context, chat string) error {
	if g.cfg.MinInterval <= 0 {
		return nil
	}
	g.mu.Lock()
	last := g.lastSend[chat]
	wait := g.cfg.MinInterval - time.Since(last)
	g.mu.Unlock()
	if wait <= 0 {
		return nil
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (g *TelegramGateway) call(ctx context.Context, method string, params map[string]any, out any) error {
	base := strings.TrimRight(g.cfg.APIBase, "/") + "/bot" + g.token + "/" + method
	form := url.Values{}
	for k, v := range params {
		switch x := v.(type) {
		case string:
			form.Set(k, x)
		default:
			b, _ := json.Marshal(x)
			form.Set(k, string(b))
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := g.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return err
	}
	var envelope struct {
		OK          bool            `json:"ok"`
		Result      json.RawMessage `json:"result"`
		ErrorCode   int             `json:"error_code"`
		Description string          `json:"description"`
		Parameters  struct {
			RetryAfter int `json:"retry_after"`
		} `json:"parameters"`
	}
	if err = json.Unmarshal(body, &envelope); err != nil {
		return fmt.Errorf("Telegram %s invalid response: %w", method, err)
	}
	if !envelope.OK {
		if envelope.ErrorCode == http.StatusTooManyRequests && envelope.Parameters.RetryAfter > 0 && envelope.Parameters.RetryAfter <= 5 {
			timer := time.NewTimer(time.Duration(envelope.Parameters.RetryAfter) * time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
			return g.call(ctx, method, params, out)
		}
		return fmt.Errorf("Telegram %s: %s", method, envelope.Description)
	}
	if out == nil || len(envelope.Result) == 0 {
		return nil
	}
	return json.Unmarshal(envelope.Result, out)
}

func (g *TelegramGateway) statePath() string { return filepath.Join(g.cfg.StateDir, "state.json") }

func (g *TelegramGateway) loadState() (telegramState, error) {
	state := telegramState{SeenUpdates: map[string]bool{}, SeenIDs: map[string]bool{}, Messages: []Inbound{}, Sent: []telegramSent{}}
	o, err := Load(g.statePath())
	if os.IsNotExist(err) {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	state.Offset = int64(N(o, "offset"))
	for k, v := range O(o, "seen_updates") {
		if b, ok := v.(bool); ok && b {
			state.SeenUpdates[k] = true
		}
	}
	for k, v := range O(o, "seen_ids") {
		if b, ok := v.(bool); ok && b {
			state.SeenIDs[k] = true
		}
	}
	b, _ := json.Marshal(o["messages"])
	_ = json.Unmarshal(b, &state.Messages)
	b, _ = json.Marshal(o["sent"])
	_ = json.Unmarshal(b, &state.Sent)
	return state, nil
}

func (g *TelegramGateway) saveState(state telegramState) error {
	return Save(g.statePath(), state)
}

func encodeTelegramEnvelope(out Outbound) string {
	if out.ID == "" {
		return out.Text
	}
	header := Object{"v": 1, "uid": out.ID}
	if out.Kind != "" {
		header["kind"] = out.Kind
	}
	b, _ := json.Marshal(header)
	return "OTOHA/1 " + string(b) + "\n" + out.Text
}

func decodeTelegramUpdate(update Object, route string) Inbound {
	message := O(update, "message")
	chat := O(message, "chat")
	from := O(message, "from")
	text := S(message, "text")
	body, envelopeID := text, ""
	if strings.HasPrefix(text, "OTOHA/1 ") {
		parts := strings.SplitN(text, "\n", 2)
		var header Object
		if len(parts) == 2 && json.Unmarshal([]byte(strings.TrimPrefix(parts[0], "OTOHA/1 ")), &header) == nil {
			envelopeID = S(header, "uid")
			body = parts[1]
		}
	}
	return Inbound{EventID: strconv.Itoa(N(update, "update_id")), EnvelopeID: envelopeID, ChatID: telegramNumber(chat, "id"), MessageID: int64(N(message, "message_id")), SenderID: int64(N(from, "id")), SenderUsername: S(from, "username"), Text: text, Body: body, Route: route, ReceivedAt: time.Now().UTC()}
}

func telegramNumber(o Object, key string) string {
	switch value := o[key].(type) {
	case float64:
		return strconv.FormatInt(int64(value), 10)
	case int:
		return strconv.Itoa(value)
	case int64:
		return strconv.FormatInt(value, 10)
	case json.Number:
		return value.String()
	case string:
		return value
	default:
		return ""
	}
}
