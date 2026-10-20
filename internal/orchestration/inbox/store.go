package inbox

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/lucasndas/mestre/internal/store"
)

// Store is the inbox_messages and outbound_messages tables of the Guild
// database.
type Store struct{ DB *store.DB }

func ms(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

func fromMs(v int64) time.Time {
	if v == 0 {
		return time.Time{}
	}
	return time.UnixMilli(v).UTC()
}

// ItemID is the id of the item with the sequence number.
func ItemID(seq int64) string { return "m" + strconv.FormatInt(seq, 10) }

func seqOf(id string) (int64, bool) {
	if len(id) < 2 || id[0] != 'm' {
		return 0, false
	}
	n, err := strconv.ParseInt(id[1:], 10, 64)
	return n, err == nil && n > 0
}

const itemColumns = `seq, workspace, target, kind, sender, sender_id, body, attachments, origin, refs, priority, state, unconfirmed, created_at, typed_at, delivered_at`

type scanner interface{ Scan(...any) error }

func scanItem(sc scanner) (Item, error) {
	var (
		it                           Item
		seq                          int64
		kind, state                  string
		attach, origin, refs         string
		prio                         int
		unconf                       int
		created, typed, deliveredAtV int64
	)
	if err := sc.Scan(&seq, &it.Workspace, &it.Target, &kind, &it.Sender, &it.SenderID, &it.Text, &attach, &origin, &refs, &prio, &state, &unconf, &created, &typed, &deliveredAtV); err != nil {
		return Item{}, err
	}
	it.ID, it.Kind, it.State, it.Priority, it.Unconfirmed = ItemID(seq), Kind(kind), State(state), Priority(prio), unconf != 0
	it.CreatedAt, it.TypedAt, it.DeliveredAt = fromMs(created), fromMs(typed), fromMs(deliveredAtV)
	if attach != "" {
		if err := json.Unmarshal([]byte(attach), &it.Attachments); err != nil {
			return Item{}, fmt.Errorf("item %s: attachments: %w", it.ID, err)
		}
	}
	if origin != "" {
		it.Origin = new(Origin)
		if err := json.Unmarshal([]byte(origin), it.Origin); err != nil {
			return Item{}, fmt.Errorf("item %s: origin: %w", it.ID, err)
		}
	}
	if refs != "" {
		if err := json.Unmarshal([]byte(refs), &it.Refs); err != nil {
			return Item{}, fmt.Errorf("item %s: refs: %w", it.ID, err)
		}
	}
	return it, nil
}

func marshalOrEmpty(v any, empty bool) (string, error) {
	if empty {
		return "", nil
	}
	b, err := json.Marshal(v)
	return string(b), err
}

// Insert queues the item and returns it with its id.
func (s *Store) Insert(ctx context.Context, n NewItem, at time.Time) (Item, error) {
	prio, rank := priorityOf(n)
	attach, err := marshalOrEmpty(n.Attachments, len(n.Attachments) == 0)
	if err != nil {
		return Item{}, err
	}
	origin, err := marshalOrEmpty(n.Origin, n.Origin == nil)
	if err != nil {
		return Item{}, err
	}
	refs, err := marshalOrEmpty(n.Refs, n.Refs == (Refs{}))
	if err != nil {
		return Item{}, err
	}
	var seq int64
	err = s.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		if k := n.Refs.Key; k != "" {
			err := tx.QueryRowContext(ctx, `SELECT seq FROM inbox_messages WHERE json_valid(refs) AND json_extract(refs, '$.key') = ? LIMIT 1`, k).Scan(&seq)
			if err == nil {
				return nil // already queued: the same item
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO inbox_messages (workspace, target, kind, sender, sender_id, body, attachments, origin, refs, priority, rank, state, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'queued', ?)`,
			n.Workspace, n.Target, string(n.Kind), n.Sender, n.SenderID, n.Text, attach, origin, refs, int(prio), rank, ms(at))
		if err != nil {
			return err
		}
		seq, err = res.LastInsertId()
		return err
	})
	if err != nil {
		return Item{}, fmt.Errorf("queue an item for %s: %w", n.Target, err)
	}
	return s.Get(ctx, ItemID(seq))
}

// ByKey finds the item queued with the idempotency key (Refs.Key).
func (s *Store) ByKey(ctx context.Context, key string) (Item, bool, error) {
	if key == "" {
		return Item{}, false, nil
	}
	items, err := s.list(ctx, `json_valid(refs) AND json_extract(refs, '$.key') = ? LIMIT 1`, key)
	if err != nil || len(items) == 0 {
		return Item{}, false, err
	}
	return items[0], true, nil
}

// ForRun finds an item `quest` of the run (Refs.Run) put in the inbox of the
// target, in whatever state.
func (s *Store) ForRun(ctx context.Context, run int64, target string) (Item, bool, error) {
	if run == 0 {
		return Item{}, false, nil
	}
	items, err := s.list(ctx, `target = ? AND kind = 'quest' AND json_valid(refs) AND json_extract(refs, '$.run') = ? LIMIT 1`, target, run)
	if err != nil || len(items) == 0 {
		return Item{}, false, err
	}
	return items[0], true, nil
}

// Get returns the item with the id (ErrNoItem).
func (s *Store) Get(ctx context.Context, id string) (Item, error) {
	seq, ok := seqOf(id)
	if !ok {
		return Item{}, fmt.Errorf("%w: %q", ErrNoItem, id)
	}
	it, err := scanItem(s.DB.Reader().QueryRowContext(ctx, `SELECT `+itemColumns+` FROM inbox_messages WHERE seq = ?`, seq))
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return Item{}, fmt.Errorf("%w: %s", ErrNoItem, id)
	case err != nil:
		return Item{}, err
	}
	return it, nil
}

func (s *Store) list(ctx context.Context, where string, args ...any) ([]Item, error) {
	rows, err := s.DB.Reader().QueryContext(ctx, `SELECT `+itemColumns+` FROM inbox_messages WHERE `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Item
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// Pending lists what waits for a character, in the order of delivery: the
// item being delivered first, then the queued ones by priority and arrival.
func (s *Store) Pending(ctx context.Context, target string) ([]Item, error) {
	return s.list(ctx, `target = ? AND state IN ('queued', 'delivering') ORDER BY (state = 'delivering') DESC, priority, rank, seq`, target)
}

// Count is how many items wait for a character (queued and being delivered).
func (s *Store) Count(ctx context.Context, target string) (int, error) {
	var n int
	err := s.DB.Reader().QueryRowContext(ctx, `SELECT count(*) FROM inbox_messages WHERE target = ? AND state IN ('queued', 'delivering')`, target).Scan(&n)
	return n, err
}

// Next is the item to deliver to a character: the queued one with the best
// priority, the oldest. onlyUsers restricts it to the messages of users (a
// paused table).
func (s *Store) Next(ctx context.Context, target string, onlyUsers bool) (Item, bool, error) {
	q := `target = ? AND state = 'queued'`
	if onlyUsers {
		q += ` AND kind = 'user'`
	}
	items, err := s.list(ctx, q+` ORDER BY priority, rank, seq LIMIT 1`, target)
	if err != nil || len(items) == 0 {
		return Item{}, false, err
	}
	return items[0], true, nil
}

// InFlight is the item typed into a character whose turn did not end yet.
func (s *Store) InFlight(ctx context.Context, target string) (Item, bool, error) {
	items, err := s.list(ctx, `target = ? AND state = 'delivering' ORDER BY seq LIMIT 1`, target)
	if err != nil || len(items) == 0 {
		return Item{}, false, err
	}
	return items[0], true, nil
}

func (s *Store) exec(ctx context.Context, q string, args ...any) (int64, error) {
	var n int64
	err := s.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, q, args...)
		if err != nil {
			return err
		}
		n, err = res.RowsAffected()
		return err
	})
	return n, err
}

// MarkTyping says the item is being typed: it is not the next any more.
func (s *Store) MarkTyping(ctx context.Context, id string, at time.Time) (bool, error) {
	seq, ok := seqOf(id)
	if !ok {
		return false, fmt.Errorf("%w: %q", ErrNoItem, id)
	}
	n, err := s.exec(ctx, `UPDATE inbox_messages SET state = 'delivering', typed_at = ? WHERE seq = ? AND state = 'queued'`, ms(at), seq)
	return n > 0, err
}

// Requeue gives an item that was being delivered back to the queue.
func (s *Store) Requeue(ctx context.Context, id string) error {
	seq, ok := seqOf(id)
	if !ok {
		return fmt.Errorf("%w: %q", ErrNoItem, id)
	}
	_, err := s.exec(ctx, `UPDATE inbox_messages SET state = 'queued', typed_at = 0 WHERE seq = ? AND state = 'delivering'`, seq)
	return err
}

// RequeueAll gives back to the queue what a character was being delivered
// (the character was interrupted).
func (s *Store) RequeueAll(ctx context.Context, target string) (int64, error) {
	return s.exec(ctx, `UPDATE inbox_messages SET state = 'queued', typed_at = 0 WHERE target = ? AND state = 'delivering'`, target)
}

// RequeueEverything is the recovery after a fall of the engine: an item that
// was being delivered and whose turn was not recorded goes back to the queue
// (docs/orquestracao/esperas-e-caixa-de-entrada.md#persistência-e-retomada).
func (s *Store) RequeueEverything(ctx context.Context) (int64, error) {
	return s.exec(ctx, `UPDATE inbox_messages SET state = 'queued', typed_at = 0 WHERE state = 'delivering'`)
}

// Deliver marks an item delivered (its turn began, or ended).
func (s *Store) Deliver(ctx context.Context, id string, at time.Time, unconfirmed bool) (bool, error) {
	seq, ok := seqOf(id)
	if !ok {
		return false, fmt.Errorf("%w: %q", ErrNoItem, id)
	}
	u := 0
	if unconfirmed {
		u = 1
	}
	n, err := s.exec(ctx, `UPDATE inbox_messages SET state = 'delivered', delivered_at = ?, unconfirmed = ? WHERE seq = ? AND state IN ('delivering', 'queued')`, ms(at), u, seq)
	return n > 0, err
}

// DeliverInFlight marks delivered the item being delivered to a character: its
// turn ended.
func (s *Store) DeliverInFlight(ctx context.Context, target string, at time.Time) (int64, error) {
	return s.exec(ctx, `UPDATE inbox_messages SET state = 'delivered', delivered_at = ? WHERE target = ? AND state = 'delivering'`, ms(at), target)
}

// End closes a queued item without delivering it (expired or cancelled).
func (s *Store) End(ctx context.Context, id string, to State, at time.Time) (bool, error) {
	if to != Expired && to != Cancelled {
		return false, fmt.Errorf("%w: an item ends as expired or cancelled, not %s", ErrBadItem, to)
	}
	seq, ok := seqOf(id)
	if !ok {
		return false, fmt.Errorf("%w: %q", ErrNoItem, id)
	}
	n, err := s.exec(ctx, `UPDATE inbox_messages SET state = ?, delivered_at = ? WHERE seq = ? AND state = 'queued'`, string(to), ms(at), seq)
	return n > 0, err
}

// CancelFor cancels everything that still waits for a character.
func (s *Store) CancelFor(ctx context.Context, target string, at time.Time) (int64, error) {
	return s.exec(ctx, `UPDATE inbox_messages SET state = 'cancelled', delivered_at = ? WHERE target = ? AND state IN ('queued', 'delivering')`, ms(at), target)
}

// CancelByRef cancels the queued items that carry a reference (an ask, a
// wait): the thing they were about ended.
func (s *Store) CancelByRef(ctx context.Context, field, value string, at time.Time) (int64, error) {
	if field != "ask" && field != "wait" && field != "op_id" {
		return 0, fmt.Errorf("%w: no reference %q", ErrBadItem, field)
	}
	q := `UPDATE inbox_messages SET state = 'cancelled', delivered_at = ? WHERE state = 'queued' AND json_valid(refs) AND json_extract(refs, '$.` + field + `') = ?`
	return s.exec(ctx, q, ms(at), value)
}

// LastUserDelivered is the message of a user most recently delivered to the
// character: the current one, the one `reply` answers.
func (s *Store) LastUserDelivered(ctx context.Context, target string) (Item, bool, error) {
	items, err := s.list(ctx, `target = ? AND kind = 'user' AND state IN ('delivering', 'delivered') ORDER BY typed_at DESC, seq DESC LIMIT 1`, target)
	if err != nil || len(items) == 0 {
		return Item{}, false, err
	}
	return items[0], true, nil
}

// LastUser is the most recent message of a user to a character, whatever its
// state.
func (s *Store) LastUser(ctx context.Context, target string) (Item, bool, error) {
	items, err := s.list(ctx, `target = ? AND kind = 'user' ORDER BY seq DESC LIMIT 1`, target)
	if err != nil || len(items) == 0 {
		return Item{}, false, err
	}
	return items[0], true, nil
}

// All lists every item of a character, oldest first (for tests and the panel).
func (s *Store) All(ctx context.Context, target string) ([]Item, error) {
	return s.list(ctx, `target = ? ORDER BY seq`, target)
}

// Outbound is an answer to a user, kept until the channel takes it.
type Outbound struct {
	ID        int64
	Workspace string
	// Message is the id of the item of the inbox that is answered.
	Message      string
	Channel      string
	User         string
	Conversation string
	// ReplyTo is the message of the channel that is answered.
	ReplyTo     string
	Text        string
	Attachments []string
	Sent        bool
	CreatedAt   time.Time
	SentAt      time.Time
}

const outColumns = `id, workspace, message, channel, user_id, conversation, reply_to, body, attachments, state, created_at, sent_at`

func scanOut(sc scanner) (Outbound, error) {
	var (
		o              Outbound
		attach, state  string
		created, sentV int64
	)
	if err := sc.Scan(&o.ID, &o.Workspace, &o.Message, &o.Channel, &o.User, &o.Conversation, &o.ReplyTo, &o.Text, &attach, &state, &created, &sentV); err != nil {
		return Outbound{}, err
	}
	o.Sent, o.CreatedAt, o.SentAt = state == "sent", fromMs(created), fromMs(sentV)
	if attach != "" {
		if err := json.Unmarshal([]byte(attach), &o.Attachments); err != nil {
			return Outbound{}, err
		}
	}
	return o, nil
}

// AddOutbound keeps an answer for the channel.
func (s *Store) AddOutbound(ctx context.Context, o Outbound) (Outbound, error) {
	attach, err := marshalOrEmpty(o.Attachments, len(o.Attachments) == 0)
	if err != nil {
		return Outbound{}, err
	}
	var id int64
	err = s.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `INSERT INTO outbound_messages (workspace, message, channel, user_id, conversation, reply_to, body, attachments, state, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'pending', ?)`, o.Workspace, o.Message, o.Channel, o.User, o.Conversation, o.ReplyTo, o.Text, attach, ms(o.CreatedAt))
		if err != nil {
			return err
		}
		id, err = res.LastInsertId()
		return err
	})
	if err != nil {
		return Outbound{}, err
	}
	o.ID = id
	return o, nil
}

// MarkSent records that the channel took the answer.
func (s *Store) MarkSent(ctx context.Context, id int64, at time.Time) error {
	_, err := s.exec(ctx, `UPDATE outbound_messages SET state = 'sent', sent_at = ? WHERE id = ? AND state = 'pending'`, ms(at), id)
	return err
}

// PendingOutbound lists the answers a channel did not take yet, oldest first;
// channel "" lists every channel, workspace "" every workspace.
func (s *Store) PendingOutbound(ctx context.Context, workspace, channel string) ([]Outbound, error) {
	q := `SELECT ` + outColumns + ` FROM outbound_messages WHERE state = 'pending'`
	var args []any
	if workspace != "" {
		q += ` AND workspace = ?`
		args = append(args, workspace)
	}
	if channel != "" {
		q += ` AND channel = ?`
		args = append(args, channel)
	}
	rows, err := s.DB.Reader().QueryContext(ctx, q+` ORDER BY id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Outbound
	for rows.Next() {
		o, err := scanOut(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}
