// Package faketelegram is the in-memory Telegram Bot API of every suite
// (docs/qualidade/dubles.md#telegram-falso). M0 serves the text flow:
// getUpdates (long polling timed by the clock), sendMessage,
// editMessageText and answerCallbackQuery, with injected failures. Voice,
// photos, documents, getFile and callback buttons arrive with the bot in M5;
// until then those methods fail the test as not implemented.
package faketelegram

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lucasndas/mestre/internal/testkit"
	"github.com/lucasndas/mestre/pkg/clock"
)

const origin = "telegram"

// User is a Telegram user.
type User struct {
	ID        int64
	FirstName string
	Username  string
}

// Chat is where a message is sent. A private chat has the user id.
type Chat struct {
	ID    int64
	Type  string // "private" or "group"
	Title string // groups only
}

// Private returns the private chat with u.
func Private(u User) Chat { return Chat{ID: u.ID, Type: "private"} }

// Sent is one message the bot sent or edited.
type Sent struct {
	Method    string
	ChatID    int64
	MessageID int64
	Text      string
	Params    map[string]any
}

// Failure is injected into the next call of a method.
type Failure struct {
	Status     int  // HTTP status and error_code, e.g. 429 or 502
	RetryAfter int  // seconds, for 429
	Cut        bool // the connection drops without a response
}

// Server is the fake Bot API. Use it as an http.Handler, usually through
// fakenet.HandlerTransport.
type Server struct {
	clk   clock.Clock
	rep   testkit.Reporter
	token string
	recs  fs.FS
	ver   string

	mu       sync.Mutex
	updates  []map[string]any
	nextUpd  int64
	nextMsg  int64
	sent     []Sent
	calls    map[string]int
	failures map[string][]Failure
	notify   chan struct{} // closed and replaced on each new update
}

// New returns a server for the bot token, using the newest recordings.
func New(clk clock.Clock, rep testkit.Reporter, token string) (*Server, error) {
	recs := testkit.Recordings()
	ver, err := testkit.LatestVersion(recs, origin)
	if err != nil {
		return nil, err
	}
	return &Server{
		clk: clk, rep: rep, token: token, recs: recs, ver: ver,
		nextUpd: 100000001, nextMsg: 1,
		failures: map[string][]Failure{}, calls: map[string]int{}, notify: make(chan struct{}),
	}, nil
}

func (s *Server) template(name string) (map[string]any, error) {
	r, err := testkit.LoadRecording(s.recs, origin, s.ver, name)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(r.Body, &m); err != nil {
		return nil, fmt.Errorf("recording %s: %w", name, err)
	}
	return m, nil
}

// UserSendsText queues a text message from u in chat and returns the
// update_id. The update has the recorded format.
func (s *Server) UserSendsText(u User, chat Chat, text string) (int64, error) {
	name := "update-message-private"
	if chat.Type != "private" {
		name = "update-message-group"
	}
	upd, err := s.template(name)
	if err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	msg, _ := upd["message"].(map[string]any)
	from, _ := msg["from"].(map[string]any)
	ch, _ := msg["chat"].(map[string]any)
	id := s.nextUpd
	s.nextUpd++
	upd["update_id"] = id
	msg["message_id"] = s.nextMsg
	s.nextMsg++
	msg["date"] = s.clk.Now().Unix()
	msg["text"] = text
	from["id"], from["first_name"], from["username"] = u.ID, u.FirstName, u.Username
	ch["id"] = chat.ID
	if chat.Type == "private" {
		ch["first_name"], ch["username"] = u.FirstName, u.Username
	} else {
		ch["title"], ch["type"] = chat.Title, chat.Type
	}
	s.updates = append(s.updates, upd)
	close(s.notify)
	s.notify = make(chan struct{})
	return id, nil
}

// Sent returns what the bot sent so far.
func (s *Server) Sent() []Sent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Sent(nil), s.sent...)
}

// FailNext makes the next call to method fail with f.
func (s *Server) FailNext(method string, f Failure) {
	s.mu.Lock()
	s.failures[method] = append(s.failures[method], f)
	s.mu.Unlock()
}

// ServeHTTP routes /bot<token>/<method>.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rest, ok := strings.CutPrefix(r.URL.Path, "/bot")
	tok, method, ok2 := strings.Cut(rest, "/")
	if !ok || !ok2 {
		testkit.NotImplemented(s.rep, w, r.Method+" "+r.URL.Path)
		return
	}
	// The method comes before the token: a method the double does not know
	// fails the test instead of looking like a 401.
	allowed, known := knownParams[method]
	if !known {
		testkit.NotImplemented(s.rep, w, "telegram "+method)
		return
	}
	if tok != s.token {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"ok": false, "error_code": 401, "description": "Unauthorized"})
		return
	}
	s.mu.Lock()
	s.calls[method]++
	s.mu.Unlock()
	if s.injectFailure(w, method) {
		return
	}
	params, err := readParams(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error_code": 400, "description": "Bad Request: " + err.Error()})
		return
	}
	for _, name := range sortedKeys(params) {
		if !allowed[name] {
			testkit.NotImplemented(s.rep, w, "telegram "+method+" parameter "+name)
			return
		}
	}
	switch method {
	case "getUpdates":
		s.getUpdates(w, r, params)
	case "sendMessage", "editMessageText":
		s.sendMessage(w, method, params)
	case "answerCallbackQuery":
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "result": true})
	}
}

// knownParams are the methods the double serves and the parameters of each
// that it honours. Anything else fails the test as not implemented (buttons,
// replies, parse modes and files arrive with the bot in M5).
var knownParams = map[string]map[string]bool{
	"getUpdates":          {"offset": true, "timeout": true},
	"sendMessage":         {"chat_id": true, "text": true},
	"editMessageText":     {"chat_id": true, "message_id": true, "text": true},
	"answerCallbackQuery": {"callback_query_id": true},
}

func sortedKeys(m map[string]any) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

func (s *Server) injectFailure(w http.ResponseWriter, method string) bool {
	s.mu.Lock()
	fs := s.failures[method]
	if len(fs) == 0 {
		s.mu.Unlock()
		return false
	}
	f := fs[0]
	s.failures[method] = fs[1:]
	s.mu.Unlock()
	if f.Cut {
		panic(http.ErrAbortHandler)
	}
	body := map[string]any{"ok": false, "error_code": f.Status, "description": http.StatusText(f.Status)}
	if f.Status == http.StatusTooManyRequests {
		if rec, err := s.template("error-429"); err == nil {
			body = rec // recorded shape, with this failure's values
		}
		body["description"] = fmt.Sprintf("Too Many Requests: retry after %d", f.RetryAfter)
		body["parameters"] = map[string]any{"retry_after": f.RetryAfter}
	}
	writeJSON(w, f.Status, body)
	return true
}

func (s *Server) getUpdates(w http.ResponseWriter, r *http.Request, p map[string]any) {
	offset := num(p["offset"])
	timeout := time.Duration(num(p["timeout"])) * time.Second
	var t clock.Timer
	if timeout > 0 {
		t = s.clk.NewTimer(timeout)
		defer t.Stop()
	}
	for {
		s.mu.Lock()
		if offset > 0 { // confirm everything before offset
			keep := s.updates[:0]
			for _, u := range s.updates {
				if num(u["update_id"]) >= offset {
					keep = append(keep, u)
				}
			}
			s.updates = keep
		}
		if len(s.updates) > 0 || t == nil {
			res := append(make([]map[string]any, 0, len(s.updates)), s.updates...)
			s.mu.Unlock()
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "result": res})
			return
		}
		notify := s.notify
		s.mu.Unlock()
		select {
		case <-notify:
		case <-t.C():
			t = nil
		case <-r.Context().Done():
			return
		}
	}
}

func (s *Server) sendMessage(w http.ResponseWriter, method string, p map[string]any) {
	chatID := num(p["chat_id"])
	text, _ := p["text"].(string)
	if chatID == 0 || text == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error_code": 400, "description": "Bad Request: chat_id and text are required"})
		return
	}
	msg, err := s.template("send-message-result")
	if err != nil {
		s.rep.Errorf("faketelegram: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.mu.Lock()
	id := num(p["message_id"])
	if method == "sendMessage" {
		id = s.nextMsg
		s.nextMsg++
	}
	s.sent = append(s.sent, Sent{Method: method, ChatID: chatID, MessageID: id, Text: text, Params: p})
	s.mu.Unlock()
	msg["message_id"], msg["date"], msg["text"] = id, s.clk.Now().Unix(), text
	if ch, ok := msg["chat"].(map[string]any); ok {
		ch["id"] = chatID
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "result": msg})
}

func readParams(r *http.Request) (map[string]any, error) {
	p := map[string]any{}
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			return nil, fmt.Errorf("decode JSON body: %w", err)
		}
		return p, nil
	}
	if err := r.ParseForm(); err != nil {
		return nil, fmt.Errorf("parse form: %w", err)
	}
	for k, v := range r.Form {
		p[k] = v[0]
	}
	return p, nil
}

func num(v any) int64 {
	switch x := v.(type) {
	case float64:
		return int64(x)
	case int64:
		return x
	case int:
		return int64(x)
	case string:
		n, _ := strconv.ParseInt(x, 10, 64)
		return n
	}
	return 0
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// Calls is how many times the method was called (the failed ones too): what a
// Citadel that must not call the Telegram did not do.
func (s *Server) Calls(method string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[method]
}
