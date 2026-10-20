package epicdeps

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/lucasndas/mestre/internal/store"
)

// State is the state of a dependency.
type State string

// The states.
const (
	Pending   State = "pending"
	Satisfied State = "satisfied"
	// Cancelled: the other epic will not be merged; the dependent one stays
	// held until the Grand Master decides.
	Cancelled State = "cancelled"
	// Released: the Grand Master dropped the dependency.
	Released State = "released"
)

// Dependency says that Epic cannot go on before DependsOn.
type Dependency struct {
	Workspace string
	Epic      int
	DependsOn int
	State     State
	// Wait is the wait of the Grand Master that holds the table of Epic ("":
	// none yet).
	Wait      string
	CreatedAt time.Time
	EndedAt   time.Time
}

// Store is the epic_dependencies table of the Guild database.
type Store struct{ DB *store.DB }

const columns = `workspace, epic, depends_on, state, wait, created_at, ended_at`

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

func scan(r interface{ Scan(...any) error }) (Dependency, error) {
	var d Dependency
	var state string
	var created, ended int64
	if err := r.Scan(&d.Workspace, &d.Epic, &d.DependsOn, &state, &d.Wait, &created, &ended); err != nil {
		return Dependency{}, err
	}
	d.State, d.CreatedAt, d.EndedAt = State(state), fromMs(created), fromMs(ended)
	return d, nil
}

func (s Store) list(ctx context.Context, where string, args ...any) ([]Dependency, error) {
	rows, err := s.DB.Reader().QueryContext(ctx, `SELECT `+columns+` FROM epic_dependencies `+where+` ORDER BY epic, depends_on`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Dependency
	for rows.Next() {
		d, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// Pending lists the pending dependencies of a workspace.
func (s Store) Pending(ctx context.Context, workspace string) ([]Dependency, error) {
	return s.list(ctx, `WHERE workspace = ? AND state = 'pending'`, workspace)
}

// PendingOf lists the pending dependencies of an epic.
func (s Store) PendingOf(ctx context.Context, workspace string, epic int) ([]Dependency, error) {
	return s.list(ctx, `WHERE workspace = ? AND epic = ? AND state = 'pending'`, workspace, epic)
}

// HeldOf lists the dependencies that keep an epic from going on: the pending
// ones and the cancelled ones nobody decided about.
func (s Store) HeldOf(ctx context.Context, workspace string, epic int) ([]Dependency, error) {
	return s.list(ctx, `WHERE workspace = ? AND epic = ? AND state IN ('pending', 'cancelled')`, workspace, epic)
}

// Get returns a dependency (false when there is none).
func (s Store) Get(ctx context.Context, workspace string, epic, on int) (Dependency, bool, error) {
	ds, err := s.list(ctx, `WHERE workspace = ? AND epic = ? AND depends_on = ?`, workspace, epic, on)
	if err != nil || len(ds) == 0 {
		return Dependency{}, false, err
	}
	return ds[0], true, nil
}

// PendingOn lists the pending dependencies on an epic.
func (s Store) PendingOn(ctx context.Context, workspace string, on int) ([]Dependency, error) {
	return s.list(ctx, `WHERE workspace = ? AND depends_on = ? AND state = 'pending'`, workspace, on)
}

// Add records a pending dependency; false when the pair already has a
// pending one. A pair that ended may be declared again.
func (s Store) Add(ctx context.Context, d Dependency) (bool, error) {
	var added bool
	err := s.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		var state string
		err := tx.QueryRowContext(ctx, `SELECT state FROM epic_dependencies WHERE workspace = ? AND epic = ? AND depends_on = ?`, d.Workspace, d.Epic, d.DependsOn).Scan(&state)
		switch {
		case err == sql.ErrNoRows:
			_, err = tx.ExecContext(ctx, `INSERT INTO epic_dependencies (`+columns+`) VALUES (?, ?, ?, 'pending', '', ?, 0)`,
				d.Workspace, d.Epic, d.DependsOn, ms(d.CreatedAt))
		case err != nil:
			return err
		case state != string(Pending):
			_, err = tx.ExecContext(ctx, `UPDATE epic_dependencies SET state = 'pending', wait = '', created_at = ?, ended_at = 0 WHERE workspace = ? AND epic = ? AND depends_on = ?`,
				ms(d.CreatedAt), d.Workspace, d.Epic, d.DependsOn)
		default:
			return nil
		}
		added = err == nil
		return err
	})
	return added, err
}

// SetWait records the wait that holds the dependency.
func (s Store) SetWait(ctx context.Context, d Dependency, wait string) error {
	return s.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE epic_dependencies SET wait = ? WHERE workspace = ? AND epic = ? AND depends_on = ? AND state = 'pending'`,
			wait, d.Workspace, d.Epic, d.DependsOn)
		return err
	})
}

// End moves a dependency from one of the states in from to another; false when
// it was not in any of them (somebody ended it first).
func (s Store) End(ctx context.Context, d Dependency, to State, at time.Time, from ...State) (bool, error) {
	if len(from) == 0 {
		from = []State{Pending}
	}
	marks := strings.TrimSuffix(strings.Repeat("?, ", len(from)), ", ")
	args := []any{string(to), ms(at), d.Workspace, d.Epic, d.DependsOn}
	for _, f := range from {
		args = append(args, string(f))
	}
	var ended bool
	err := s.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE epic_dependencies SET state = ?, ended_at = ? WHERE workspace = ? AND epic = ? AND depends_on = ? AND state IN (`+marks+`)`, args...)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		ended = n > 0
		return err
	})
	return ended, err
}

// Restore puts back a dependency as it was before it was declared again.
func (s Store) Restore(ctx context.Context, d Dependency) error {
	return s.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE epic_dependencies SET state = ?, wait = ?, created_at = ?, ended_at = ? WHERE workspace = ? AND epic = ? AND depends_on = ?`,
			string(d.State), d.Wait, ms(d.CreatedAt), ms(d.EndedAt), d.Workspace, d.Epic, d.DependsOn)
		return err
	})
}

// OnAs lists the dependencies on an epic that are in a state.
func (s Store) OnAs(ctx context.Context, workspace string, on int, state State) ([]Dependency, error) {
	return s.list(ctx, `WHERE workspace = ? AND depends_on = ? AND state = ?`, workspace, on, string(state))
}

// Delete removes a dependency that was just declared (the undo of Depend).
func (s Store) Delete(ctx context.Context, d Dependency) error {
	return s.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM epic_dependencies WHERE workspace = ? AND epic = ? AND depends_on = ? AND state = 'pending'`,
			d.Workspace, d.Epic, d.DependsOn)
		return err
	})
}
