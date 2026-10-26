package panelchat

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/mestre-ia/mestre/internal/attachments"
	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/orchestration/inbox"
	"github.com/mestre-ia/mestre/internal/panel"
	"github.com/mestre-ia/mestre/internal/secretscreen"
	"github.com/mestre-ia/mestre/internal/users"
)

func isNoRows(err error) bool { return errors.Is(err, sql.ErrNoRows) }

// What the Grão-Mestre gets in the place of a message with a password phrase: the notice,
// never the content (the same rule as the Telegram's).
const HeldNotice = "[mensagem retida: o usuário mandou uma senha pelo painel; o motor não a entregou. " +
	"Recuse, peça o cadastro pela tela de Segredos e recomende trocar a senha.]"

const (
	// secretHitLimit secrets in secretWindow block the sending of the user until the oldest
	// attempt leaves the window: the filter must not be a way to ask the engine, again and
	// again, whether a text is a secret it knows.
	secretHitLimit = 5
	secretWindow   = 10 * time.Minute
	// followUpLife is how long after a held phrase the next message of the user is held too.
	followUpLife = 5 * time.Minute
	// maxUploads is how many messages of one user may be taking files at once.
	maxUploads = 2
)

// allowSend counts a message of the user against the minute; false when there were too many.
func (s *Service) allowSend(user string) bool {
	now := s.clk.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	recent := s.sends[user][:0]
	for _, t := range s.sends[user] {
		if now.Sub(t) < time.Minute {
			recent = append(recent, t)
		}
	}
	if len(recent) >= sendsPerMinute {
		s.sends[user] = recent
		return false
	}
	s.sends[user] = append(recent, now)
	return true
}

// recent keeps the moments that are still in the window.
func recent(list []time.Time, now time.Time, window time.Duration) []time.Time {
	out := list[:0]
	for _, t := range list {
		if now.Sub(t) < window {
			out = append(out, t)
		}
	}
	return out
}

// secretBlocked says the user already had too many secrets caught: nothing he sends is
// looked at or accepted until the window passes.
func (s *Service) secretBlocked(user string) bool {
	now := s.clk.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hits[user] = recent(s.hits[user], now, secretWindow)
	return len(s.hits[user]) >= secretHitLimit
}

// followUpOpen says whether the next message of the user is still to be held, and ends the
// wait (it is one message).
func (s *Service) followUpOpen(user string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	until, ok := s.followUp[user]
	if !ok {
		return false
	}
	delete(s.followUp, user)
	return s.clk.Now().Before(until)
}

// secretCaught is what the engine does when a secret arrives: the attempt goes to the audit
// (who, where, when: nothing of the text), a phrase starts the wait for its value, and the
// fifth attempt of the window tells the Owner (once for the window).
func (s *Service) secretCaught(ctx context.Context, u users.User, kind secretscreen.Kind) {
	now := s.clk.Now()
	s.mu.Lock()
	s.hits[u.ID] = append(recent(s.hits[u.ID], now, secretWindow), now)
	n := len(s.hits[u.ID])
	if kind == secretscreen.Passphrase {
		s.followUp[u.ID] = now.Add(followUpLife)
	}
	alert := n >= secretHitLimit && now.Sub(s.alerted[u.ID]) >= secretWindow
	if alert {
		s.alerted[u.ID] = now
	}
	s.mu.Unlock()
	if s.o.AuditSecret != nil {
		s.o.AuditSecret(ctx, u.ID, string(kind))
	}
	if alert && s.o.AlertOwner != nil {
		s.o.AlertOwner(ctx, fmt.Sprintf("%s mandou segredos pelo painel %d vezes em %d minutos; o envio dele pela conversa está bloqueado até a janela passar. Vale conversar com ele.",
			u.Name, n, int(secretWindow/time.Minute)))
	}
}

// acquireUpload takes one of the places of the user; false when he has all of them.
func (s *Service) acquireUpload(user string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.uploads[user] >= maxUploads {
		return false
	}
	s.uploads[user]++
	return true
}

func (s *Service) releaseUpload(user string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.uploads[user]--; s.uploads[user] <= 0 {
		delete(s.uploads, user)
	}
}

// quote resolves the entry a message answers. Only an entry of the conversation of this very
// user, in this very workspace, is one: the table the Grão-Mestre is told comes from here,
// never from the browser.
func (s *Service) quote(ctx context.Context, u users.User, workspace, id string) (table string, err error) {
	bad := panel.Err(panel.CodeChatQuoteUnknown, "a mensagem citada não existe", nil)
	switch {
	case strings.HasPrefix(id, prefixIn):
		it, err := s.o.Inbox.UserItem(ctx, workspace, u.ID, "m"+strings.TrimPrefix(id, prefixIn))
		if err != nil {
			return "", bad
		}
		return it.Origin.Table, nil
	case strings.HasPrefix(id, prefixOut):
		n, err := strconv.ParseInt(strings.TrimPrefix(id, prefixOut), 10, 64)
		if err != nil {
			return "", bad
		}
		o, err := s.o.Inbox.OutboundGet(ctx, n)
		if err != nil || o.Workspace != workspace || o.User != u.ID {
			return "", bad
		}
		if it, err := s.o.Inbox.UserItem(ctx, workspace, u.ID, o.Message); err == nil { // the table of the message it answered
			return it.Origin.Table, nil
		}
		return "", nil
	case strings.HasPrefix(id, prefixAsk):
		n, err := strconv.ParseInt(strings.TrimPrefix(id, prefixAsk), 10, 64)
		if err != nil {
			return "", bad
		}
		var mesa string
		err = s.o.DB.Reader().QueryRowContext(ctx, `SELECT mesa FROM choose_questions WHERE id = ? AND workspace = ? AND user_id = ?`, n, workspace, u.ID).Scan(&mesa)
		if err != nil {
			return "", bad
		}
		return mesa, nil
	}
	return "", bad
}

// Begin implements panel.ChatSource. It checks, before anything is kept: the level, the workspace,
// the places for uploads, the block for secrets, the text (the filter of the Telegram), the quote
// and the Grão-Mestre.
func (s *Service) Begin(ctx context.Context, u users.User, in panel.ChatDraftInput) (panel.ChatDraft, error) {
	if !u.Acts(in.Workspace) {
		return nil, notFound()
	}
	if err := users.Require(u.Level, users.TalkToGrandMaster); err != nil {
		return nil, panel.Err(panel.CodeForbiddenLevel, "o seu nível não permite falar com o Grão-Mestre", map[string]any{"capability": string(users.TalkToGrandMaster)})
	}
	if !clientIDRe.MatchString(in.ClientID) {
		return nil, panel.Err(panel.CodeChatMalformed, "mensagem malformada", nil)
	}
	if utf8.RuneCountInString(in.Text) > MaxText {
		return nil, panel.Err(panel.CodeChatTooLong, "mensagem longa demais", map[string]any{"max": MaxText})
	}
	// before anything is looked at: a user with too many secrets caught is refused whatever he sends,
	// so a refusal never says whether a text was one
	if s.secretBlocked(u.ID) {
		return nil, panel.Err(panel.CodeChatBlocked, "envio bloqueado por tentativas de mandar segredos", map[string]any{"minutes": int(secretWindow / time.Minute)})
	}
	if !s.acquireUpload(u.ID) {
		return nil, panel.Err(panel.CodeChatBusy, "há mensagens suas sendo enviadas; espere terminar", nil)
	}
	d, err := s.begin(ctx, u, in)
	if err != nil {
		s.releaseUpload(u.ID)
		return nil, err
	}
	return d, nil
}

func (s *Service) begin(ctx context.Context, u users.User, in panel.ChatDraftInput) (*draft, error) {
	gm, ok, err := s.o.GrandMaster(ctx, in.Workspace)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, panel.Err(panel.CodeChatNoGrandMaster, "o workspace não tem Grão-Mestre agora", nil)
	}
	var table string
	if in.Quote != "" {
		if table, err = s.quote(ctx, u, in.Workspace, in.Quote); err != nil {
			return nil, err
		}
	}
	if !s.allowSend(u.ID) {
		return nil, panel.Err(panel.CodeRateLimited, "mensagens demais; espere um instante", nil)
	}
	// the filter of the Telegram: a phrase holds the message, a token of a known format is taken out
	// of it, and the message that follows a held phrase is held too
	kind, clean := secretscreen.Screen(s.mask, in.Text)
	if kind == secretscreen.None && strings.TrimSpace(in.Text) != "" && s.followUpOpen(u.ID) {
		kind, clean = secretscreen.FollowUp, ""
	}
	d := &draft{s: s, u: u, in: in, gm: gm, table: table, text: strings.TrimSpace(in.Text)}
	if kind != secretscreen.None {
		d.catch(ctx, kind, clean)
	}
	return d, nil
}

// draft is a message being received.
type draft struct {
	s      *Service
	u      users.User
	in     panel.ChatDraftInput
	gm     characters.Character
	table  string
	text   string
	secret secretscreen.Kind
	stored []attachments.Stored
	done   sync.Once
}

// catch records a secret in the message: what the Grão-Mestre gets is the notice or the text without the
// piece, no file goes, and the ones already kept are taken back.
func (d *draft) catch(ctx context.Context, kind secretscreen.Kind, clean string) {
	if d.secret != secretscreen.None {
		return
	}
	d.secret = kind
	if kind.Held() {
		d.text = HeldNotice
	} else {
		d.text = strings.TrimSpace(clean)
	}
	d.s.secretCaught(ctx, d.u, kind)
	d.discardFiles(ctx)
}

func (d *draft) discardFiles(ctx context.Context) {
	for _, st := range d.stored {
		if err := d.s.discard(ctx, d.in.Workspace, st); err != nil {
			d.s.log.Warn("panelchat: a file of a message that was not delivered was not taken back", "err", err)
		}
	}
	d.stored = nil
}

// Abort implements panel.ChatDraft: the message will not be sent; the files kept for it are taken back and
// the place of the upload is given back.
func (d *draft) Abort(ctx context.Context) {
	d.done.Do(func() {
		d.discardFiles(context.WithoutCancel(ctx))
		d.s.releaseUpload(d.u.ID)
	})
}

// AddFile implements panel.ChatDraft. The file is kept as an attachment of the workspace with
// the origin "painel": the type is the one its content says, it is never executed, and one over
// the limit is refused (and nothing past the limit is read). The name of the file is looked at like
// the text; a message with a secret takes no file.
func (d *draft) AddFile(ctx context.Context, name, mimeType string, r io.Reader) error {
	limit := d.s.o.Limit()
	if d.secret == secretscreen.None {
		if kind, _ := secretscreen.Screen(d.s.mask, path.Base(strings.ReplaceAll(name, "\\", "/"))); kind != secretscreen.None {
			d.catch(ctx, secretscreen.Passphrase, "") // a name that gives a password holds the message
		}
	}
	if d.secret != secretscreen.None {
		_, err := io.Copy(io.Discard, io.LimitReader(r, limit+1))
		return err
	}
	if len(d.stored) >= MaxFiles {
		return panel.Err(panel.CodeChatTooManyFiles, "arquivos demais numa mensagem", map[string]any{"max": MaxFiles})
	}
	tooLarge := panel.Err(panel.CodeChatFileTooLarge, "arquivo grande demais", map[string]any{"limit": limit})
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		var big *http.MaxBytesError
		if errors.As(err, &big) {
			return tooLarge
		}
		return err
	}
	if int64(len(data)) > limit {
		return tooLarge
	}
	st, err := d.s.o.Receive(ctx, attachments.Request{Workspace: d.in.Workspace, UserID: d.u.ID,
		Origin: attachments.Origin{Channel: inbox.ChannelPanel, PanelState: d.in.Session},
		File:   attachments.Ref{Name: name, MIME: mimeType, Size: int64(len(data)), Kind: attachments.KindDocument}}, data)
	var big *attachments.TooLargeError
	switch {
	case errors.As(err, &big):
		return tooLarge
	case err != nil:
		return err
	}
	d.stored = append(d.stored, st)
	return nil
}

// Send implements panel.ChatDraft: the message enters the inbox of the Grão-Mestre with the origin
// "painel" (the user, the session and the id of the message) and the priority of a message of a
// user. Sent again with the same id, it is in the inbox once; the files kept for a message that was
// not queued (a failure, or the repeat of one already there) are taken back.
func (d *draft) Send(ctx context.Context) (panel.ChatSent, error) {
	released := false
	defer func() {
		if !released {
			d.Abort(ctx)
		}
	}()
	if d.text == "" && len(d.stored) == 0 {
		return panel.ChatSent{}, panel.Err(panel.CodeChatEmpty, "escreva uma mensagem", nil)
	}
	lines := []string{}
	if d.text != "" {
		lines = append(lines, d.text)
	}
	var paths []string
	for _, st := range d.stored {
		lines = append(lines, st.Line())
		paths = append(paths, st.Path)
	}
	n := inbox.NewItem{Workspace: d.in.Workspace, Target: d.gm.ID, Kind: inbox.KindUser, Sender: d.u.Name, Text: strings.Join(lines, "\n"), Attachments: paths,
		Origin: &inbox.Origin{Channel: inbox.ChannelPanel, User: d.u.ID, UserName: d.u.Name, Level: characters.UserLevel(d.u.Level),
			Conversation: d.in.Session, Message: d.in.ClientID, RepliedTo: d.in.Quote, Table: d.table},
		Refs: inbox.Refs{Key: "panel:" + d.u.ID + ":" + d.in.Workspace + ":" + d.in.ClientID}}
	it, err := d.s.enqueue(ctx, n)
	switch {
	case errors.Is(err, ErrNoGrandMaster):
		return panel.ChatSent{}, panel.Err(panel.CodeChatNoGrandMaster, "o workspace não tem Grão-Mestre agora", nil)
	case err != nil:
		return panel.ChatSent{}, err
	}
	// a repeat of a message already queued returns that one: the files of this try are not its own
	if len(d.stored) > 0 && (len(it.Attachments) == 0 || it.Attachments[0] != d.stored[0].Path) {
		d.discardFiles(ctx)
	}
	released = true
	d.done.Do(func() { d.s.releaseUpload(d.u.ID) })
	held := d.s.o.Locked != nil && d.s.o.Locked(ctx)
	// the other tabs of the user show the message too
	d.s.emit(panel.ChatEvent{User: d.u.ID, Workspace: d.in.Workspace, Kind: panel.EventChatChanged})
	sent := panel.ChatSent{ID: prefixIn + strconv.FormatInt(seqOf(it.ID), 10), Held: held}
	switch {
	case d.secret.Held():
		sent.Secret = "held"
	case d.secret == secretscreen.Format:
		sent.Secret = "removed"
	}
	return sent, nil
}
