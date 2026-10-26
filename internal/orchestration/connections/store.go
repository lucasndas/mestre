package connections

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/mestre-ia/mestre/internal/protocol"
	"github.com/mestre-ia/mestre/internal/store"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// Store is the connections table of the Guild database. Every change is one
// transaction: a connection never exists without a reason.
type Store struct {
	DB    *store.DB
	Clock clock.Clock
	ids   *protocol.IDGen
}

func (s *Store) newID() string {
	if s.ids == nil {
		s.ids = protocol.NewIDGen(s.Clock)
	}
	return s.ids.NewString()
}

func ms(t time.Time) int64 { return t.UnixMilli() }

func fromMs(v int64) time.Time {
	if v == 0 {
		return time.Time{}
	}
	return time.UnixMilli(v).UTC()
}

// AddInput is one reason for a connection between two endpoints.
type AddInput struct {
	Workspace, Table string
	A, B             Ref
	Reason           string
	By               string
}

// AddResult says what Add did.
type AddResult struct {
	Connection Connection
	// Existed is true when the pair was already connected.
	Existed bool
	// NewReason is true when the reason was not there yet.
	NewReason bool
}

// Add connects the two endpoints for the reason. A pair already connected
// gets the reason added, once: connecting again changes nothing.
func (s *Store) Add(ctx context.Context, in AddInput) (AddResult, error) {
	a, b := ordered(in.A, in.B)
	var res AddResult
	err := s.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		now := ms(s.Clock.Now())
		var id string
		err := tx.QueryRowContext(ctx, `SELECT id FROM connections WHERE a_kind = ? AND a_id = ? AND b_kind = ? AND b_id = ?`,
			string(a.Kind), a.ID, string(b.Kind), b.ID).Scan(&id)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			id = s.newID()
			if _, err := tx.ExecContext(ctx, `INSERT INTO connections (id, workspace, table_id, a_kind, a_id, b_kind, b_id, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
				id, in.Workspace, in.Table, string(a.Kind), a.ID, string(b.Kind), b.ID, now); err != nil {
				return err
			}
		case err != nil:
			return err
		default:
			res.Existed = true
		}
		r, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO connection_reasons (connection_id, reason, at, by) VALUES (?, ?, ?, ?)`, id, in.Reason, now, in.By)
		if err != nil {
			return err
		}
		n, _ := r.RowsAffected()
		res.NewReason = n > 0
		res.Connection, err = connection(ctx, tx, `c.id = ?`, id)
		return err
	})
	return res, err
}

// DropReason takes a reason off the connection between two endpoints; the
// connection goes away with its last reason. It returns what is left of it
// (the zero Connection when it is gone), and ErrNoConnection when there was
// no such reason.
func (s *Store) DropReason(ctx context.Context, a, b Ref, reason string) (Connection, error) {
	a, b = ordered(a, b)
	var left Connection
	err := s.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		c, err := connection(ctx, tx, `c.a_kind = ? AND c.a_id = ? AND c.b_kind = ? AND c.b_id = ?`, string(a.Kind), a.ID, string(b.Kind), b.ID)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			return ErrNoConnection
		case err != nil:
			return err
		case !c.Has(reason):
			return ErrNoConnection
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM connection_reasons WHERE connection_id = ? AND reason = ?`, c.ID, reason); err != nil {
			return err
		}
		if len(c.Reasons) == 1 {
			_, err := tx.ExecContext(ctx, `DELETE FROM connections WHERE id = ?`, c.ID)
			return err
		}
		left, err = connection(ctx, tx, `c.id = ?`, c.ID)
		return err
	})
	return left, err
}

// DropReasonEverywhere takes a reason off every connection that has it (the
// end of a quest), deleting the ones it was the last reason of. It returns the
// connections as they were, with the reasons they had.
func (s *Store) DropReasonEverywhere(ctx context.Context, reason string) ([]Connection, error) {
	var was []Connection
	err := s.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		cs, err := connections(ctx, tx, `c.id IN (SELECT connection_id FROM connection_reasons WHERE reason = ?)`, reason)
		if err != nil {
			return err
		}
		for _, c := range cs {
			if _, err := tx.ExecContext(ctx, `DELETE FROM connection_reasons WHERE connection_id = ? AND reason = ?`, c.ID, reason); err != nil {
				return err
			}
			if len(c.Reasons) == 1 {
				if _, err := tx.ExecContext(ctx, `DELETE FROM connections WHERE id = ?`, c.ID); err != nil {
					return err
				}
			}
		}
		was = cs
		return nil
	})
	return was, err
}

// RemoveNode deletes every connection of an endpoint (a character that ended,
// a scroll that was deleted) and returns them.
func (s *Store) RemoveNode(ctx context.Context, x Ref) ([]Connection, error) {
	var gone []Connection
	err := s.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		cs, err := connections(ctx, tx, `(c.a_kind = ? AND c.a_id = ?) OR (c.b_kind = ? AND c.b_id = ?)`, string(x.Kind), x.ID, string(x.Kind), x.ID)
		if err != nil {
			return err
		}
		for _, c := range cs {
			if _, err := tx.ExecContext(ctx, `DELETE FROM connection_reasons WHERE connection_id = ?`, c.ID); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM connections WHERE id = ?`, c.ID); err != nil {
				return err
			}
		}
		gone = cs
		return nil
	})
	return gone, err
}

// Of lists the connections of an endpoint.
func (s *Store) Of(ctx context.Context, x Ref) ([]Connection, error) {
	return connections(ctx, s.DB.Reader(), `(c.a_kind = ? AND c.a_id = ?) OR (c.b_kind = ? AND c.b_id = ?)`, string(x.Kind), x.ID, string(x.Kind), x.ID)
}

// Between returns the connection of the pair, if there is one.
func (s *Store) Between(ctx context.Context, a, b Ref) (Connection, bool, error) {
	a, b = ordered(a, b)
	c, err := connection(ctx, s.DB.Reader(), `c.a_kind = ? AND c.a_id = ? AND c.b_kind = ? AND c.b_id = ?`, string(a.Kind), a.ID, string(b.Kind), b.ID)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return Connection{}, false, nil
	case err != nil:
		return Connection{}, false, err
	}
	return c, true, nil
}

// OfTable lists the connections between the scrolls, the tomes and the
// characters of a table.
func (s *Store) OfTable(ctx context.Context, workspace, table string) ([]Connection, error) {
	return connections(ctx, s.DB.Reader(), `c.workspace = ? AND c.table_id = ?`, workspace, table)
}

// querier is a transaction or a pool.
type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func connection(ctx context.Context, q querier, where string, args ...any) (Connection, error) {
	cs, err := connections(ctx, q, where, args...)
	if err != nil {
		return Connection{}, err
	}
	if len(cs) == 0 {
		return Connection{}, sql.ErrNoRows
	}
	return cs[0], nil
}

func connections(ctx context.Context, q querier, where string, args ...any) ([]Connection, error) {
	rows, err := q.QueryContext(ctx, `SELECT c.id, c.workspace, c.table_id, c.a_kind, c.a_id, c.b_kind, c.b_id, c.created_at
		FROM connections c WHERE `+where+` ORDER BY c.created_at, c.id`, args...)
	if err != nil {
		return nil, fmt.Errorf("connections: %w", err)
	}
	var out []Connection
	for rows.Next() {
		var c Connection
		var ak, bk string
		var created int64
		if err := rows.Scan(&c.ID, &c.Workspace, &c.Table, &ak, &c.A.ID, &bk, &c.B.ID, &created); err != nil {
			rows.Close()
			return nil, fmt.Errorf("connections: %w", err)
		}
		c.A.Kind, c.B.Kind, c.CreatedAt = Kind(ak), Kind(bk), fromMs(created)
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("connections: %w", err)
	}
	rows.Close()
	for i := range out {
		rs, err := q.QueryContext(ctx, `SELECT reason, at, by FROM connection_reasons WHERE connection_id = ? ORDER BY at, reason`, out[i].ID)
		if err != nil {
			return nil, fmt.Errorf("connections: %w", err)
		}
		for rs.Next() {
			var r Reason
			var at int64
			if err := rs.Scan(&r.Name, &at, &r.By); err != nil {
				rs.Close()
				return nil, fmt.Errorf("connections: %w", err)
			}
			r.At = fromMs(at)
			out[i].Reasons = append(out[i].Reasons, r)
		}
		if err := rs.Err(); err != nil {
			rs.Close()
			return nil, fmt.Errorf("connections: %w", err)
		}
		rs.Close()
	}
	return out, nil
}
