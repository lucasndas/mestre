package panelchat

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/mestre-ia/mestre/internal/blocking"
	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/orchestration/inbox"
	"github.com/mestre-ia/mestre/internal/panel"
	"github.com/mestre-ia/mestre/internal/users"
)

// Outgoing is the inbox.Channels of the panel: the answer of `mestre reply` to a message
// that came from the panel. The answer is already kept (outbound_messages) and is read from
// there whenever the user opens the conversation, so "sending" it is telling the screens of
// that user that there is something new; a user who is away finds it, with the unread counter,
// when he opens the panel. Nothing goes to the Telegram for a message of the panel.
type Outgoing struct{ s *Service }

var _ inbox.Channels = Outgoing{}

// Channel is the channel of the panel for the inbox.
func (s *Service) Channel() Outgoing { return Outgoing{s} }

// Send implements inbox.Channels.
func (c Outgoing) Send(_ context.Context, o inbox.Outbound) error {
	if o.Channel != inbox.ChannelPanel {
		return inbox.ErrChannelDown
	}
	c.s.emit(panel.ChatEvent{User: o.User, Workspace: o.Workspace, Kind: panel.EventChatChanged})
	return nil
}

// Redacted is told a message of the panel was taken out of the inbox (`mestre reply --redact-source`):
// the open tabs of its user read the conversation again and drop it.
func (c Outgoing) Redacted(o inbox.Origin) {
	if o.Channel == inbox.ChannelPanel {
		c.s.emit(panel.ChatEvent{User: o.User, Kind: panel.EventChatChanged})
	}
}

// File implements panel.ChatSource: a file the Grão-Mestre sent to this user, in this workspace.
func (s *Service) File(ctx context.Context, u users.User, workspace string, answer int64, index int) (panel.ChatDownload, error) {
	gone := panel.Err(panel.CodeChatFileGone, "o arquivo não está disponível", nil)
	if !u.Acts(workspace) {
		return panel.ChatDownload{}, notFound()
	}
	o, err := s.o.Inbox.OutboundGet(ctx, answer)
	if err != nil || o.Workspace != workspace || o.User != u.ID || o.Channel != inbox.ChannelPanel || index >= len(o.Attachments) || s.o.Open == nil {
		return panel.ChatDownload{}, gone
	}
	d, err := s.o.Open(ctx, workspace, o, index)
	if err != nil {
		return panel.ChatDownload{}, gone
	}
	return d, nil
}

// SetProgress keeps the message of progress of a workspace as the card of the conversation, in
// place of the one before (one row per workspace: never a second card), and tells the people
// it is for.
func (s *Service) SetProgress(ctx context.Context, workspace, text string) error {
	text = s.mask(text)
	var same int
	// the message goes once to each person it is for: the card is only rewritten when it changed
	if err := s.o.DB.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM panel_chat_progress WHERE workspace = ? AND text = ?`, workspace, text).Scan(&same); err == nil && same > 0 {
		return nil
	}
	err := s.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO panel_chat_progress (workspace, text, updated_at) VALUES (?, ?, ?)
			ON CONFLICT(workspace) DO UPDATE SET text = excluded.text, updated_at = excluded.updated_at`, workspace, text, s.clk.Now().UnixMilli())
		return err
	})
	if err != nil {
		return err
	}
	for _, u := range s.audience(ctx, workspace, func(u users.User) bool { return canSeeProgress(u, workspace) }) {
		s.emit(panel.ChatEvent{User: u.ID, Workspace: workspace, Kind: panel.EventChatChanged})
	}
	return nil
}

// Question is a question of `mestre choose` for a user of the panel.
type Question struct {
	Workspace, Mesa, Asker, UserID string
	Text                           string
	Options                        []string
}

// MinOptions and MaxOptions are how many options a question has.
const (
	MinOptions = 2
	MaxOptions = 6
)

// AskChoice keeps the question and shows it in the conversation of the user, with the option the
// Grão-Mestre recommends (from 1) first. It returns the number of the question. It refuses a user
// who may not give orders to the Grão-Mestre, is not active, or does not act in the workspace.
func (s *Service) AskChoice(ctx context.Context, q Question, recommend int) (int64, error) {
	if n := len(q.Options); n < MinOptions || n > MaxOptions {
		return 0, fmt.Errorf("a question has %d to %d options, not %d", MinOptions, MaxOptions, n)
	}
	u, err := s.o.Users.Get(ctx, q.UserID)
	if err != nil {
		return 0, err
	}
	if !users.Can(u.Level, users.TalkToGrandMaster) {
		return 0, blocking.ErrNoLevel
	}
	if u.State != users.Active || !u.Acts(q.Workspace) {
		return 0, blocking.ErrNotReachable
	}
	if recommend < 1 || recommend > len(q.Options) {
		recommend = 1
	}
	ordered := []string{q.Options[recommend-1]}
	for i, o := range q.Options {
		if i != recommend-1 {
			ordered = append(ordered, o)
		}
	}
	body, err := json.Marshal(ordered)
	if err != nil {
		return 0, err
	}
	var id int64
	err = s.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `INSERT INTO choose_questions (workspace, mesa, asker, user_id, question, options, channel, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			q.Workspace, q.Mesa, q.Asker, u.ID, q.Text, string(body), inbox.ChannelPanel, s.clk.Now().UnixMilli())
		if err != nil {
			return err
		}
		id, err = res.LastInsertId()
		return err
	})
	if err != nil {
		return 0, err
	}
	s.emit(panel.ChatEvent{User: u.ID, Workspace: q.Workspace, Kind: panel.EventChatChanged})
	return id, nil
}

// Choose implements panel.ChatSource: the click of the user on an option of a question the
// Grão-Mestre asked in the panel. The answer goes to the inbox of the Grão-Mestre once, as a
// message of the user; a second click, from any tab, is refused. Only the user the question was
// asked of, in the channel it was asked in, can answer it.
func (s *Service) Choose(ctx context.Context, u users.User, id int64, option int) error {
	unknown := panel.Err(panel.CodeChatChoiceUnknown, "essa pergunta não existe mais", nil)
	var q question
	row := s.o.DB.Reader().QueryRowContext(ctx, `SELECT id, workspace, mesa, user_id, channel, question, options, state, answer, created_at FROM choose_questions
		WHERE id = ? AND channel = ? AND user_id = ?`, id, inbox.ChannelPanel, u.ID)
	q, err := scanQuestion(row)
	switch {
	case isNoRows(err):
		return unknown
	case err != nil:
		return err
	}
	if !u.Acts(q.Workspace) || !users.Can(u.Level, users.TalkToGrandMaster) {
		return unknown
	}
	if option < 0 || option >= len(q.Options) {
		return panel.Err(panel.CodeBadRequest, "opção inválida", nil)
	}
	claimed := false
	if err := s.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE choose_questions SET state = 'answered', answer = ?, answered_at = ? WHERE id = ? AND state = 'open'`, q.Options[option], s.clk.Now().UnixMilli(), id)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		claimed = n > 0
		return nil
	}); err != nil {
		return err
	}
	if !claimed {
		return panel.Err(panel.CodeChatChoiceTaken, "essa pergunta já foi respondida", nil)
	}
	_, err = s.enqueue(ctx, inbox.NewItem{Workspace: q.Workspace, Kind: inbox.KindUser, Sender: u.Name,
		Origin: &inbox.Origin{Channel: inbox.ChannelPanel, User: u.ID, UserName: u.Name, Level: characters.UserLevel(u.Level), Table: q.Mesa, Choice: q.ID},
		Text:   fmt.Sprintf("Escolha %q (pergunta %d: %s)", q.Options[option], q.ID, oneLine(q.Text)),
		Refs:   inbox.Refs{Key: fmt.Sprintf("panel-choice:%d", q.ID)}})
	if err != nil {
		// the answer is not lost to a failure of the inbox: the question is open again
		_ = s.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `UPDATE choose_questions SET state = 'open', answer = '', answered_at = 0 WHERE id = ? AND state = 'answered'`, id)
			return err
		})
		return panel.Err(panel.CodeChatChoiceLost, "não consegui entregar a resposta ao Grão-Mestre; clique de novo", nil)
	}
	s.emit(panel.ChatEvent{User: u.ID, Workspace: q.Workspace, Kind: panel.EventChatChanged})
	return nil
}
