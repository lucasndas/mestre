package telegram

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"
	"time"
)

// MessageLife is how long the bot remembers where a message it sent came from
// (docs/telegram/roteamento.md#resposta-encadeada): a reply to an older one
// is routed by the other rules.
const MessageLife = 180 * 24 * time.Hour

// PendingLife is how long a message waits for the user to choose a workspace;
// it is the life of the buttons that ask.
const PendingLife = ButtonTTL

// Active is the workspace the user chose as his active one ("" when none).
func (s *Store) Active(ctx context.Context, userID string) (string, error) {
	var ws string
	err := s.DB.Reader().QueryRowContext(ctx, `SELECT workspace FROM telegram_active WHERE user_id = ?`, userID).Scan(&ws)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return ws, err
}

// SetActive makes a workspace the active one of the user.
func (s *Store) SetActive(ctx context.Context, userID, workspace string) error {
	return s.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO telegram_active (user_id, workspace, at) VALUES (?, ?, ?)
			ON CONFLICT(user_id) DO UPDATE SET workspace = excluded.workspace, at = excluded.at`, userID, workspace, ms(s.Clock.Now()))
		return err
	})
}

// Workspaces are the workspaces that exist, sorted.
func (s *Store) Workspaces(ctx context.Context) ([]string, error) {
	rows, err := s.DB.Reader().QueryContext(ctx, `SELECT slug FROM workspaces ORDER BY slug`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var w string
		if err := rows.Scan(&w); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// Park keeps a message of a user who acts in several workspaces and has not
// chosen one: it is delivered when he does.
func (s *Store) Park(ctx context.Context, in Inbound) error {
	return s.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO telegram_pending (user_id, kind, text, chat_id, message_id, at) VALUES (?, ?, ?, ?, ?, ?)`,
			in.User.ID, string(in.Kind), in.Text, in.ChatID, in.MessageID, ms(s.Clock.Now()))
		return err
	})
}

// Parked lists the messages the user parked that are still alive, oldest first.
func (s *Store) Parked(ctx context.Context, userID string) ([]HeldMessage, error) {
	rows, err := s.DB.Reader().QueryContext(ctx, `SELECT id, kind, text, chat_id, message_id FROM telegram_pending WHERE user_id = ? AND at > ? ORDER BY id`,
		userID, ms(s.Clock.Now().Add(-PendingLife)))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []HeldMessage
	for rows.Next() {
		var h HeldMessage
		var kind string
		if err := rows.Scan(&h.ID, &kind, &h.Text, &h.ChatID, &h.MessageID); err != nil {
			return nil, err
		}
		h.UserID, h.Kind = userID, Kind(kind)
		out = append(out, h)
	}
	return out, rows.Err()
}

// Unpark forgets a parked message (delivered, or too old).
func (s *Store) Unpark(ctx context.Context, id int64) error {
	return s.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM telegram_pending WHERE id = ?`, id)
		return err
	})
}

// MessageActions are the actions (the labels of the buttons) of a message the
// bot sent, "" when it had none.
func (s *Store) MessageActions(ctx context.Context, chat, message int64) (string, error) {
	var a string
	err := s.DB.Reader().QueryRowContext(ctx, `SELECT actions FROM bot_messages WHERE chat_id = ? AND message_id = ?`, chat, message).Scan(&a)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return a, err
}

// labelsOf are the labels of the buttons in the params of a message to send.
func labelsOf(params map[string]any) string {
	kb, ok := params["reply_markup"].(map[string]any)
	if !ok {
		return ""
	}
	var out []string
	switch rows := kb["inline_keyboard"].(type) {
	case []any:
		for _, r := range rows {
			if row, ok := r.([]any); ok {
				for _, b := range row {
					if m, ok := b.(map[string]any); ok {
						if t, ok := m["text"].(string); ok {
							out = append(out, t)
						}
					}
				}
			}
		}
	}
	return strings.Join(out, "\n")
}

// permitted are the workspaces the user acts in that exist: all of them for the
// Owner, his own list for the others.
func permitted(all []string, u interface {
	Acts(string) bool
}) []string {
	var out []string
	for _, w := range all {
		if u.Acts(w) {
			out = append(out, w)
		}
	}
	sort.Strings(out)
	return out
}
