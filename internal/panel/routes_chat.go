package panel

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"

	"github.com/mestre-ia/mestre/internal/users"
)

// The conversation with the Grão-Mestre (docs/painel/conversa-e-aprovacoes.md#conversa-com-o-grão-mestre).
// The panel is one more channel of the inbox: a message goes to the inbox of the Grão-Mestre of
// the workspace with the origin "painel", and the answer of `mestre reply` comes back to the
// user who wrote it. Each user gets only his own conversation: the server takes the user from
// the session, checks the level and the workspace in every request, and masks every text it
// returns. The browser only hides what is not allowed.

func init() { RegisterArea("chat", mountChat) }

// The codes of the errors of the conversation.
const (
	CodeChatNoGrandMaster Code = "chat_no_grand_master"
	CodeChatFileTooLarge  Code = "chat_file_too_large"
	CodeChatTooManyFiles  Code = "chat_too_many_files"
	CodeChatBlocked       Code = "chat_blocked"
	CodeChatBusy          Code = "chat_busy"
	CodeChatEmpty         Code = "chat_empty"
	CodeChatTooLong       Code = "chat_too_long"
	CodeChatQuoteUnknown  Code = "chat_quote_unknown"
	CodeChatChoiceUnknown Code = "chat_choice_unknown"
	CodeChatChoiceTaken   Code = "chat_choice_answered"
	CodeChatChoiceLost    Code = "chat_choice_not_delivered"
	CodeChatFileGone      Code = "chat_file_unavailable"
	CodeChatMalformed     Code = "chat_malformed"
)

func init() {
	for c, s := range map[Code]int{
		CodeChatNoGrandMaster: http.StatusConflict,
		CodeChatFileTooLarge:  http.StatusRequestEntityTooLarge,
		CodeChatTooManyFiles:  http.StatusUnprocessableEntity,
		CodeChatBlocked:       http.StatusTooManyRequests,
		CodeChatBusy:          http.StatusTooManyRequests,
		CodeChatEmpty:         http.StatusUnprocessableEntity,
		CodeChatTooLong:       http.StatusUnprocessableEntity,
		CodeChatQuoteUnknown:  http.StatusNotFound,
		CodeChatChoiceUnknown: http.StatusNotFound,
		CodeChatChoiceTaken:   http.StatusConflict,
		CodeChatChoiceLost:    http.StatusBadGateway,
		CodeChatFileGone:      http.StatusNotFound,
		CodeChatMalformed:     http.StatusBadRequest,
	} {
		status[c] = s
	}
}

// ChatEvent is what the conversation tells the panel to publish: to one user, in one
// workspace.
type ChatEvent struct {
	User      string
	Workspace string
	// Kind is EventChatChanged or EventChatStatus (Status is then set).
	Kind   string
	Status *ChatStatus
}

// ChatDraftInput is what a message starts with; the files come after.
type ChatDraftInput struct {
	Workspace string
	Text      string
	// ClientID is the id the browser made for the message: sending it twice puts it once
	// in the inbox.
	ClientID string
	// Quote is the id of the entry the message answers ("" for none).
	Quote string
	// Session is the session of the panel the message was written in.
	Session string
	// Language is the language the panel is open in (the tag of the browser or the
	// one chosen), a hint for the language of the Grão-Mestre when the user chose none.
	Language string
}

// ChatDraft is a message being received: its files, then the send.
type ChatDraft interface {
	// AddFile keeps a file of the message under the rules of the attachments.
	AddFile(ctx context.Context, name, mimeType string, r io.Reader) error
	// Send puts the message in the inbox of the Grão-Mestre.
	Send(ctx context.Context) (ChatSent, error)
	// Abort gives up the message: what was kept for it is taken back. Every draft ends in Send or Abort.
	Abort(ctx context.Context)
}

// ChatDownload is a file the Grão-Mestre sent.
type ChatDownload struct {
	Name string
	Size int64
	Body io.ReadCloser
}

// ChatSource is what the conversation needs of the engine. Its errors are *APIError when
// the user should be told, anything else is an internal error.
type ChatSource interface {
	// Limits are the biggest attachment, in bytes, and how many go in a message.
	Limits() (perFile int64, maxFiles int)
	Overview(ctx context.Context, u users.User) (ChatOverview, error)
	Conversation(ctx context.Context, u users.User, workspace string) (ChatConversation, error)
	// Begin checks the level, the workspace, the quote and the text of a message and returns
	// the draft that takes its files.
	Begin(ctx context.Context, u users.User, in ChatDraftInput) (ChatDraft, error)
	// Read marks the answers of the workspace as read by the user.
	Read(ctx context.Context, u users.User, workspace string) error
	// Choose gives the answer of the user to a question of `mestre choose`, once.
	// language is the language the panel is open in ("" when it did not say).
	Choose(ctx context.Context, u users.User, question int64, option int, language string) error
	// File opens a file the Grão-Mestre sent to the user.
	File(ctx context.Context, u users.User, workspace string, answer int64, index int) (ChatDownload, error)
	// Listen registers what the source tells the panel to publish; the returned function
	// ends it.
	Listen(fn func(ChatEvent)) (cancel func())
	// Unread is the count of unread answers of the user, for the snapshot of the topic.
	Unread(ctx context.Context, u users.User) (int, error)
}

func mountChat(r *Router) {
	p := r.Panel()
	r.Auth("GET /api/chat", p.handleChatOverview)
	r.Auth("GET /api/chat/{workspace}", p.handleChat)
	r.Auth("POST /api/chat/{workspace}/read", p.handleChatRead)
	r.Auth("GET /api/chat/{workspace}/files/{answer}/{index}", p.handleChatFile)
	r.Require("POST /api/chat-choices/{id}", p.handleChatChoose, users.TalkToGrandMaster)
	// the body of a message is its text and its files: the limit is of the source
	big := int64(maxBody)
	if p.chat != nil {
		per, n := p.chat.Limits()
		big = per*int64(n) + maxBody
	}
	r.RequireLimit("POST /api/chat/{workspace}/messages", big, p.handleChatSend, users.TalkToGrandMaster)
	p.hub.Provide(ChatTopic, Topic{
		Authorize: func(u users.User, id string) bool { return u.ID == id },
		Snapshot: func(ctx context.Context, u users.User, _ string) (any, error) {
			if p.chat == nil {
				return ChatSnapshot{}, nil
			}
			n, err := p.chat.Unread(ctx, u)
			return ChatSnapshot{Unread: n}, err
		},
	})
}

// listenChat publishes what the conversation says to the sockets of the user it is for.
func (p *Panel) listenChat() {
	if p.chat == nil {
		return
	}
	p.stop = append(p.stop, p.chat.Listen(func(e ChatEvent) {
		topic := ChatTopic + ":" + e.User
		switch e.Kind {
		case EventChatStatus:
			if e.Status != nil {
				_ = p.hub.Publish(topic, EventChatStatus, ChatStatusEvent{Workspace: e.Workspace, Status: *e.Status})
			}
		default:
			_ = p.hub.Publish(topic, EventChatChanged, ChatChangedEvent{Workspace: e.Workspace})
		}
	}))
}

func (p *Panel) needChat(w http.ResponseWriter) bool {
	if p.chat == nil {
		WriteError(w, Err(CodeUnavailable, "a conversa não está disponível agora", nil))
		return false
	}
	return true
}

// chatWorkspace is the workspace of the path when the user acts in it; a workspace that
// does not exist and one the user does not act in get the same answer.
func (p *Panel) chatWorkspace(w http.ResponseWriter, r *http.Request) (string, bool) {
	ws := r.PathValue("workspace")
	if ws == "" || !UserOf(r).Acts(ws) {
		WriteError(w, Err(CodeNotFound, "não encontrado", nil))
		return "", false
	}
	return ws, true
}

// writeChatError answers an error of the source: the ones with a code as they are.
func (p *Panel) writeChatError(w http.ResponseWriter, what string, err error) {
	var api *APIError
	if errors.As(err, &api) {
		WriteError(w, api)
		return
	}
	p.log.Error("panel: "+what, "err", err)
	WriteError(w, errInternal())
}

// handleChatOverview is GET /api/chat: the workspaces the user may talk in, with the
// unread of each.
func (p *Panel) handleChatOverview(w http.ResponseWriter, r *http.Request) {
	if !p.needChat(w) {
		return
	}
	o, err := p.chat.Overview(r.Context(), UserOf(r))
	if err != nil {
		p.writeChatError(w, "reading the workspaces of the conversation", err)
		return
	}
	WriteJSON(w, http.StatusOK, o)
}

// handleChat is GET /api/chat/{workspace}: the conversation of the user, and only his.
func (p *Panel) handleChat(w http.ResponseWriter, r *http.Request) {
	if !p.needChat(w) {
		return
	}
	ws, ok := p.chatWorkspace(w, r)
	if !ok {
		return
	}
	c, err := p.chat.Conversation(r.Context(), UserOf(r), ws)
	if err != nil {
		p.writeChatError(w, "reading the conversation", err)
		return
	}
	WriteJSON(w, http.StatusOK, c)
}

// handleChatRead is POST /api/chat/{workspace}/read: the user opened the conversation.
func (p *Panel) handleChatRead(w http.ResponseWriter, r *http.Request) {
	if !p.needChat(w) {
		return
	}
	ws, ok := p.chatWorkspace(w, r)
	if !ok {
		return
	}
	if err := p.chat.Read(r.Context(), UserOf(r), ws); err != nil {
		p.writeChatError(w, "marking the conversation as read", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// maxChatField is the most a text field of a message may be: the text has its own limit
// in the source, this one only keeps a field from filling the memory.
const maxChatField = 256 << 10

// handleChatSend is POST /api/chat/{workspace}/messages: multipart, with the fields text,
// client_id and quote first and then the files. Nothing is kept before the user, the
// workspace, the quote and the text are checked.
func (p *Panel) handleChatSend(w http.ResponseWriter, r *http.Request) {
	if !p.needChat(w) {
		return
	}
	ws, ok := p.chatWorkspace(w, r)
	if !ok {
		return
	}
	mr, err := r.MultipartReader()
	if err != nil {
		WriteError(w, Err(CodeChatMalformed, "mensagem malformada", nil))
		return
	}
	u := UserOf(r)
	in := ChatDraftInput{Workspace: ws, Session: SessionOf(r).ID}
	var draft ChatDraft
	sent := false
	defer func() {
		if draft != nil && !sent {
			draft.Abort(r.Context())
		}
	}()
	begin := func() bool {
		if draft != nil {
			return true
		}
		d, err := p.chat.Begin(r.Context(), u, in)
		if err != nil {
			p.writeChatError(w, "starting a message", err)
			return false
		}
		draft = d
		return true
	}
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			p.chatBodyError(w, err)
			return
		}
		name := part.FormName()
		if part.FileName() == "" {
			if draft != nil { // a field after the first file: the order is part of the contract
				WriteError(w, Err(CodeChatMalformed, "mensagem malformada", nil))
				return
			}
			val, err := io.ReadAll(io.LimitReader(part, maxChatField+1))
			if err != nil {
				p.chatBodyError(w, err)
				return
			}
			if len(val) > maxChatField {
				WriteError(w, Err(CodeChatTooLong, "mensagem longa demais", nil))
				return
			}
			switch name {
			case "text":
				in.Text = string(val)
			case "client_id":
				in.ClientID = string(val)
			case "quote":
				in.Quote = string(val)
			case "language":
				in.Language = string(val)
			}
			continue
		}
		if name != "files" {
			WriteError(w, Err(CodeChatMalformed, "mensagem malformada", nil))
			return
		}
		if !begin() {
			return
		}
		mt, _, _ := mime.ParseMediaType(part.Header.Get("Content-Type"))
		if err := draft.AddFile(r.Context(), part.FileName(), mt, part); err != nil {
			var big *http.MaxBytesError
			if errors.As(err, &big) {
				p.chatBodyError(w, err)
				return
			}
			p.writeChatError(w, "keeping a file of a message", err)
			return
		}
	}
	if !begin() {
		return
	}
	// the files are read: only now does the message change the Guild, so only now is the gate of the handover held
	r, release, held := p.holdRequest(w, r)
	if !held {
		return // the draft is given up by the defer above
	}
	defer release()
	res, err := draft.Send(r.Context())
	sent = true // Send gives the draft up itself, with or without an error
	if err != nil {
		p.writeChatError(w, "sending a message", err)
		return
	}
	WriteJSON(w, http.StatusCreated, res)
}

// chatBodyError answers a body that could not be read: too big, or broken.
func (p *Panel) chatBodyError(w http.ResponseWriter, err error) {
	var big *http.MaxBytesError
	if errors.As(err, &big) {
		per, _ := p.chat.Limits()
		WriteError(w, Err(CodeChatFileTooLarge, "arquivo grande demais", map[string]any{"limit": per}))
		return
	}
	var api *APIError
	if errors.As(err, &api) {
		WriteError(w, api)
		return
	}
	p.log.Warn("panel: reading a message", "err", err)
	WriteError(w, Err(CodeChatMalformed, "mensagem malformada", nil))
}

// handleChatChoose is POST /api/chat-choices/{id}: the click on an option of a question of
// the Grão-Mestre. The answer goes back once; a second click is refused.
func (p *Panel) handleChatChoose(w http.ResponseWriter, r *http.Request) {
	if !p.needChat(w) {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var body ChatChoice
	if err != nil || id <= 0 || json.NewDecoder(r.Body).Decode(&body) != nil {
		WriteError(w, errBadRequest())
		return
	}
	if err := p.chat.Choose(r.Context(), UserOf(r), id, body.Option, body.Language); err != nil {
		p.writeChatError(w, "answering a question", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleChatFile is GET /api/chat/{workspace}/files/{answer}/{index}: a file the Grão-Mestre
// sent to the user, as a download that is never shown in the page.
func (p *Panel) handleChatFile(w http.ResponseWriter, r *http.Request) {
	if !p.needChat(w) {
		return
	}
	ws, ok := p.chatWorkspace(w, r)
	if !ok {
		return
	}
	id, err1 := strconv.ParseInt(r.PathValue("answer"), 10, 64)
	idx, err2 := strconv.Atoi(r.PathValue("index"))
	if err1 != nil || err2 != nil || id <= 0 || idx < 0 {
		WriteError(w, errBadRequest())
		return
	}
	f, err := p.chat.File(r.Context(), UserOf(r), ws, id, idx)
	if err != nil {
		p.writeChatError(w, "opening a file of the conversation", err)
		return
	}
	defer f.Body.Close()
	h := w.Header()
	h.Set("Content-Type", "application/octet-stream")
	h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": f.Name}))
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "sandbox; default-src 'none'")
	h.Set("Cache-Control", "private, no-store")
	if f.Size > 0 {
		h.Set("Content-Length", strconv.FormatInt(f.Size, 10))
	}
	_, _ = io.Copy(w, f.Body)
}
