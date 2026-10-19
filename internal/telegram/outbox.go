package telegram

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/lucasndas/mestre/internal/users"
)

// ButtonTTL is how long a button the bot gave out stays valid.
const ButtonTTL = 24 * time.Hour

// Button is an inline button. Only a key goes to the Telegram as callback_data;
// the action, the argument, the recipient and the validity stay in the database.
type Button struct {
	Text, Action, Arg string
	// Capability is the level the click needs (checked again at the click).
	Capability users.Capability
}

// Out is a message to send. Text is Markdown.
type Out struct {
	ChatID int64
	// UserID is the user it is for: the terms he has not seen are explained and
	// the buttons are his.
	UserID string
	Text   string
	// Workspace and Mesa are recorded with the message for the threaded reply.
	Workspace, Mesa, Origin string
	Buttons                 [][]Button
	// ForceReply asks the client to open the reply box (the PIN, a code).
	ForceReply bool
}

// Send puts a message in the queue and returns the id of its last part. It goes
// out when the queue is flushed, never before the Gate lets it.
func (b *Bot) Send(ctx context.Context, o Out) (int64, error) {
	text := b.o.Mask(o.Text)
	var seen map[string]bool
	var explained []string
	if o.UserID != "" && len(b.o.Terms) > 0 {
		var err error
		if seen, err = b.st.Seen(ctx, o.UserID); err != nil {
			return 0, err
		}
		text, explained = explain(text, b.o.Terms, seen)
	}
	type item struct {
		method string
		params map[string]any
	}
	var items []item
	parts := Split(text, partSize)
	if len(parts) > MaxParts {
		summary := Split(text, 600)[0] + "\n\n(o texto completo vai no arquivo abaixo)"
		parts = []string{summary}
		items = append(items, item{"sendDocument", map[string]any{"chat_id": o.ChatID, "caption": "Texto completo", "filename": "mensagem.md", "content": text}})
	}
	var msgs []item
	for _, p := range parts {
		params := map[string]any{"chat_id": o.ChatID}
		if h, err := ToHTML(p); err == nil {
			params["text"], params["parse_mode"] = h, "HTML"
		} else {
			params["text"] = p // the conversion failed: plain text
		}
		msgs = append(msgs, item{"sendMessage", params})
	}
	items = append(msgs, items...) // the message first, the file after it
	var id int64
	err := b.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		now := b.clk.Now()
		last := msgs[len(msgs)-1].params
		switch {
		case o.ForceReply:
			last["reply_markup"] = map[string]any{"force_reply": true}
		case len(o.Buttons) > 0:
			var rows [][]map[string]string
			for _, row := range o.Buttons {
				var r []map[string]string
				for _, bt := range row {
					key, err := newKey()
					if err != nil {
						return err
					}
					if _, err := tx.ExecContext(ctx, `INSERT INTO telegram_callbacks (key, user_id, action, arg, capability, expires_at, state) VALUES (?, ?, ?, ?, ?, ?, 'open')`,
						key, o.UserID, bt.Action, bt.Arg, string(bt.Capability), ms(now.Add(ButtonTTL))); err != nil {
						return err
					}
					r = append(r, map[string]string{"text": bt.Text, "callback_data": key})
				}
				rows = append(rows, r)
			}
			last["reply_markup"] = map[string]any{"inline_keyboard": rows}
		}
		for _, t := range explained {
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO glossary_seen (user_id, term) VALUES (?, ?)`, o.UserID, t); err != nil {
				return err
			}
		}
		for _, it := range items {
			body, err := json.Marshal(it.params)
			if err != nil {
				return err
			}
			res, err := tx.ExecContext(ctx, `INSERT INTO telegram_outbox (chat_id, method, params, workspace, mesa, origin, state, not_before, created_at) VALUES (?, ?, ?, ?, ?, ?, 'queued', ?, ?)`,
				o.ChatID, it.method, string(body), o.Workspace, o.Mesa, o.Origin, ms(now), ms(now))
			if err != nil {
				return err
			}
			id, _ = res.LastInsertId()
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	b.wake()
	return id, nil
}

func newKey() (string, error) {
	var raw [10]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil // 20 characters: far under the 64 bytes of callback_data
}

type queued struct {
	id        int64
	chat      int64
	method    string
	params    map[string]any
	workspace string
	mesa      string
	origin    string
	attempts  int
	notBefore time.Time
}

func (b *Bot) due(ctx context.Context) ([]queued, error) {
	rows, err := b.o.DB.Reader().QueryContext(ctx, `SELECT id, chat_id, method, params, workspace, mesa, origin, attempts, not_before FROM telegram_outbox WHERE state = 'queued' ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []queued
	for rows.Next() {
		var q queued
		var params string
		var nb int64
		if err := rows.Scan(&q.id, &q.chat, &q.method, &params, &q.workspace, &q.mesa, &q.origin, &q.attempts, &nb); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(params), &q.params); err != nil {
			return nil, err
		}
		q.notBefore = fromMs(nb)
		out = append(out, q)
	}
	return out, rows.Err()
}

// Flush sends what is due, one message per second per chat and about thirty a
// second in all, in order within each chat. It returns when the next one will be
// due (zero: nothing waits). A 429 holds the chat for its retry_after; a network
// error or a 5xx holds the whole queue with a growing backoff, and the messages
// stay in the database until the Telegram is back.
func (b *Bot) Flush(ctx context.Context) (time.Time, error) {
	if !b.allowed(ctx) {
		return time.Time{}, nil
	}
	list, err := b.due(ctx)
	if err != nil {
		return time.Time{}, err
	}
	now := b.clk.Now()
	var next time.Time
	soon := func(t time.Time) {
		if next.IsZero() || t.Before(next) {
			next = t
		}
	}
	blocked := map[int64]bool{}
	for _, q := range list {
		if blocked[q.chat] {
			continue
		}
		if q.notBefore.After(now) {
			blocked[q.chat] = true
			soon(q.notBefore)
			continue
		}
		if last, ok := b.last[q.chat]; ok && now.Before(last.Add(time.Second)) {
			blocked[q.chat] = true
			soon(last.Add(time.Second))
			continue
		}
		if b.windowFull(now) {
			soon(now.Truncate(time.Second).Add(time.Second))
			break
		}
		msgID, err := b.deliver(ctx, q)
		var api *APIError
		switch {
		case err == nil:
			b.last[q.chat] = now
			b.counted(now)
			if err := b.sent(ctx, q, msgID); err != nil {
				return next, err
			}
		case errors.As(err, &api) && api.Code == 429:
			at := now.Add(time.Duration(max(api.RetryAfter, 1)) * time.Second)
			if err := b.hold(ctx, q.id, at, false); err != nil {
				return next, err
			}
			blocked[q.chat] = true
			soon(at)
		case errors.As(err, &api) && !api.Retryable():
			b.log.Warn("telegram: a message was refused and dropped", "outbox", q.id, "code", api.Code)
			if err := b.fail(ctx, q.id, fmt.Sprintf("%d %s", api.Code, api.Description)); err != nil {
				return next, err
			}
		default:
			at := now.Add(backoff(q.attempts))
			if herr := b.hold(ctx, q.id, at, true); herr != nil {
				return next, herr
			}
			soon(at)
			return next, err // the Telegram is out: nothing else goes now
		}
	}
	return next, nil
}

func (b *Bot) deliver(ctx context.Context, q queued) (int64, error) {
	var raw json.RawMessage
	var err error
	if q.method == "sendDocument" {
		content, _ := q.params["content"].(string)
		name, _ := q.params["filename"].(string)
		fields := map[string]string{"chat_id": fmt.Sprint(q.chat)}
		if c, ok := q.params["caption"].(string); ok {
			fields["caption"] = c
		}
		raw, err = b.o.Client.Upload(ctx, q.method, fields, "document", name, []byte(content))
	} else {
		raw, err = b.o.Client.Call(ctx, q.method, q.params)
	}
	if err != nil {
		return 0, err
	}
	var res struct {
		MessageID int64 `json:"message_id"`
	}
	_ = json.Unmarshal(raw, &res)
	return res.MessageID, nil
}

func (b *Bot) sent(ctx context.Context, q queued, msgID int64) error {
	return b.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE telegram_outbox SET state = 'sent', message_id = ?, params = '{}' WHERE id = ?`, msgID, q.id); err != nil {
			return err
		}
		if msgID == 0 {
			return nil
		}
		_, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO bot_messages (chat_id, message_id, workspace, mesa, origin, at) VALUES (?, ?, ?, ?, ?, ?)`,
			q.chat, msgID, q.workspace, q.mesa, q.origin, ms(b.clk.Now()))
		return err
	})
}

func (b *Bot) hold(ctx context.Context, id int64, at time.Time, failed bool) error {
	inc := 0
	if failed {
		inc = 1
	}
	return b.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE telegram_outbox SET not_before = ?, attempts = attempts + ? WHERE id = ?`, ms(at), inc, id)
		return err
	})
}

func (b *Bot) fail(ctx context.Context, id int64, why string) error {
	return b.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE telegram_outbox SET state = 'failed', params = ? WHERE id = ?`, `{"error":`+quote(why)+`}`, id)
		return err
	})
}

func quote(s string) string { b, _ := json.Marshal(s); return string(b) }

// backoff is 1 s, doubling to 60 s.
func backoff(attempts int) time.Duration {
	d := time.Second << min(attempts, 6)
	return min(d, time.Minute)
}

// OutboxState counts the messages of the queue by state (for the panel and the tests).
func (b *Bot) OutboxState(ctx context.Context) (queued, sent, failed int, err error) {
	rows, err := b.o.DB.Reader().QueryContext(ctx, `SELECT state, COUNT(*) FROM telegram_outbox GROUP BY state`)
	if err != nil {
		return 0, 0, 0, err
	}
	defer rows.Close()
	for rows.Next() {
		var st string
		var n int
		if err := rows.Scan(&st, &n); err != nil {
			return 0, 0, 0, err
		}
		switch st {
		case "queued":
			queued = n
		case "sent":
			sent = n
		case "failed":
			failed = n
		}
	}
	return queued, sent, failed, rows.Err()
}
