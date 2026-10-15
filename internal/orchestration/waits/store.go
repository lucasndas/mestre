package waits

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/lucasndas/mestre/internal/store"
)

// Store is the waits, asks and wait_cycles tables of the Guild database.
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

type scanner interface{ Scan(...any) error }

const waitColumns = `id, workspace, table_id, waiter, kind, mode, targets, reason, quest, state, result, created_at, timeout_at, until_at, ended_at`

func scanWait(sc scanner) (Wait, error) {
	var (
		w                               Wait
		kind, mode, state               string
		targets                         string
		created, timeout, until_, ended int64
	)
	if err := sc.Scan(&w.ID, &w.Workspace, &w.Table, &w.Waiter, &kind, &mode, &targets, &w.Reason, &w.Quest, &state, &w.Result, &created, &timeout, &until_, &ended); err != nil {
		return Wait{}, err
	}
	w.Kind, w.Mode, w.State = Kind(kind), Mode(mode), State(state)
	w.CreatedAt, w.TimeoutAt, w.UntilAt, w.EndedAt = fromMs(created), fromMs(timeout), fromMs(until_), fromMs(ended)
	if err := json.Unmarshal([]byte(targets), &w.Targets); err != nil {
		return Wait{}, fmt.Errorf("wait %s: targets: %w", w.ID, err)
	}
	return w, nil
}

// nextID is the next number of a prefix ("w12"), inside the transaction.
func nextID(ctx context.Context, tx *sql.Tx, table, prefix string) (string, error) {
	var n int64
	err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(CAST(substr(id, 2) AS INTEGER)), 0) + 1 FROM `+table).Scan(&n)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s%d", prefix, n), nil
}

// InsertWait registers a wait and gives it its id.
func (s *Store) InsertWait(ctx context.Context, w Wait) (Wait, error) {
	targets, err := json.Marshal(w.Targets)
	if err != nil {
		return Wait{}, err
	}
	err = s.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		id, err := nextID(ctx, tx, "waits", "w")
		if err != nil {
			return err
		}
		w.ID = id
		_, err = tx.ExecContext(ctx, `INSERT INTO waits (`+waitColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 'active', '', ?, ?, ?, 0)`,
			w.ID, w.Workspace, w.Table, w.Waiter, string(w.Kind), string(w.Mode), string(targets), w.Reason, w.Quest, ms(w.CreatedAt), ms(w.TimeoutAt), ms(w.UntilAt))
		return err
	})
	if err != nil {
		return Wait{}, fmt.Errorf("register a wait of %s: %w", w.Waiter, err)
	}
	w.State = Active
	return w, nil
}

// GetWait returns a wait (ErrNoWait).
func (s *Store) GetWait(ctx context.Context, id string) (Wait, error) {
	w, err := scanWait(s.DB.Reader().QueryRowContext(ctx, `SELECT `+waitColumns+` FROM waits WHERE id = ?`, id))
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return Wait{}, fmt.Errorf("%w: %s", ErrNoWait, id)
	case err != nil:
		return Wait{}, err
	}
	return w, nil
}

func (s *Store) listWaits(ctx context.Context, where string, args ...any) ([]Wait, error) {
	rows, err := s.DB.Reader().QueryContext(ctx, `SELECT `+waitColumns+` FROM waits WHERE `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Wait
	for rows.Next() {
		w, err := scanWait(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// ActiveWaits lists the active waits, oldest first.
func (s *Store) ActiveWaits(ctx context.Context) ([]Wait, error) {
	return s.listWaits(ctx, `state = 'active' ORDER BY created_at, id`)
}

// ActiveOf lists the active waits of a character, oldest first.
func (s *Store) ActiveOf(ctx context.Context, waiter string) ([]Wait, error) {
	return s.listWaits(ctx, `state = 'active' AND waiter = ? ORDER BY created_at, id`, waiter)
}

// SetTargets keeps the targets of an active wait (a target is done).
func (s *Store) SetTargets(ctx context.Context, id string, targets []Target) error {
	b, err := json.Marshal(targets)
	if err != nil {
		return err
	}
	_, err = s.exec(ctx, `UPDATE waits SET targets = ? WHERE id = ? AND state = 'active'`, string(b), id)
	return err
}

// End closes an active wait; it reports whether this call was the one that
// closed it (a wait is satisfied or expires only once).
func (s *Store) End(ctx context.Context, id string, to State, result string, at time.Time, targets []Target) (bool, error) {
	b, err := json.Marshal(targets)
	if err != nil {
		return false, err
	}
	n, err := s.exec(ctx, `UPDATE waits SET state = ?, result = ?, ended_at = ?, targets = ? WHERE id = ? AND state = 'active'`, string(to), result, ms(at), string(b), id)
	return n > 0, err
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

const askColumns = `id, workspace, table_id, asker, target, item, state, blocking, response, created_at, timed_out_at, answered_at`

func scanAsk(sc scanner) (Ask, error) {
	var (
		a                         Ask
		state                     string
		blocking                  int
		created, timedOut, answer int64
	)
	if err := sc.Scan(&a.ID, &a.Workspace, &a.Table, &a.Asker, &a.Target, &a.Item, &state, &blocking, &a.Response, &created, &timedOut, &answer); err != nil {
		return Ask{}, err
	}
	a.State, a.Blocking = AskState(state), blocking != 0
	a.CreatedAt, a.TimedOutAt, a.AnsweredAt = fromMs(created), fromMs(timedOut), fromMs(answer)
	return a, nil
}

// InsertAsk registers an ask and gives it its id.
func (s *Store) InsertAsk(ctx context.Context, a Ask) (Ask, error) {
	blocking := 0
	if a.Blocking {
		blocking = 1
	}
	err := s.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		id, err := nextID(ctx, tx, "asks", "a")
		if err != nil {
			return err
		}
		a.ID = id
		_, err = tx.ExecContext(ctx, `INSERT INTO asks (`+askColumns+`) VALUES (?, ?, ?, ?, ?, ?, 'open', ?, '', ?, 0, 0)`,
			a.ID, a.Workspace, a.Table, a.Asker, a.Target, a.Item, blocking, ms(a.CreatedAt))
		return err
	})
	if err != nil {
		return Ask{}, fmt.Errorf("register an ask of %s: %w", a.Asker, err)
	}
	a.State = AskOpen
	return a, nil
}

// GetAsk returns an ask.
func (s *Store) GetAsk(ctx context.Context, id string) (Ask, error) {
	a, err := scanAsk(s.DB.Reader().QueryRowContext(ctx, `SELECT `+askColumns+` FROM asks WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Ask{}, fmt.Errorf("%w: ask %s", ErrNoWait, id)
	}
	return a, err
}

func (s *Store) listAsks(ctx context.Context, where string, args ...any) ([]Ask, error) {
	rows, err := s.DB.Reader().QueryContext(ctx, `SELECT `+askColumns+` FROM asks WHERE `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Ask
	for rows.Next() {
		a, err := scanAsk(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// OpenAsks lists the asks that are open, oldest first.
func (s *Store) OpenAsks(ctx context.Context) ([]Ask, error) {
	return s.listAsks(ctx, `state = 'open' ORDER BY created_at, id`)
}

// OpenAsksTo lists the open asks made to a character, by the asker when set.
func (s *Store) OpenAsksTo(ctx context.Context, target, asker string) ([]Ask, error) {
	if asker == "" {
		return s.listAsks(ctx, `state = 'open' AND target = ? ORDER BY created_at, id`, target)
	}
	return s.listAsks(ctx, `state = 'open' AND target = ? AND asker = ? ORDER BY created_at, id`, target, asker)
}

// SetAskItem keeps the item that carries the question.
func (s *Store) SetAskItem(ctx context.Context, id, item string) error {
	_, err := s.exec(ctx, `UPDATE asks SET item = ? WHERE id = ?`, item, id)
	return err
}

// Detach says no call of the CLI waits for the ask any more; timedOut is when
// its time ran out (zero: the caller left).
func (s *Store) Detach(ctx context.Context, id string, timedOut time.Time) error {
	_, err := s.exec(ctx, `UPDATE asks SET blocking = 0, timed_out_at = CASE WHEN ? > 0 THEN ? ELSE timed_out_at END WHERE id = ? AND state = 'open'`, ms(timedOut), ms(timedOut), id)
	return err
}

// DetachAll detaches every ask: the calls of the CLI did not survive the
// restart of the engine.
func (s *Store) DetachAll(ctx context.Context) error {
	_, err := s.exec(ctx, `UPDATE asks SET blocking = 0 WHERE state = 'open' AND blocking = 1`)
	return err
}

// EndAsk closes an open ask; it reports whether this call closed it.
func (s *Store) EndAsk(ctx context.Context, id string, to AskState, response string, at time.Time) (bool, error) {
	n, err := s.exec(ctx, `UPDATE asks SET state = ?, response = ?, answered_at = ?, blocking = 0 WHERE id = ? AND state = 'open'`, string(to), response, ms(at), id)
	return n > 0, err
}

// ActiveCycles lists the deadlocks warned about that still last.
func (s *Store) ActiveCycles(ctx context.Context) ([]Cycle, error) {
	rows, err := s.DB.Reader().QueryContext(ctx, `SELECT key, workspace, text, created_at FROM wait_cycles WHERE ended_at = 0 ORDER BY created_at, key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Cycle
	for rows.Next() {
		var c Cycle
		var at int64
		if err := rows.Scan(&c.Key, &c.Workspace, &c.Text, &at); err != nil {
			return nil, err
		}
		c.CreatedAt = fromMs(at)
		out = append(out, c)
	}
	return out, rows.Err()
}

// AddCycle records a deadlock; it reports whether it was new (a cycle that is
// recorded and still lasts is not new). A cycle that ended and came back is.
func (s *Store) AddCycle(ctx context.Context, c Cycle) (bool, error) {
	fresh := false
	err := s.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		var ended int64
		err := tx.QueryRowContext(ctx, `SELECT ended_at FROM wait_cycles WHERE key = ?`, c.Key).Scan(&ended)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			fresh = true
			_, err = tx.ExecContext(ctx, `INSERT INTO wait_cycles (key, workspace, text, created_at, ended_at) VALUES (?, ?, ?, ?, 0)`, c.Key, c.Workspace, c.Text, ms(c.CreatedAt))
			return err
		case err != nil:
			return err
		case ended == 0:
			return nil
		}
		fresh = true
		_, err = tx.ExecContext(ctx, `UPDATE wait_cycles SET text = ?, created_at = ?, ended_at = 0 WHERE key = ?`, c.Text, ms(c.CreatedAt), c.Key)
		return err
	})
	return fresh, err
}

// EndCycle records that a cycle went away.
func (s *Store) EndCycle(ctx context.Context, key string, at time.Time) error {
	_, err := s.exec(ctx, `UPDATE wait_cycles SET ended_at = ? WHERE key = ? AND ended_at = 0`, ms(at), key)
	return err
}
