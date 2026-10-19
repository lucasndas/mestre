package telegram

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

// Edit is the new text of a message the bot sent, with the buttons it keeps (an
// empty list takes them away).
type Edit struct {
	// Outbox is the id Send returned.
	Outbox int64
	// UserID is the user the message is for: the new buttons are his.
	UserID  string
	Text    string
	Buttons [][]Button
}

// Edit replaces the text and the buttons of a message: in the queue, if it did
// not go out yet, otherwise in the chat. A text that did not change is not an
// error. The text is masked like every message that leaves.
func (b *Bot) Edit(ctx context.Context, e Edit) error {
	var chat, msg int64
	var state string
	var params string
	err := b.o.DB.Reader().QueryRowContext(ctx, `SELECT chat_id, message_id, state, params FROM telegram_outbox WHERE id = ?`, e.Outbox).Scan(&chat, &msg, &state, &params)
	if errors.Is(err, sql.ErrNoRows) {
		return errors.New("telegram: no such message")
	}
	if err != nil {
		return err
	}
	text := Split(b.maskText(e.Text), partSize)[0]
	p := map[string]any{"chat_id": chat, "message_id": msg}
	if h, err := ToHTML(text); err == nil {
		p["text"], p["parse_mode"] = b.o.Mask(h), "HTML"
	} else {
		p["text"] = text
	}
	var markup any = map[string]any{"inline_keyboard": [][]map[string]string{}}
	if len(e.Buttons) > 0 {
		var rows [][]map[string]string
		err := b.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
			var err error
			rows, err = b.buttons(ctx, tx, e.UserID, e.Buttons, true)
			return err
		})
		if err != nil {
			return err
		}
		markup = map[string]any{"inline_keyboard": rows}
	}
	if state == "queued" {
		// It has not gone out: the queued message is rewritten. The delivery
		// that is already under way keeps the old text, and a message with
		// buttons that no longer count is refused at the click.
		var q map[string]any
		if err := json.Unmarshal([]byte(params), &q); err != nil {
			return err
		}
		q["text"] = p["text"]
		if pm, ok := p["parse_mode"]; ok {
			q["parse_mode"] = pm
		} else {
			delete(q, "parse_mode")
		}
		if _, html := p["parse_mode"]; html {
			q["_plain"] = text // the plain retry keeps the new text
		} else {
			delete(q, "_plain")
		}
		q["reply_markup"] = markup
		body, err := json.Marshal(q)
		if err != nil {
			return err
		}
		var changed int64
		if err := b.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
			res, err := tx.ExecContext(ctx, `UPDATE telegram_outbox SET params = ? WHERE id = ? AND state = 'queued'`, string(body), e.Outbox)
			if err != nil {
				return err
			}
			changed, _ = res.RowsAffected()
			return nil
		}); err != nil {
			return err
		}
		if changed > 0 {
			return nil
		}
		// It went out meanwhile: edit it in the chat.
		if err := b.o.DB.Reader().QueryRowContext(ctx, `SELECT message_id FROM telegram_outbox WHERE id = ?`, e.Outbox).Scan(&msg); err != nil {
			return err
		}
		p["message_id"] = msg
	}
	if msg == 0 || !b.allowed(ctx) {
		return errors.New("telegram: the message can not be edited now")
	}
	p["reply_markup"] = markup
	if _, err := b.o.Client.Call(ctx, "editMessageText", p); err != nil && !strings.Contains(err.Error(), "message is not modified") {
		return err
	}
	return nil
}
