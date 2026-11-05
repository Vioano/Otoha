package otoha

import (
	"context"
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// The work page is a rendering of accepted collaboration messages. Its
// presentation document does not introduce task types or state into Graph Core.
//
//go:embed page.html
var workPageHTML string

var pageRequestIDRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,79}$`)

// Request IDs use the collaboration protocol's client_id alphabet, not the
// stricter business-name alphabet for teams. This also keeps filenames local.
func pageRequestID(id string) error {
	if !pageRequestIDRE.MatchString(id) {
		return errors.New("request ID must start with an ASCII letter or digit and contain at most 80 letters, digits, dots, underscores or hyphens")
	}
	return nil
}

type pageWorker struct {
	wake chan struct{}
}

type pageManager struct {
	home, token, baseURL string
	mu                   sync.Mutex
	states               map[string]Object
	workers              map[string]*pageWorker
}

func newPageManager(home string) *pageManager {
	return &pageManager{home: home, states: map[string]Object{}, workers: map[string]*pageWorker{}}
}

func (m *pageManager) statePath(team string) string {
	return filepath.Join(m.home, "pages", "state", team+".json")
}
func (m *pageManager) pendingPath(team, id string) string {
	return filepath.Join(m.home, "pages", "outbox", team, id+".json")
}
func (m *pageManager) receiptPath(team, id string) string {
	return filepath.Join(m.home, "pages", "receipts", team, id+".json")
}
func pageTime() string { return time.Now().UTC().Format(time.RFC3339Nano) }

func (m *pageManager) stateLocked(team string) (Object, error) {
	if state, ok := m.states[team]; ok {
		return state, nil
	}
	state, err := Load(m.statePath(team))
	if os.IsNotExist(err) {
		state, err = Object{"schema": 1, "team": team, "cursor": 0, "page": Object{}, "feedback": []any{}}, nil
	}
	if err != nil {
		return nil, err
	}
	// A previous healthy connection does not prove this process has connected.
	state["connection"] = "connecting"
	m.states[team] = state
	return state, nil
}

func (m *pageManager) changeState(team string, change func(Object) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	state, err := m.stateLocked(team)
	if err != nil {
		return err
	}
	next := Clone(state)
	if err = change(next); err != nil {
		return err
	}
	if err = Save(m.statePath(team), next); err != nil {
		return err
	}
	m.states[team] = next
	return nil
}

func pageOperationSummary(op Object) Object {
	r := Object{}
	for _, key := range []string{"request_id", "state", "operation", "created_at", "accepted_at", "message_id", "last_error", "duplicate", "page_request_id", "page_message_id"} {
		if value, ok := op[key]; ok {
			r[key] = value
		}
	}
	return r
}

func (m *pageManager) snapshot(team string) (Object, error) {
	if err := Name(team); err != nil {
		return nil, err
	}
	if _, err := loadTeamConfig(team); err != nil {
		return nil, err
	}
	m.mu.Lock()
	state, err := m.stateLocked(team)
	if err != nil {
		m.mu.Unlock()
		return nil, err
	}
	result := Clone(state)
	m.mu.Unlock()
	result["pending"] = []any{}
	paths, err := filepath.Glob(filepath.Join(m.home, "pages", "outbox", team, "*.json"))
	if err != nil {
		return nil, err
	}
	for _, path := range paths {
		op, err := Load(path)
		if os.IsNotExist(err) { // A concurrent flush moved this operation to receipts.
			continue
		}
		if err != nil {
			return nil, err
		}
		result["pending"] = append(Array(result["pending"]), pageOperationSummary(op))
	}
	return result, nil
}

func pageDocument(input Object) (Object, error) {
	isHTML := S(input, "format") == "html"
	if isHTML {
		html, ok := input["html"].(string)
		if !ok || !utf8.ValidString(html) {
			return nil, errors.New("HTML document requires a UTF-8 html string")
		}
	} else if strings.TrimSpace(S(input, "title")) == "" || len(S(input, "title")) > 600 {
		return nil, errors.New("document.title is required and must be at most 600 bytes")
	}
	raw, err := json.Marshal(input)
	if err != nil || len(raw) > 48000 {
		return nil, errors.New("page document must fit within 48000 JSON bytes")
	}
	// Normalize library-created Object values to the same JSON representation
	// as CLI/HTTP input before checking the renderer contract.
	var normalized Object
	if err = json.Unmarshal(raw, &normalized); err != nil {
		return nil, err
	}
	input = normalized
	if isHTML {
		// HTML is an opaque presentation artifact. The work-list fields below
		// belong to one optional renderer template, not to all work pages.
		return input, nil
	}
	if value, exists := input["summary"]; exists {
		if _, ok := value.(string); !ok {
			return nil, errors.New("document.summary must be a string")
		}
	}
	if items, present := input["items"]; present {
		list, ok := items.([]any)
		if !ok || len(list) > 100 {
			return nil, errors.New("document.items must be an array of at most 100 objects")
		}
		seen := map[string]bool{}
		for _, item := range list {
			row, ok := item.(map[string]any)
			if !ok {
				return nil, errors.New("document.items must contain objects")
			}
			id := S(Object(row), "id")
			if id == "" || len(id) > 300 || seen[id] {
				return nil, errors.New("each document item requires a unique stable id of at most 300 bytes")
			}
			if strings.TrimSpace(S(Object(row), "title")) == "" {
				return nil, errors.New("each document item requires a nonempty string title")
			}
			for _, field := range []string{"summary", "status", "detail"} {
				if value, exists := row[field]; exists {
					if _, ok := value.(string); !ok {
						return nil, fmt.Errorf("item.%s must be a string", field)
					}
				}
			}
			seen[id] = true
		}
	}
	return Clone(input), nil
}

func (m *pageManager) enqueue(team, operation string, input Object) (Object, error) {
	if err := Name(team); err != nil {
		return nil, err
	}
	cfg, err := loadTeamConfig(team)
	if err != nil {
		return nil, err
	}
	id := S(input, "request_id")
	if id == "" {
		id = ID()
	}
	if err := pageRequestID(id); err != nil {
		return nil, fmt.Errorf("invalid request_id: %w", err)
	}
	payload := Object{"schema": "otoha-page/1", "operation": operation}
	summary, kind, source := "工作页更新", "status", "agent"
	switch operation {
	case "publish":
		document, err := pageDocument(O(input, "document"))
		if err != nil {
			return nil, err
		}
		payload["document"] = document
		summary = "工作页：" + S(document, "title")
	case "feedback":
		text, pageID := strings.TrimSpace(S(input, "text")), S(input, "page_request_id")
		if text == "" || len(text) > 8000 {
			return nil, errors.New("feedback text is required and must be at most 8000 bytes")
		}
		if err := pageRequestID(pageID); err != nil {
			return nil, errors.New("feedback requires the observed page_request_id")
		}
		messageID, validMessageID := eventInteger(input["page_message_id"])
		if !validMessageID || messageID <= 0 {
			return nil, errors.New("feedback requires a positive observed page_message_id from page status; request IDs alone are only actor-local")
		}
		payload["text"], payload["page_request_id"], payload["page_message_id"] = text, pageID, messageID
		summary, kind, source = "工作页反馈", "question", "human"
	default:
		return nil, errors.New("unknown page operation")
	}
	raw, _ := json.Marshal(payload)
	body := Object{"channel": cfg.Channel, "session": "otoha-page", "client_id": id, "uid": id,
		"summary": summary, "body": string(raw), "text": string(raw), "class": "queued", "kind": kind,
		"delivery": "tool", "source": source, "agent": "otoha-cli"}
	encodedBody, err := json.Marshal(body)
	if err != nil || len(encodedBody) > 65536 {
		return nil, errors.New("encoded page message exceeds the service's 65536 byte request limit; reduce the document")
	}
	op := Object{"request_id": id, "team": team, "operation": operation, "state": "queued", "created_at": pageTime(), "body": body}
	if operation == "feedback" {
		op["page_request_id"] = payload["page_request_id"]
		op["page_message_id"] = payload["page_message_id"]
	}
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
	return result, nil
}

func (m *pageManager) operation(team, id string) (Object, error) {
	if err := Name(team); err != nil {
		return nil, err
	}
	if err := pageRequestID(id); err != nil {
		return nil, err
	}
	for _, path := range []string{m.receiptPath(team, id), m.pendingPath(team, id)} {
		op, err := Load(path)
		if err == nil {
			return pageOperationSummary(op), nil
		}
		if !os.IsNotExist(err) {
			return nil, err
		}
	}
	return nil, os.ErrNotExist
}

func pageJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(value)
}

func (m *pageManager) cookieName() string {
	// Browser cookies are shared across ports. A private test Home must not
	// replace another local Otoha instance's browser session.
	parsed, _ := url.Parse(m.baseURL)
	return "otoha-page-" + parsed.Port()
}

func (m *pageManager) authorized(r *http.Request) bool {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if token == r.Header.Get("Authorization") {
		token = ""
	}
	if token == "" {
		if cookie, err := r.Cookie(m.cookieName()); err == nil {
			token = cookie.Value
		}
	}
	return token != "" && subtle.ConstantTimeCompare([]byte(token), []byte(m.token)) == 1
}

func (m *pageManager) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Host != strings.TrimPrefix(m.baseURL, "http://") {
		pageJSON(w, http.StatusForbidden, Object{"error": "invalid_host"})
		return
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if strings.HasPrefix(r.URL.Path, "/git/") {
		m.serveGit(w, r)
		return
	}
	if r.Method == http.MethodGet && r.URL.Path == "/" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		io.WriteString(w, workPageHTML)
		return
	}
	if !strings.HasPrefix(r.URL.Path, "/api/") {
		http.NotFound(w, r)
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" && origin != m.baseURL {
		pageJSON(w, http.StatusForbidden, Object{"error": "invalid_origin"})
		return
	}
	if !m.authorized(r) {
		pageJSON(w, http.StatusUnauthorized, Object{"error": "local_authentication_required"})
		return
	}
	var result Object
	var err error
	team := r.URL.Query().Get("team")
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/api/session":
		http.SetCookie(w, &http.Cookie{Name: m.cookieName(), Value: m.token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
		result = Object{"authenticated": true}
	case r.Method == http.MethodGet && r.URL.Path == "/api/teams":
		teams := []Object{}
		paths, _ := filepath.Glob(filepath.Join(m.home, "teams", "*", "team.json"))
		for _, path := range paths {
			cfg, e := loadTeamConfig(filepath.Base(filepath.Dir(path)))
			if e == nil {
				state, _ := m.snapshot(cfg.Name)
				teams = append(teams, Object{"name": cfg.Name, "project": cfg.Project, "connection": S(state, "connection")})
			}
		}
		result = Object{"schema": 1, "teams": teams}
	case r.Method == http.MethodGet && (r.URL.Path == "/api/state" || r.URL.Path == "/api/feedback"):
		result, err = m.snapshot(team)
		if err == nil && r.URL.Path == "/api/feedback" {
			result = Object{"schema": 1, "team": team, "feedback": result["feedback"], "feedback_truncated": B(result, "feedback_truncated"), "limit": 100, "cursor": result["cursor"], "connection": result["connection"]}
		}
	case r.Method == http.MethodGet && r.URL.Path == "/api/operations":
		result, err = m.operation(team, r.URL.Query().Get("id"))
	case r.Method == http.MethodGet && r.URL.Path == "/api/messages":
		result, err = m.messageSnapshot(team)
	case r.Method == http.MethodGet && r.URL.Path == "/api/messages/by-id":
		result, err = m.messageByID(r.Context(), team, r.URL.Query().Get("id"))
	case r.Method == http.MethodPost && (r.URL.Path == "/api/publish" || r.URL.Path == "/api/feedback" || r.URL.Path == "/api/messages"):
		var input Object
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 65536))
		if err = decoder.Decode(&input); err == nil {
			var extra any
			if decoder.Decode(&extra) != io.EOF {
				err = errors.New("one JSON object required")
			} else if r.URL.Path == "/api/messages" {
				result, err = m.enqueueMessage(S(input, "team"), input)
			} else {
				result, err = m.enqueue(S(input, "team"), strings.TrimPrefix(r.URL.Path, "/api/"), input)
			}
		}
	default:
		pageJSON(w, http.StatusNotFound, Object{"error": "route_not_found"})
		return
	}
	if err != nil {
		status := http.StatusBadRequest
		if os.IsNotExist(err) {
			status = http.StatusNotFound
		}
		var upstream *APIError
		if errors.As(err, &upstream) {
			status = upstream.Status
		}
		pageJSON(w, status, Object{"error": err.Error()})
		return
	}
	pageJSON(w, http.StatusOK, result)
}

// RunPageService runs inside the same process as Receive and owns only this
// OTOHA_HOME's work-page state. It does not register a second receiver.
func RunPageService(ctx context.Context) error {
	m := newPageManager(Home())
	address := "127.0.0.1:0"
	old, err := Load(filepath.Join(m.home, "page-runtime.json"))
	if err == nil {
		m.token = S(old, "token")
		if parsed, e := url.Parse(S(old, "url")); e == nil && parsed.Scheme == "http" && parsed.Hostname() == "127.0.0.1" && parsed.Port() != "" {
			address = parsed.Host
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if m.token == "" {
		m.token = ID() + ID()
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	m.baseURL = "http://" + listener.Addr().String()
	if err = Save(filepath.Join(m.home, "page-runtime.json"), Object{"schema": 1, "url": m.baseURL, "token": m.token, "pid": os.Getpid(), "started_at": pageTime()}); err != nil {
		listener.Close()
		return err
	}
	server := &http.Server{Handler: http.HandlerFunc(m.serveHTTP), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
	go func() { <-ctx.Done(); server.Close() }()
	go m.discoverTeams(ctx)
	err = server.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func pageLocalCall(ctx context.Context, method, path string, body any) (Object, error) {
	local, err := Load(filepath.Join(Home(), "page-runtime.json"))
	if err != nil {
		return nil, err
	}
	client := NewClient(Object{"url": S(local, "url"), "token": S(local, "token")}, nil, false)
	defer client.Transport.Close()
	return client.Call(ctx, method, path, body)
}

// EnsurePageService starts or reuses this Home's existing receiver, then waits
// for its local API. Enrollment and repo access are separate readiness facts.
func EnsurePageService(ctx context.Context, team string) (Object, error) {
	if _, err := loadTeamConfig(team); err != nil {
		return nil, err
	}
	check := func() bool {
		deadline, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		defer cancel()
		_, err := pageLocalCall(deadline, http.MethodGet, "/api/teams", nil)
		return err == nil
	}
	if !check() {
		if err := InstallReceiver(); err != nil {
			return nil, err
		}
		deadline := time.Now().Add(10 * time.Second)
		for !check() {
			if time.Now().After(deadline) {
				return nil, errors.New("local page service did not become ready; inspect receiver.log")
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(100 * time.Millisecond):
			}
		}
	}
	local, err := Load(filepath.Join(Home(), "page-runtime.json"))
	if err != nil {
		return nil, err
	}
	fragment := url.Values{"token": {S(local, "token")}, "team": {team}}
	connection := "connecting"
	deadline := time.Now().Add(5 * time.Second)
	for {
		probe, cancel := context.WithTimeout(ctx, time.Second)
		state, stateErr := pageLocalCall(probe, http.MethodGet, "/api/state?team="+url.QueryEscape(team), nil)
		cancel()
		if stateErr != nil {
			return nil, stateErr
		}
		connection = S(state, "connection")
		if connection != "connecting" || time.Now().After(deadline) {
			break
		}
		if !pagePause(ctx, 100*time.Millisecond) {
			return nil, ctx.Err()
		}
	}
	return Object{"ready": connection == "online", "local_ready": true, "connection": connection, "team": team, "url": S(local, "url") + "/#" + fragment.Encode(), "local_url": S(local, "url"), "pid": N(local, "pid")}, nil
}

func pageCommandHelp() Object {
	return Object{
		"usage": "otoha page open|status|publish|feedback|operation --team NAME",
		"examples": []string{
			"otoha page open --team NAME --browser",
			"otoha page publish --team NAME --file DOCUMENT.json --request-id ID",
			"otoha page publish --team NAME --file PRESENTATION.html --request-id ID",
			"otoha page status --team NAME",
			"otoha page feedback --team NAME --page REQUEST_ID --page-message-id MESSAGE_ID --text TEXT",
			"otoha page feedback --team NAME",
			"otoha page operation --team NAME --request-id ID",
		},
		"feedback_source": "先用 page status 取得 page.request_id 和 page.message_id，再同时传给反馈；message_id 只在该团队配置的中心范围内标识版本。",
	}
}

func PageCommand(ctx context.Context, args []string) (Object, error) {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		return pageCommandHelp(), nil
	}
	switch args[0] {
	case "open", "status", "publish", "feedback", "operation":
	default:
		return nil, fmt.Errorf("unknown page action %q; use otoha page help", args[0])
	}
	f := flag.NewFlagSet("page "+args[0], flag.ContinueOnError)
	f.SetOutput(io.Discard)
	team := f.String("team", "", "joined team")
	file := f.String("file", "", "page document JSON or UTF-8 HTML")
	format := f.String("format", "", "html or json; inferred from .html/.htm when omitted")
	requestID := f.String("request-id", "", "stable operation ID")
	text := f.String("text", "", "feedback text")
	page := f.String("page", "", "observed page request ID")
	pageMessageID := f.Int("page-message-id", 0, "accepted page message ID in this team's authority; obtain it with page status")
	browser := f.Bool("browser", false, "open the local page")
	if err := f.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return pageCommandHelp(), nil
		}
		return nil, err
	}
	if *team == "" {
		name, err := discoverTeamName()
		if err != nil {
			return nil, err
		}
		*team = name
	}
	ready, err := EnsurePageService(ctx, *team)
	if err != nil {
		return nil, err
	}
	switch args[0] {
	case "open":
		if *browser {
			command, commandArgs := "open", []string{S(ready, "url")}
			if runtime.GOOS == "linux" {
				command = "xdg-open"
			} else if runtime.GOOS == "windows" {
				command, commandArgs = "rundll32", []string{"url.dll,FileProtocolHandler", S(ready, "url")}
			}
			if err := exec.CommandContext(ctx, command, commandArgs...).Run(); err != nil {
				return ready, err
			}
		}
		return ready, nil
	case "status":
		return pageLocalCall(ctx, http.MethodGet, "/api/state?team="+url.QueryEscape(*team), nil)
	case "publish":
		if *file == "" {
			return nil, errors.New("page publish requires --file DOCUMENT.json or PRESENTATION.html")
		}
		document, err := pageDocumentFile(*file, *format)
		if err != nil {
			return nil, err
		}
		return pageLocalCall(ctx, http.MethodPost, "/api/publish", Object{"team": *team, "request_id": *requestID, "document": document})
	case "feedback":
		if *text == "" {
			return pageLocalCall(ctx, http.MethodGet, "/api/feedback?team="+url.QueryEscape(*team), nil)
		}
		return pageLocalCall(ctx, http.MethodPost, "/api/feedback", Object{"team": *team, "request_id": *requestID, "page_request_id": *page, "page_message_id": *pageMessageID, "text": *text})
	case "operation":
		if *requestID == "" {
			return nil, errors.New("page operation requires --request-id ID")
		}
		return pageLocalCall(ctx, http.MethodGet, "/api/operations?team="+url.QueryEscape(*team)+"&id="+url.QueryEscape(*requestID), nil)
	default:
		return nil, fmt.Errorf("unknown page action %q", args[0])
	}
}

func pageDocumentFile(path, format string) (Object, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if format == "" {
		format = "json"
		if extension := strings.ToLower(filepath.Ext(path)); extension == ".html" || extension == ".htm" {
			format = "html"
		}
	}
	var document Object
	switch format {
	case "html":
		if !utf8.Valid(data) {
			return nil, errors.New("HTML file must contain UTF-8 text")
		}
		document = Object{"format": "html", "html": string(data), "title": filepath.Base(path)}
	case "json":
		if err = json.Unmarshal(data, &document); err != nil {
			return nil, err
		}
	default:
		return nil, errors.New("page format must be html or json")
	}
	return pageDocument(document)
}

// Keep deterministic local ordering for pending operations while the authority
// remains responsible for the accepted event order.
func sortPageOperations(operations []Object) {
	sort.Slice(operations, func(i, j int) bool { return S(operations[i], "created_at") < S(operations[j], "created_at") })
}
