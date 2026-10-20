// Package faketelegram is the in-memory Telegram Bot API of every suite
// (docs/qualidade/dubles.md#telegram-falso). M0 serves the text flow:
// getUpdates (long polling timed by the clock), sendMessage,
// editMessageText and answerCallbackQuery, with injected failures. The bot of
// M5 adds the buttons (reply_markup, callback clicks), parse modes, replies,
// deleteMessage, deleteWebhook, setMyCommands, leaveChat and documents, and the
// stickers a user may send, and the attachments: voice, photos and documents with
// their file_size, getFile and the download of the file.
package faketelegram

import (
	"encoding/json"
	"fmt"
	"io"
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

// Deleted is a message the bot deleted.
type Deleted struct{ ChatID, MessageID int64 }

// Failure is injected into the next call of a method.
type Failure struct {
	Status     int // HTTP status and error_code, e.g. 429 or 502
	RetryAfter int // seconds, for 429
	// Description replaces the standard text of the error (e.g. a parse error).
	Description string
	Cut         bool // the connection drops without a response
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
	pollGen  int // the long getUpdates in progress; a newer one aborts the older with 409
	deleted  []Deleted
	failures map[string][]Failure
	files    map[string][]byte // file_id → content, for getFile and the download
	notify   chan struct{}     // closed and replaced on each new update
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
		failures: map[string][]Failure{}, calls: map[string]int{}, files: map[string][]byte{}, notify: make(chan struct{}),
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
	return s.userSendsText(u, chat, text, 0)
}

func (s *Server) userSendsText(u User, chat Chat, text string, replyTo int64) (int64, error) {
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
	if replyTo != 0 {
		msg["reply_to_message"] = map[string]any{"message_id": replyTo, "date": s.clk.Now().Unix(), "chat": map[string]any{"id": chat.ID, "type": chat.Type},
			"from": map[string]any{"id": int64(1), "is_bot": true, "first_name": "Mestre"}, "text": "."}
	}
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

// UserRepliesText is UserSendsText as a reply to a message of the bot (the
// threaded reply): the update carries reply_to_message with that message_id.
func (s *Server) UserRepliesText(u User, chat Chat, text string, replyTo int64) (int64, error) {
	return s.userSendsText(u, chat, text, replyTo)
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
	if rest, ok := strings.CutPrefix(r.URL.Path, "/file/bot"); ok {
		s.serveFile(w, rest)
		return
	}
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
	case "sendDocument":
		s.sendDocument(w, params)
	case "getFile":
		s.getFile(w, params)
	case "answerCallbackQuery":
		s.mu.Lock()
		s.sent = append(s.sent, Sent{Method: method, Text: str(params["text"]), Params: params})
		s.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "result": true})
	case "deleteMessage":
		s.mu.Lock()
		s.deleted = append(s.deleted, Deleted{ChatID: num(params["chat_id"]), MessageID: num(params["message_id"])})
		s.sent = append(s.sent, Sent{Method: method, ChatID: num(params["chat_id"]), MessageID: num(params["message_id"]), Params: params})
		s.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "result": true})
	default: // deleteWebhook, setMyCommands, leaveChat
		s.mu.Lock()
		s.sent = append(s.sent, Sent{Method: method, ChatID: num(params["chat_id"]), Params: params})
		s.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "result": true})
	}
}

// knownParams are the methods the double serves and the parameters of each
// that it honours. Anything else fails the test as not implemented (a method
// or a parameter the bot does not use yet is added when a test needs it).
var knownParams = map[string]map[string]bool{
	"getUpdates":          {"offset": true, "timeout": true, "allowed_updates": true},
	"sendMessage":         {"chat_id": true, "text": true, "parse_mode": true, "reply_markup": true, "reply_to_message_id": true},
	"editMessageText":     {"chat_id": true, "message_id": true, "text": true, "parse_mode": true, "reply_markup": true},
	"answerCallbackQuery": {"callback_query_id": true, "text": true, "show_alert": true},
	"deleteMessage":       {"chat_id": true, "message_id": true},
	"deleteWebhook":       {"drop_pending_updates": true},
	"setMyCommands":       {"commands": true},
	"leaveChat":           {"chat_id": true},
	"sendDocument":        {"chat_id": true, "document": true, "caption": true, "parse_mode": true},
	"getFile":             {"file_id": true},
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
	if f.Description != "" {
		body["description"] = f.Description
	}
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
	gen := 0
	if timeout > 0 {
		t = s.clk.NewTimer(timeout)
		defer t.Stop()
		s.mu.Lock()
		s.pollGen++
		gen = s.pollGen
		close(s.notify) // wakes the older long poll, which finds itself replaced
		s.notify = make(chan struct{})
		s.mu.Unlock()
	}
	for {
		s.mu.Lock()
		if gen != 0 && gen != s.pollGen {
			s.mu.Unlock()
			writeJSON(w, http.StatusConflict, map[string]any{"ok": false, "error_code": 409,
				"description": "Conflict: terminated by other getUpdates request; make sure that only one bot instance is running"})
			return
		}
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
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			return nil, fmt.Errorf("parse multipart: %w", err)
		}
		for k, v := range r.MultipartForm.Value {
			p[k] = v[0]
		}
		for k, fh := range r.MultipartForm.File {
			f, err := fh[0].Open()
			if err != nil {
				return nil, err
			}
			b, err := io.ReadAll(f)
			f.Close()
			if err != nil {
				return nil, err
			}
			p[k] = File{Name: fh[0].Filename, Content: b}
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

// LongPolls is how many long getUpdates have been registered (waiting for
// updates, replacing the one before). Calls counts a call when it arrives, a
// moment before it is registered: a test that needs "the older poll is in
// place" waits for this.
func (s *Server) LongPolls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pollGen
}

// File is a file uploaded to the bot API (the document of sendDocument).
type File struct {
	Name    string
	Content []byte
}

func str(v any) string { s, _ := v.(string); return s }

func (s *Server) sendDocument(w http.ResponseWriter, p map[string]any) {
	f, ok := p["document"].(File)
	chatID := num(p["chat_id"])
	if !ok || chatID == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error_code": 400, "description": "Bad Request: chat_id and a document file are required"})
		return
	}
	s.mu.Lock()
	id := s.nextMsg
	s.nextMsg++
	s.sent = append(s.sent, Sent{Method: "sendDocument", ChatID: chatID, MessageID: id, Text: str(p["caption"]), Params: p})
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "result": map[string]any{"message_id": id, "date": s.clk.Now().Unix(),
		"chat": map[string]any{"id": chatID, "type": "private"}, "document": map[string]any{"file_name": f.Name, "file_size": len(f.Content)}}})
}

// Deleted returns the messages the bot deleted.
func (s *Server) Deleted() []Deleted {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Deleted(nil), s.deleted...)
}

// UserSendsSticker queues a sticker from u in the private chat with them.
func (s *Server) UserSendsSticker(u User) (int64, error) {
	upd, err := s.template("update-message-sticker")
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
	from["id"], from["first_name"], from["username"] = u.ID, u.FirstName, u.Username
	ch["id"] = u.ID
	s.push(upd)
	return id, nil
}

// UserSendsDocument queues a document from u in the private chat with them. The
// update is the sticker one with the sticker replaced by a document (the Bot API
// shape; it is not a recording).
func (s *Server) UserSendsDocument(u User, name string) (int64, error) {
	return s.userSendsMedia(u, "document", map[string]any{"file_name": name, "mime_type": "application/pdf", "file_id": "doc-" + name, "file_unique_id": "u-" + name, "file_size": 1234})
}

// UserSendsVoice queues a voice message from u in the private chat with them
// (the same hand-made shape).
func (s *Server) UserSendsVoice(u User) (int64, error) {
	return s.userSendsMedia(u, "voice", map[string]any{"duration": 3, "mime_type": "audio/ogg", "file_id": "voice-1", "file_unique_id": "u-voice-1", "file_size": 4321})
}

func (s *Server) userSendsMedia(u User, key string, media map[string]any) (int64, error) {
	return s.userSendsMediaCaption(u, key, media, "")
}

func (s *Server) userSendsMediaCaption(u User, key string, media any, caption string) (int64, error) {
	upd, err := s.template("update-message-sticker")
	if err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	msg, _ := upd["message"].(map[string]any)
	from, _ := msg["from"].(map[string]any)
	ch, _ := msg["chat"].(map[string]any)
	delete(msg, "sticker")
	msg[key] = media
	if caption != "" {
		msg["caption"] = caption
	}
	id := s.nextUpd
	s.nextUpd++
	upd["update_id"] = id
	msg["message_id"] = s.nextMsg
	s.nextMsg++
	msg["date"] = s.clk.Now().Unix()
	from["id"], from["first_name"], from["username"] = u.ID, u.FirstName, u.Username
	ch["id"] = u.ID
	s.push(upd)
	return id, nil
}

// UserClicks queues the click of u on the button with data, of the message
// messageID of the private chat with them.
func (s *Server) UserClicks(u User, messageID int64, data string) (callbackID string, err error) {
	upd, err := s.template("update-callback-query")
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cq, _ := upd["callback_query"].(map[string]any)
	from, _ := cq["from"].(map[string]any)
	msg, _ := cq["message"].(map[string]any)
	ch, _ := msg["chat"].(map[string]any)
	id := s.nextUpd
	s.nextUpd++
	upd["update_id"] = id
	callbackID = fmt.Sprintf("cb%d", id)
	cq["id"], cq["data"] = callbackID, data
	from["id"], from["first_name"], from["username"] = u.ID, u.FirstName, u.Username
	msg["message_id"] = messageID
	ch["id"] = u.ID
	s.push(upd)
	return callbackID, nil
}

// push appends an update and wakes the pending getUpdates. The caller holds mu.
func (s *Server) push(upd map[string]any) {
	s.updates = append(s.updates, upd)
	close(s.notify)
	s.notify = make(chan struct{})
}

// AddFile makes a file the bot can get: getFile answers with its path and size, and
// the download serves its content. The id is the file_id a message carries.
func (s *Server) AddFile(id string, content []byte) {
	s.mu.Lock()
	s.files[id] = append([]byte(nil), content...)
	s.mu.Unlock()
}

func (s *Server) getFile(w http.ResponseWriter, p map[string]any) {
	id := str(p["file_id"])
	s.mu.Lock()
	content, ok := s.files[id]
	s.mu.Unlock()
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error_code": 400, "description": "Bad Request: wrong file_id or the file is temporarily unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "result": map[string]any{"file_id": id, "file_unique_id": "u-" + id, "file_size": len(content), "file_path": "files/" + id}})
}

// serveFile serves /file/bot<token>/files/<file_id>.
func (s *Server) serveFile(w http.ResponseWriter, rest string) {
	tok, path, ok := strings.Cut(rest, "/")
	id, isFile := strings.CutPrefix(path, "files/")
	if !ok || !isFile {
		testkit.NotImplemented(s.rep, w, "telegram file "+path)
		return
	}
	s.mu.Lock()
	s.calls["download"]++
	fail := s.failures["download"]
	if len(fail) > 0 {
		s.failures["download"] = fail[1:]
	}
	content, found := s.files[id]
	s.mu.Unlock()
	switch {
	case tok != s.token:
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
	case len(fail) > 0:
		http.Error(w, http.StatusText(fail[0].Status), fail[0].Status)
	case !found:
		http.Error(w, "Not Found", http.StatusNotFound)
	default:
		_, _ = w.Write(content)
	}
}

// Media is a file a user sends: the file_id the message carries, the size the
// message declares (the content may be anything, or nothing), and the rest of what
// the Bot API tells.
type Media struct {
	FileID   string
	Name     string
	MIME     string
	Size     int64
	Duration int
	Caption  string
}

// UserSendsFile queues a message of u with a file of the kind document, voice or
// photo. It is the same hand-made shape as UserSendsDocument.
func (s *Server) UserSendsFile(u User, kind string, m Media) (int64, error) {
	media := map[string]any{"file_id": m.FileID, "file_unique_id": "u-" + m.FileID, "file_size": m.Size}
	if m.MIME != "" {
		media["mime_type"] = m.MIME
	}
	if m.Name != "" {
		media["file_name"] = m.Name
	}
	if m.Duration > 0 {
		media["duration"] = m.Duration
	}
	var v any = media
	if kind == "photo" {
		// a photo comes in several sizes, the smallest first
		small := map[string]any{"file_id": m.FileID + "-small", "file_unique_id": "u-" + m.FileID + "-small", "file_size": 100, "width": 90, "height": 90}
		big := map[string]any{"file_id": m.FileID, "file_unique_id": "u-" + m.FileID, "file_size": m.Size, "width": 1280, "height": 960}
		v = []any{small, big}
	}
	return s.userSendsMediaCaption(u, kind, v, m.Caption)
}
