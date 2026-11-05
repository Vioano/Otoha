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
	Token          string            `json:"token,omitempty"`
	TokenFile      string            `json:"token_file,omitempty"`
	APIBase        string            `json:"api_base,omitempty"`
	StateDir       string            `json:"state_dir,omitempty"`
	DefaultRoute   string            `json:"default_route,omitempty"`
	Routes         map[string]string `json:"routes,omitempty"`          // chat ID -> local session
	Recipients     map[string]string `json:"recipients,omitempty"`      // local session/member -> chat ID
	AllowedUpdates []string          `json:"allowed_updates,omitempty"` // nil preserves the bot's existing subscription
	MinInterval    time.Duration     `json:"-"`
}

type telegramSent struct {
	ID         string `json:"id"`
	Status     string `json:"status"`
	ExternalID int64  `json:"external_id,omitempty"`
	ChatID     string `json:"chat_id"`
}

type telegramState struct {
	Offset      int64             `json:"offset"`
	SeenUpdates map[string]bool   `json:"seen_updates"`
	SeenIDs     map[string]bool   `json:"seen_ids"`
	Messages    []Inbound         `json:"messages"`
	Sent        []telegramSent    `json:"sent"`
	Pending     []Inbound         `json:"pending,omitempty"`
	Unsupported map[string]Object `json:"unsupported_updates,omitempty"`
}

type TelegramGateway struct {
	cfg       TelegramConfig
	token     string
	base      string
	client    *http.Client
	connected bool
	me        Object
	mu        sync.Mutex
	stateMu   sync.Mutex
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
	var allowedUpdates []string
	if value, present := b["allowed_updates"]; present {
		values, ok := value.([]any)
		if !ok {
			return TelegramConfig{}, errors.New("Telegram allowed_updates must be an array of strings when explicitly configured")
		}
		allowedUpdates = []string{}
		for _, value := range values {
			update, ok := value.(string)
			if !ok {
				return TelegramConfig{}, errors.New("Telegram allowed_updates must contain strings")
			}
			allowedUpdates = append(allowedUpdates, update)
		}
	}
	return TelegramConfig{Token: S(b, "token"), TokenFile: S(b, "token_file"), APIBase: S(b, "api_base"), StateDir: S(b, "state_dir"), DefaultRoute: S(b, "default_route"), Routes: routes, Recipients: recipients, AllowedUpdates: allowedUpdates}, nil
}

func (g *TelegramGateway) Name() string { return "telegram" }

func (g *TelegramGateway) Identity() Object {
	g.mu.Lock()
	defer g.mu.Unlock()
	return Clone(g.me)
}

func (g *TelegramGateway) StatePath() string { return g.statePath() }

func (g *TelegramGateway) Capabilities() Capabilities {
	return Capabilities{DirectToMember: true, PrivateChat: true, Broadcast: true, Retention: 24 * time.Hour, History: false, MaxTextBytes: 4096}
}

func (g *TelegramGateway) Connect(ctx context.Context) error {
	g.mu.Lock()
	defer g.mu.Unlock()
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
	var concurrentPrevious *telegramSent
	if _, err = g.updateState(func(current *telegramState) error {
		for _, previous := range current.Sent {
			if previous.ID == out.ID {
				copy := previous
				concurrentPrevious = &copy
				return nil
			}
		}
		current.Sent = append(current.Sent, telegramSent{ID: out.ID, Status: "pending", ChatID: chat})
		return nil
	}); err != nil {
		return SendResult{}, err
	}
	if concurrentPrevious != nil {
		if concurrentPrevious.Status != "sent" {
			return SendResult{}, fmt.Errorf("Telegram send outcome for %s is unknown; do not resend blindly", out.ID)
		}
		return SendResult{ID: concurrentPrevious.ID, ExternalID: concurrentPrevious.ExternalID, ChatID: concurrentPrevious.ChatID}, nil
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
	if _, err := g.updateState(func(current *telegramState) error {
		for i := range current.Sent {
			if current.Sent[i].ID == out.ID {
				current.Sent[i].Status, current.Sent[i].ExternalID, current.Sent[i].ChatID = "sent", result.ExternalID, chatID
				return nil
			}
		}
		return errors.New("Telegram accepted send but its local pending record is missing")
	}); err != nil {
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
	progress, err := g.deliverPending(&state, sink)
	if err != nil {
		return progress, err
	}
	params := map[string]any{"timeout": 10}
	if g.cfg.AllowedUpdates != nil {
		params["allowed_updates"] = g.cfg.AllowedUpdates
	}
	if state.Offset > 0 {
		params["offset"] = state.Offset
	}
	var updates []Object
	if err = g.call(ctx, "getUpdates", params, &updates); err != nil {
		return false, err
	}
	if len(updates) == 0 {
		return progress, nil
	}
	// Preserve unsupported originals and stop before confirming any update in
	// this batch. Keeping an existing all-types subscription must not silently
	// discard callbacks or media that this text adapter cannot deliver.
	for _, update := range updates {
		message := O(update, "message")
		if _, textMessage := message["text"].(string); len(message) == 0 || !textMessage {
			key := strconv.FormatInt(int64(N(update, "update_id")), 10)
			if state, err = g.updateState(func(current *telegramState) error {
				current.Unsupported[key] = Clone(update)
				return nil
			}); err != nil {
				return progress, err
			}
			return progress, fmt.Errorf("Telegram update %s is not a supported text message; original retained for inspection and source cursor unchanged", key)
		}
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
		chatID := telegramNumber(O(message, "chat"), "id")
		inbound := decodeTelegramUpdate(update, g.route(chatID))
		if inbound.EnvelopeID != "" && state.SeenIDs[inbound.EnvelopeID] {
			if state, err = g.updateState(func(current *telegramState) error { current.SeenUpdates[key] = true; return nil }); err != nil {
				return progress, err
			}
			continue
		}
		if state, err = g.updateState(func(current *telegramState) error {
			if current.SeenUpdates[key] {
				return nil
			}
			current.Messages = append(current.Messages, inbound)
			current.SeenUpdates[key] = true
			if inbound.EnvelopeID != "" {
				current.SeenIDs[inbound.EnvelopeID] = true
			}
			current.Pending = append(current.Pending, inbound)
			return nil
		}); err != nil {
			return false, err
		}
		if _, err = g.deliverPending(&state, sink); err != nil {
			return progress, err
		}
	}
	if maxID >= state.Offset {
		if state, err = g.updateState(func(current *telegramState) error {
			if maxID >= current.Offset {
				current.Offset = maxID + 1
			}
			return nil
		}); err != nil {
			return false, err
		}
	}
	return true, nil
}

func (g *TelegramGateway) deliverPending(state *telegramState, sink func(Inbound) error) (bool, error) {
	progress := false
	for len(state.Pending) > 0 {
		if err := sink(state.Pending[0]); err != nil {
			return progress, err
		}
		eventID := state.Pending[0].EventID
		next, err := g.updateState(func(current *telegramState) error {
			for i, pending := range current.Pending {
				if pending.EventID == eventID {
					current.Pending = append(current.Pending[:i], current.Pending[i+1:]...)
					break
				}
			}
			return nil
		})
		if err != nil {
			return progress, err
		}
		*state = next
		progress = true
	}
	return progress, nil
}

func (g *TelegramGateway) route(chatID string) string {
	if route := g.cfg.Routes[chatID]; route != "" {
		return route
	}
	return g.cfg.DefaultRoute
}

// TelegramRecipient resolves a targeted message without ever falling back to a
// public destination. A bridge currently forwards to one distinct chat at a time.
func TelegramRecipient(cfg TelegramConfig, message Object) (string, error) {
	targets := Array(O(message, "payload")["to_sessions"])
	if len(targets) == 0 {
		if recipient := strings.TrimSpace(cfg.Recipients["public"]); recipient != "" {
			return recipient, nil
		}
		return "", errors.New("Telegram public route is not configured")
	}
	recipient := ""
	for _, value := range targets {
		target, ok := value.(string)
		if !ok || target == "" || strings.TrimSpace(cfg.Recipients[target]) == "" {
			return "", errors.New("Telegram targeted session has no configured recipient; refusing public fallback")
		}
		chat := strings.TrimSpace(cfg.Recipients[target])
		if recipient != "" && recipient != chat {
			return "", errors.New("Telegram bridge cannot forward one targeted envelope to multiple distinct chats")
		}
		recipient = chat
	}
	return recipient, nil
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
		return g.callError(method, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := g.client.Do(req)
	if err != nil {
		return g.callError(method, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return g.callError(method, err)
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
		return fmt.Errorf("Telegram %s: %s", method, strings.ReplaceAll(envelope.Description, g.token, "[redacted]"))
	}
	if out == nil || len(envelope.Result) == 0 {
		return nil
	}
	return json.Unmarshal(envelope.Result, out)
}

type telegramCallError struct {
	message string
	cause   error
}

func (e *telegramCallError) Error() string { return e.message }
func (e *telegramCallError) Unwrap() error { return e.cause }

func (g *TelegramGateway) callError(method string, err error) error {
	// net/http includes the full request URL in url.Error. Bot API puts the
	// credential in that URL, so printing the transport error verbatim leaks it.
	var requestError *url.Error
	if errors.As(err, &requestError) {
		err = requestError.Err
	}
	return &telegramCallError{message: "Telegram " + method + " request failed: " + strings.ReplaceAll(err.Error(), g.token, "[redacted]"), cause: err}
}

func (g *TelegramGateway) statePath() string { return filepath.Join(g.cfg.StateDir, "state.json") }

func (g *TelegramGateway) loadState() (telegramState, error) {
	state := telegramState{SeenUpdates: map[string]bool{}, SeenIDs: map[string]bool{}, Messages: []Inbound{}, Sent: []telegramSent{}, Pending: []Inbound{}, Unsupported: map[string]Object{}}
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
	b, _ = json.Marshal(o["pending"])
	_ = json.Unmarshal(b, &state.Pending)
	b, _ = json.Marshal(o["unsupported_updates"])
	_ = json.Unmarshal(b, &state.Unsupported)
	if state.Unsupported == nil {
		state.Unsupported = map[string]Object{}
	}
	return state, nil
}

func (g *TelegramGateway) saveState(state telegramState) error {
	return Save(g.statePath(), state)
}

// Only short local state updates hold this lock. A long poll, Telegram send or
// sink call must not block the opposite direction of the same bridge instance.
func (g *TelegramGateway) updateState(update func(*telegramState) error) (telegramState, error) {
	g.stateMu.Lock()
	defer g.stateMu.Unlock()
	state, err := g.loadState()
	if err != nil {
		return state, err
	}
	if err = update(&state); err != nil {
		return state, err
	}
	return state, g.saveState(state)
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
