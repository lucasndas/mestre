package rituals

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/lucasndas/mestre/internal/store"
)

// Store is the rituals and ritual_firings tables of the Guild database. Every
// change of the state of a ritual is one transaction.
type Store struct{ DB *store.DB }

func ms(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UTC().UnixMilli()
}

func fromMs(v int64) time.Time {
	if v == 0 {
		return time.Time{}
	}
	return time.UnixMilli(v).UTC()
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type scanner interface{ Scan(...any) error }

const ritualColumns = `id, workspace, table_id, name, target, target_table, quest, action, content, schedule, pre_run, timeout_ms, state,
	next_run, created_by, created_at, updated_at`

func scanRitual(sc scanner) (Ritual, error) {
	var (
		r                          Ritual
		action, state, sched       string
		timeout, next, created, up int64
	)
	if err := sc.Scan(&r.ID, &r.Workspace, &r.Table, &r.Name, &r.Target, &r.TargetTable, &r.Quest, &action, &r.Content, &sched, &r.PreRun,
		&timeout, &state, &next, &r.CreatedBy, &created, &up); err != nil {
		return Ritual{}, err
	}
	r.Action, r.State = Action(action), State(state)
	r.Timeout, r.NextRun = time.Duration(timeout)*time.Millisecond, fromMs(next)
	r.CreatedAt, r.UpdatedAt = fromMs(created), fromMs(up)
	if err := json.Unmarshal([]byte(sched), &r.Schedule); err != nil {
		return Ritual{}, fmt.Errorf("ritual %s: schedule: %w", r.ID, err)
	}
	return r, nil
}

const firingColumns = `id, ritual, scheduled_for, created_at, state, reason, late, output, delivered_at, updated_at`

func scanFiring(sc scanner) (Firing, error) {
	var (
		f                                        Firing
		state                                    string
		late                                     int
		scheduled, created, delivered, updatedAt int64
	)
	if err := sc.Scan(&f.ID, &f.Ritual, &scheduled, &created, &state, &f.Reason, &late, &f.Output, &delivered, &updatedAt); err != nil {
		return Firing{}, err
	}
	f.State, f.Late = FiringState(state), late != 0
	f.ScheduledFor, f.CreatedAt, f.DeliveredAt, f.UpdatedAt = fromMs(scheduled), fromMs(created), fromMs(delivered), fromMs(updatedAt)
	return f, nil
}

func listRituals(ctx context.Context, q querier, where string, args ...any) ([]Ritual, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+ritualColumns+` FROM rituals WHERE `+where+` ORDER BY created_at, id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Ritual
	for rows.Next() {
		r, err := scanRitual(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func listFirings(ctx context.Context, q querier, where string, args ...any) ([]Firing, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+firingColumns+` FROM ritual_firings WHERE `+where+` ORDER BY id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Firing
	for rows.Next() {
		f, err := scanFiring(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// Ritual returns a ritual by its id (ErrNoRitual).
func (s *Store) Ritual(ctx context.Context, id string) (Ritual, error) {
	rs, err := listRituals(ctx, s.DB.Reader(), `id = ?`, id)
	if err != nil {
		return Ritual{}, err
	}
	if len(rs) == 0 {
		return Ritual{}, fmt.Errorf("%w: %s", ErrNoRitual, id)
	}
	return rs[0], nil
}

// ByName finds a ritual of a scope by its name (table "" is the workspace).
func (s *Store) ByName(ctx context.Context, workspace, table, name string) (Ritual, bool, error) {
	rs, err := listRituals(ctx, s.DB.Reader(), `workspace = ? AND table_id = ? AND name = ? COLLATE NOCASE`, workspace, table, name)
	if err != nil || len(rs) == 0 {
		return Ritual{}, false, err
	}
	return rs[0], true, nil
}

// OfScope lists the rituals of a table, or of the workspace with table "".
func (s *Store) OfScope(ctx context.Context, workspace, table string) ([]Ritual, error) {
	return listRituals(ctx, s.DB.Reader(), `workspace = ? AND table_id = ?`, workspace, table)
}

// Due lists the enabled rituals whose slot came.
func (s *Store) Due(ctx context.Context, now time.Time) ([]Ritual, error) {
	return listRituals(ctx, s.DB.Reader(), `state = 'enabled' AND next_run > 0 AND next_run <= ?`, ms(now))
}

// Firing returns a firing.
func (s *Store) Firing(ctx context.Context, id int64) (Firing, error) {
	fs, err := listFirings(ctx, s.DB.Reader(), `id = ?`, id)
	if err != nil {
		return Firing{}, err
	}
	if len(fs) == 0 {
		return Firing{}, fmt.Errorf("no such firing %d", id)
	}
	return fs[0], nil
}

// Firings is the history of a ritual, oldest first.
func (s *Store) Firings(ctx context.Context, ritual string) ([]Firing, error) {
	return listFirings(ctx, s.DB.Reader(), `ritual = ?`, ritual)
}

// Open lists the firings that wait to be delivered, oldest first.
func (s *Store) Open(ctx context.Context) ([]Firing, error) {
	return listFirings(ctx, s.DB.Reader(), `state IN ('queued', 'pre_running', 'delivering')`)
}

// Tx is a transaction on the rituals.
type Tx struct {
	ctx context.Context
	tx  *sql.Tx
}

// Do runs fn in one transaction: all of it happens or none of it.
func (s *Store) Do(ctx context.Context, fn func(*Tx) error) error {
	return s.DB.WriteDurable(ctx, func(tx *sql.Tx) error { return fn(&Tx{ctx: ctx, tx: tx}) })
}

// Ritual reads a ritual inside the transaction.
func (t *Tx) Ritual(id string) (Ritual, error) {
	rs, err := listRituals(t.ctx, t.tx, `id = ?`, id)
	if err != nil {
		return Ritual{}, err
	}
	if len(rs) == 0 {
		return Ritual{}, fmt.Errorf("%w: %s", ErrNoRitual, id)
	}
	return rs[0], nil
}

// Insert makes a ritual and gives it its id.
func (t *Tx) Insert(r Ritual) (Ritual, error) {
	var taken int
	if err := t.tx.QueryRowContext(t.ctx, `SELECT COUNT(*) FROM rituals WHERE workspace = ? AND table_id = ? AND name = ? COLLATE NOCASE`,
		r.Workspace, r.Table, r.Name).Scan(&taken); err != nil {
		return Ritual{}, err
	}
	if taken > 0 {
		return Ritual{}, fmt.Errorf("%w: %s", ErrNameTaken, r.Name)
	}
	var n int64
	if err := t.tx.QueryRowContext(t.ctx, `SELECT COALESCE(MAX(CAST(substr(id, 2) AS INTEGER)), 0) + 1 FROM rituals`).Scan(&n); err != nil {
		return Ritual{}, err
	}
	r.ID = fmt.Sprintf("r%d", n)
	sched, err := json.Marshal(r.Schedule)
	if err != nil {
		return Ritual{}, err
	}
	_, err = t.tx.ExecContext(t.ctx, `INSERT INTO rituals (`+ritualColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.Workspace, r.Table, r.Name, r.Target, r.TargetTable, r.Quest, string(r.Action), r.Content, string(sched), r.PreRun,
		r.Timeout.Milliseconds(), string(r.State), ms(r.NextRun), r.CreatedBy, ms(r.CreatedAt), ms(r.UpdatedAt))
	return r, err
}

// Update writes a ritual again (every field but the id and the creation).
func (t *Tx) Update(r Ritual) error {
	var taken int
	if err := t.tx.QueryRowContext(t.ctx, `SELECT COUNT(*) FROM rituals WHERE workspace = ? AND table_id = ? AND name = ? COLLATE NOCASE AND id <> ?`,
		r.Workspace, r.Table, r.Name, r.ID).Scan(&taken); err != nil {
		return err
	}
	if taken > 0 {
		return fmt.Errorf("%w: %s", ErrNameTaken, r.Name)
	}
	sched, err := json.Marshal(r.Schedule)
	if err != nil {
		return err
	}
	_, err = t.tx.ExecContext(t.ctx, `UPDATE rituals SET name = ?, target = ?, target_table = ?, quest = ?, action = ?, content = ?, schedule = ?,
		pre_run = ?, timeout_ms = ?, state = ?, next_run = ?, updated_at = ? WHERE id = ?`,
		r.Name, r.Target, r.TargetTable, r.Quest, string(r.Action), r.Content, string(sched), r.PreRun, r.Timeout.Milliseconds(), string(r.State),
		ms(r.NextRun), ms(r.UpdatedAt), r.ID)
	return err
}

// Delete removes a ritual and its history.
func (t *Tx) Delete(id string) error {
	if _, err := t.tx.ExecContext(t.ctx, `DELETE FROM ritual_firings WHERE ritual = ?`, id); err != nil {
		return err
	}
	_, err := t.tx.ExecContext(t.ctx, `DELETE FROM rituals WHERE id = ?`, id)
	return err
}

// AddFiring records a firing. A firing that is to be queued is recorded as
// skipped when the ritual still has one in the queue: the firings do not pile
// up. It returns the firing as it was recorded.
func (t *Tx) AddFiring(f Firing) (Firing, error) {
	if f.State == Queued {
		var open int
		if err := t.tx.QueryRowContext(t.ctx, `SELECT COUNT(*) FROM ritual_firings WHERE ritual = ? AND state IN ('queued', 'pre_running', 'delivering')`, f.Ritual).Scan(&open); err != nil {
			return Firing{}, err
		}
		if open > 0 {
			f.State, f.Reason = Skipped, "o disparo anterior ainda está na fila"
		}
	}
	res, err := t.tx.ExecContext(t.ctx, `INSERT INTO ritual_firings (ritual, scheduled_for, created_at, state, reason, late, output, delivered_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		f.Ritual, ms(f.ScheduledFor), ms(f.CreatedAt), string(f.State), f.Reason, b2i(f.Late), f.Output, ms(f.DeliveredAt), ms(f.UpdatedAt))
	if err != nil {
		return Firing{}, err
	}
	f.ID, err = res.LastInsertId()
	return f, err
}

// SetFiring moves a firing to a state, with the reason, the tail of the
// error output and the time of the delivery. It reports whether the firing was
// still open (a closed one is never changed again).
func (s *Store) SetFiring(ctx context.Context, id int64, to FiringState, reason, output string, at time.Time) (bool, error) {
	var changed bool
	err := s.Do(ctx, func(t *Tx) error {
		delivered := int64(0)
		if to == Delivered {
			delivered = ms(at)
		}
		res, err := t.tx.ExecContext(ctx, `UPDATE ritual_firings SET state = ?, reason = ?, output = ?, delivered_at = ?, updated_at = ?
			WHERE id = ? AND state IN ('queued', 'pre_running', 'delivering')`, string(to), reason, output, delivered, ms(at), id)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		changed = n > 0
		return err
	})
	return changed, err
}

// Fire takes the slot of a ritual: if its next_run is still `expected`, it
// records the firings, moves next_run and the state, all in one transaction.
// It reports whether it did; another look that came first wins.
func (s *Store) Fire(ctx context.Context, id string, expected, next time.Time, state State, firings []Firing) (done bool, recorded []Firing, err error) {
	err = s.Do(ctx, func(t *Tx) error {
		r, err := t.Ritual(id)
		if err != nil {
			return err
		}
		if r.State != Enabled || !r.NextRun.Equal(expected) {
			return nil
		}
		for _, f := range firings {
			got, err := t.AddFiring(f)
			if err != nil {
				return err
			}
			recorded = append(recorded, got)
		}
		if _, err := t.tx.ExecContext(ctx, `UPDATE rituals SET next_run = ?, state = ? WHERE id = ?`, ms(next), string(state), id); err != nil {
			return err
		}
		done = true
		return nil
	})
	return done, recorded, err
}

// markPreRunning marks a queued firing as running its pre-run. It says whether
// the firing was queued: the state is written before the side effect, so that
// a firing found in it after a restart is not run again.
func (s *Store) markPreRunning(ctx context.Context, id int64, at time.Time) (bool, error) {
	return s.mark(ctx, id, PreRunning, at, "'queued'")
}

// markDelivering records the intent to deliver (the command typed, the prompt
// queued) before it happens.
func (s *Store) markDelivering(ctx context.Context, id int64, at time.Time) (bool, error) {
	return s.mark(ctx, id, Delivering, at, "'queued', 'pre_running'")
}

func (s *Store) mark(ctx context.Context, id int64, to FiringState, at time.Time, from string) (bool, error) {
	var changed bool
	err := s.Do(ctx, func(t *Tx) error {
		res, err := t.tx.ExecContext(ctx, `UPDATE ritual_firings SET state = ?, updated_at = ? WHERE id = ? AND state IN (`+from+`)`, string(to), ms(at), id)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		changed = n > 0
		return err
	})
	return changed, err
}

// Disable puts the enabled rituals of a scope in disabled, and skips the
// firings they have in the queue. It takes the rituals of the workspace that
// aim at the table master of that table too. It returns how
// many rituals changed.
func (s *Store) Disable(ctx context.Context, workspace, table string, now time.Time, reason string) (int, error) {
	var n int
	err := s.Do(ctx, func(t *Tx) error {
		where := `workspace = ? AND (table_id = ? OR (table_id = '' AND target_table = ?))`
		rs, err := listRituals(ctx, t.tx, where+` AND state = 'enabled'`, workspace, table, table)
		if err != nil {
			return err
		}
		ids := make([]string, 0, len(rs))
		for _, r := range rs {
			ids = append(ids, r.ID)
		}
		// Rituals already disabled may still have a firing in the queue.
		all, err := listRituals(ctx, t.tx, where, workspace, table, table)
		if err != nil {
			return err
		}
		for _, r := range all {
			if _, err := t.tx.ExecContext(ctx, `UPDATE ritual_firings SET state = 'skipped', reason = ?, updated_at = ? WHERE ritual = ? AND state IN ('queued', 'pre_running', 'delivering')`,
				reason, ms(now), r.ID); err != nil {
				return err
			}
		}
		if len(ids) == 0 {
			return nil
		}
		ph := strings.TrimSuffix(strings.Repeat("?, ", len(ids)), ", ")
		args := []any{ms(now)}
		for _, id := range ids {
			args = append(args, id)
		}
		if _, err := t.tx.ExecContext(ctx, `UPDATE rituals SET state = 'disabled', updated_at = ? WHERE id IN (`+ph+`)`, args...); err != nil {
			return err
		}
		n = len(ids)
		return nil
	})
	return n, err
}
