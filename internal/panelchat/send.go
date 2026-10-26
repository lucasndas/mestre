package panelchat

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mestre-ia/mestre/internal/attachments"
	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/orchestration/inbox"
	"github.com/mestre-ia/mestre/internal/panel"
	"github.com/mestre-ia/mestre/internal/users"
)

func isNoRows(err error) bool { return errors.Is(err, sql.ErrNoRows) }

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
// the rate, the text (no secret), the quote and the Grão-Mestre.
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
	if m := s.mask(in.Text); m != in.Text {
		// a secret does not go to the Grão-Mestre: the attempt is audited, never the text
		if s.o.AuditSecret != nil {
			s.o.AuditSecret(ctx, u.ID)
		}
		return nil, panel.Err(panel.CodeChatSecret, "a mensagem parece conter um segredo e não foi enviada", nil)
	}
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
	return &draft{s: s, u: u, in: in, gm: gm, table: table}, nil
}

// draft is a message being received.
type draft struct {
	s      *Service
	u      users.User
	in     panel.ChatDraftInput
	gm     characters.Character
	table  string
	stored []attachments.Stored
}

// AddFile implements panel.ChatDraft. The file is kept as an attachment of the workspace with
// the origin "painel": the type is the one its content says, it is never executed, and one over
// the limit is refused (and nothing past the limit is read).
func (d *draft) AddFile(ctx context.Context, name, mimeType string, r io.Reader) error {
	if len(d.stored) >= MaxFiles {
		return panel.Err(panel.CodeChatTooManyFiles, "arquivos demais numa mensagem", map[string]any{"max": MaxFiles})
	}
	limit := d.s.o.Limit()
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
// user. Sent again with the same id, it is in the inbox once.
func (d *draft) Send(ctx context.Context) (panel.ChatSent, error) {
	text := strings.TrimSpace(d.in.Text)
	if text == "" && len(d.stored) == 0 {
		return panel.ChatSent{}, panel.Err(panel.CodeChatEmpty, "escreva uma mensagem", nil)
	}
	lines := []string{}
	if text != "" {
		lines = append(lines, text)
	}
	var paths []string
	for _, st := range d.stored {
		lines = append(lines, st.Line())
		paths = append(paths, st.Path)
	}
	n := inbox.NewItem{Workspace: d.in.Workspace, Target: d.gm.ID, Kind: inbox.KindUser, Sender: d.u.Name, Text: strings.Join(lines, "\n"), Attachments: paths,
		Origin: &inbox.Origin{Channel: inbox.ChannelPanel, User: d.u.ID, UserName: d.u.Name, Level: characters.UserLevel(d.u.Level),
			Conversation: d.in.Session, Message: d.in.ClientID, RepliedTo: d.in.Quote, Table: d.table},
		Refs: inbox.Refs{Key: "panel:" + d.u.ID + ":" + d.in.ClientID}}
	it, err := d.s.enqueue(ctx, n)
	switch {
	case errors.Is(err, ErrNoGrandMaster):
		return panel.ChatSent{}, panel.Err(panel.CodeChatNoGrandMaster, "o workspace não tem Grão-Mestre agora", nil)
	case err != nil:
		return panel.ChatSent{}, err
	}
	held := d.s.o.Locked != nil && d.s.o.Locked(ctx)
	// the other tabs of the user show the message too
	d.s.emit(panel.ChatEvent{User: d.u.ID, Workspace: d.in.Workspace, Kind: panel.EventChatChanged})
	return panel.ChatSent{ID: prefixIn + strconv.FormatInt(seqOf(it.ID), 10), Held: held}, nil
}
