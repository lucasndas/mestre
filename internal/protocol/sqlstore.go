package protocol

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/mestre-ia/mestre/internal/store"
)

// SQLStore keeps the outbox and the processed commands in the tables of the
// same names of the machine database (a Tower) or of the Guild database (the
// Citadel); the migrations 0002_protocol.sql of both create them. It is an
// Outbox and a Processed.
type SQLStore struct {
	db *store.DB
}

var (
	_ Outbox    = (*SQLStore)(nil)
	_ Processed = (*SQLStore)(nil)
)

// NewSQLStore returns the store over an open database.
func NewSQLStore(db *store.DB) *SQLStore { return &SQLStore{db: db} }

// Put stores the envelope durably: when it returns the row survives a crash
// of the machine, because the message is about to leave and a Tower that
// restarted must still resend it.
func (s *SQLStore) Put(ctx context.Context, peer string, env Envelope) error {
	body, err := json.Marshal(env)
	if err != nil {
		return err
	}
	return s.db.WriteDurable(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO outbox (peer, id, type, body, created_at) VALUES (?, ?, ?, ?, ?)`,
			peer, env.ID, env.Type, body, env.TS.UnixMilli())
		return err
	})
}

// Done removes a message.
func (s *SQLStore) Done(ctx context.Context, peer, id string) error {
	_, err := s.db.Writer().ExecContext(ctx, `DELETE FROM outbox WHERE peer = ? AND id = ?`, peer, id)
	return err
}

// Pending returns the messages of the peer, oldest first.
func (s *SQLStore) Pending(ctx context.Context, peer string) ([]Envelope, error) {
	rows, err := s.db.Reader().QueryContext(ctx, `SELECT body FROM outbox WHERE peer = ? ORDER BY seq`, peer)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Envelope
	for rows.Next() {
		var body []byte
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		var env Envelope
		if err := json.Unmarshal(body, &env); err != nil {
			return nil, fmt.Errorf("outbox: unreadable row: %w", err)
		}
		out = append(out, env)
	}
	return out, rows.Err()
}

// Claim records that the sender's message is being taken.
func (s *SQLStore) Claim(ctx context.Context, sender, id, typ string, now time.Time) (bool, []byte, error) {
	var fresh bool
	var prev []byte
	err := s.db.WriteDurable(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO processed_commands (sender, id, type, processed_at) VALUES (?, ?, ?, ?)`,
			sender, id, typ, now.UnixMilli())
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if fresh = n == 1; fresh {
			return nil
		}
		err = tx.QueryRowContext(ctx, `SELECT ack FROM processed_commands WHERE sender = ? AND id = ?`, sender, id).Scan(&prev)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	})
	return fresh, prev, err
}

// Finish stores the ack that was given, durably: a repeat after a crash
// still gets the first answer.
func (s *SQLStore) Finish(ctx context.Context, sender, id string, ack []byte) error {
	return s.db.WriteDurable(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE processed_commands SET ack = ? WHERE sender = ? AND id = ?`, ack, sender, id)
		return err
	})
}

// Release forgets a claim, durably: a message refused with a retryable error
// must be able to run when it comes again, even after a crash.
func (s *SQLStore) Release(ctx context.Context, sender, id string) error {
	return s.db.WriteDurable(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM processed_commands WHERE sender = ? AND id = ?`, sender, id)
		return err
	})
}

// Prune forgets, of one sender, what was claimed before cutoff with an id
// lower than floor (ULIDs sort as their strings).
func (s *SQLStore) Prune(ctx context.Context, sender string, cutoff time.Time, floor string) (int, error) {
	res, err := s.db.Writer().ExecContext(ctx,
		`DELETE FROM processed_commands WHERE sender = ? AND processed_at < ? AND id < ?`, sender, cutoff.UnixMilli(), floor)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}
