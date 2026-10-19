package telegram

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"time"

	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/pkg/clock"
)

// Store is the part of the Guild database the bot keeps.
type Store struct {
	DB    *store.DB
	Clock clock.Clock
}

func ms(t time.Time) int64     { return t.UnixMilli() }
func fromMs(v int64) time.Time { return time.UnixMilli(v).UTC() }

// QuietReset is how long without an update makes the offset be dropped: after a
// week with none the Telegram picks the next update_id at random, maybe below
// the one kept, and an offset above it would confirm (and lose) everything.
const QuietReset = 5 * 24 * time.Hour

// Offset is the next offset of getUpdates: the last update recorded plus one;
// zero (no offset) when none is kept or the bot was quiet for QuietReset.
func (s *Store) Offset(ctx context.Context) (int64, error) {
	var v, at string
	err := s.DB.Reader().QueryRowContext(ctx, `SELECT value FROM telegram_state WHERE key = 'last_update'`).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if err := s.DB.Reader().QueryRowContext(ctx, `SELECT value FROM telegram_state WHERE key = 'last_update_at'`).Scan(&at); err == nil {
		if t, _ := strconv.ParseInt(at, 10, 64); s.Clock.Now().Sub(fromMs(t)) > QuietReset {
			return 0, nil
		}
	}
	n, _ := strconv.ParseInt(v, 10, 64)
	return n + 1, nil
}

// Prune forgets the updates processed more than two days ago: the deduplication
// only needs the recent ones, and an id the Telegram reuses after a long quiet
// must not be taken for a repetition.
func (s *Store) Prune(ctx context.Context) error {
	return s.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM telegram_updates WHERE state = 'done' AND received_at < ?`, ms(s.Clock.Now().Add(-48*time.Hour)))
		return err
	})
}

// Record keeps the update before the offset moves; false says it was already
// there (a repeated update_id is never processed twice). The offset moves in the
// same transaction.
func (s *Store) Record(ctx context.Context, id int64, body []byte) (bool, error) {
	fresh := false
	err := s.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO telegram_updates (update_id, received_at, state, body) VALUES (?, ?, 'new', ?)`, id, ms(s.Clock.Now()), string(body))
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		fresh = n > 0
		for k, v := range map[string]string{"last_update": strconv.FormatInt(id, 10), "last_update_at": strconv.FormatInt(ms(s.Clock.Now()), 10)} {
			if _, err := tx.ExecContext(ctx, `INSERT INTO telegram_state (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, k, v); err != nil {
				return err
			}
		}
		return nil
	})
	return fresh, err
}

// Pending are the updates recorded and not processed yet, in order.
func (s *Store) Pending(ctx context.Context) ([]Pending, error) {
	rows, err := s.DB.Reader().QueryContext(ctx, `SELECT update_id, body FROM telegram_updates WHERE state = 'new' ORDER BY update_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Pending
	for rows.Next() {
		var p Pending
		var body string
		if err := rows.Scan(&p.ID, &body); err != nil {
			return nil, err
		}
		p.Body = []byte(body)
		out = append(out, p)
	}
	return out, rows.Err()
}

// Pending is an update recorded and not processed.
type Pending struct {
	ID   int64
	Body []byte
}

// Done marks the update processed and clears its body: only the id stays, for
// the deduplication.
func (s *Store) Done(ctx context.Context, id int64) error {
	return s.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE telegram_updates SET state = 'done', body = '' WHERE update_id = ?`, id)
		return err
	})
}

// Capture is an answer the bot waits for from a user.
type Capture struct {
	UserID, Kind, Ref string
	OutboxID          int64
	Expires           time.Time
	// Armed says the prompt was delivered: before that the user has not seen it,
	// and what he writes is an ordinary message. Expired says the time ran out.
	Armed, Expired bool
}

// ErrCaptureBusy is an Ask while the user is still answering another one.
var ErrCaptureBusy = errors.New("telegram: the user is still answering another prompt")

// SetCapture makes the bot wait for the next text of the user, once his prompt
// is delivered. One at a time: a prompt that is still open is not replaced.
func (s *Store) SetCapture(ctx context.Context, c Capture) error {
	return s.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		var exp int64
		err := tx.QueryRowContext(ctx, `SELECT expires_at FROM telegram_captures WHERE user_id = ?`, c.UserID).Scan(&exp)
		if err == nil && s.Clock.Now().Before(fromMs(exp)) {
			return ErrCaptureBusy
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO telegram_captures (user_id, kind, ref, outbox_id, expires_at) VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(user_id) DO UPDATE SET kind = excluded.kind, ref = excluded.ref, outbox_id = excluded.outbox_id, expires_at = excluded.expires_at`,
			c.UserID, c.Kind, c.Ref, c.OutboxID, ms(c.Expires))
		return err
	})
}

// Capturing is the capture of a user, if any (armed or not, expired or not).
func (s *Store) Capturing(ctx context.Context, userID string) (Capture, bool, error) {
	var c Capture
	var exp int64
	var state sql.NullString
	err := s.DB.Reader().QueryRowContext(ctx, `SELECT c.user_id, c.kind, c.ref, c.outbox_id, c.expires_at, o.state
		FROM telegram_captures c LEFT JOIN telegram_outbox o ON o.id = c.outbox_id WHERE c.user_id = ?`, userID).
		Scan(&c.UserID, &c.Kind, &c.Ref, &c.OutboxID, &exp, &state)
	if errors.Is(err, sql.ErrNoRows) {
		return Capture{}, false, nil
	}
	if err != nil {
		return Capture{}, false, err
	}
	c.Expires = fromMs(exp)
	c.Expired = !s.Clock.Now().Before(c.Expires)
	c.Armed = state.Valid && state.String == "sent"
	return c, true, nil
}

// ClearCapture ends the wait for that prompt; true says this call ended it.
func (s *Store) ClearCapture(ctx context.Context, userID, ref string) (bool, error) {
	cleared := false
	err := s.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `DELETE FROM telegram_captures WHERE user_id = ? AND ref = ?`, userID, ref)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		cleared = n > 0
		return nil
	})
	return cleared, err
}

// Callback is a button the bot gave out: the key is the callback_data, the
// rest stays here.
type Callback struct {
	Key, UserID, Action, Arg string
	Capability               string
	Expires                  time.Time
	State                    string
	DecidedBy                string
}

// Callback returns a button by its key.
func (s *Store) Callback(ctx context.Context, key string) (Callback, bool, error) {
	var c Callback
	var exp int64
	err := s.DB.Reader().QueryRowContext(ctx, `SELECT key, user_id, action, arg, capability, expires_at, state, decided_by FROM telegram_callbacks WHERE key = ?`, key).
		Scan(&c.Key, &c.UserID, &c.Action, &c.Arg, &c.Capability, &exp, &c.State, &c.DecidedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return Callback{}, false, nil
	}
	c.Expires = fromMs(exp)
	return c, err == nil, err
}

// Take claims a button for a click: true if this click is the first (the others
// find it done).
func (s *Store) Take(ctx context.Context, key, by string) (bool, error) {
	took := false
	err := s.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE telegram_callbacks SET state = 'done', decided_by = ? WHERE key = ? AND state = 'open'`, by, key)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		took = n > 0
		return nil
	})
	return took, err
}

// Release gives a button back after its action failed, so the user may click again.
func (s *Store) Release(ctx context.Context, key string) error {
	return s.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE telegram_callbacks SET state = 'open', decided_by = '' WHERE key = ?`, key)
		return err
	})
}

// Seen are the terms already explained to the user.
func (s *Store) Seen(ctx context.Context, userID string) (map[string]bool, error) {
	rows, err := s.DB.Reader().QueryContext(ctx, `SELECT term FROM glossary_seen WHERE user_id = ?`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		out[t] = true
	}
	return out, rows.Err()
}

// ResetSeen forgets what was explained to the user (the Owner does it in the panel).
func (s *Store) ResetSeen(ctx context.Context, userID string) error {
	return s.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM glossary_seen WHERE user_id = ?`, userID)
		return err
	})
}

// MessageOrigin is the workspace and the mesa of a message the bot sent
// (the threaded reply).
func (s *Store) MessageOrigin(ctx context.Context, chat, message int64) (workspace, mesa string, ok bool, err error) {
	err = s.DB.Reader().QueryRowContext(ctx, `SELECT workspace, mesa FROM bot_messages WHERE chat_id = ? AND message_id = ?`, chat, message).Scan(&workspace, &mesa)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", false, nil
	}
	return workspace, mesa, err == nil, err
}
