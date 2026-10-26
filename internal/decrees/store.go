package decrees

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/users"
)

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

// queryer is what a transaction and a pool both are.
type queryer interface {
	QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row
	ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error)
}

const decreeColumns = `id, kind, state, effect, target_kind, target, workspace, mesa, scope, author_id, author_level, text, origin, pin, request, action, target_ref, request_type, impression, suspended_by, created_at, until, ended_at`

type scanner interface{ Scan(...any) error }

func scanDecree(sc scanner) (Decree, error) {
	var (
		d                                 Decree
		kind, state, effect, tkind, level string
		origin, action, ref               string
		pin, created, until, ended        int64
	)
	if err := sc.Scan(&d.Number, &kind, &state, &effect, &tkind, &d.Target.Name, &d.Workspace, &d.Mesa, &d.Scope, &d.Author, &level, &d.Text, &origin,
		&pin, &d.Request, &action, &ref, &d.RequestType, &d.Impression, &d.SuspendedBy, &created, &until, &ended); err != nil {
		return Decree{}, err
	}
	d.Kind, d.State, d.Target.Kind, d.AuthorLevel, d.PIN = Kind(kind), State(state), TargetKind(tkind), classes.Level(level), pin != 0
	d.CreatedAt, d.Until, d.EndedAt = fromMs(created), fromMs(until), fromMs(ended)
	if err := json.Unmarshal([]byte(effect), &d.Effect); err != nil {
		return Decree{}, fmt.Errorf("decree %d: %w", d.Number, err)
	}
	if err := json.Unmarshal([]byte(origin), &d.Origin); err != nil {
		return Decree{}, fmt.Errorf("decree %d: %w", d.Number, err)
	}
	if action != "" {
		d.Action = new(classes.Action)
		if err := json.Unmarshal([]byte(action), d.Action); err != nil {
			return Decree{}, fmt.Errorf("decree %d: %w", d.Number, err)
		}
	}
	if ref != "" {
		d.TargetRef = json.RawMessage(ref)
	}
	return d, nil
}

func queryDecrees(ctx context.Context, q queryer, where string, args ...any) ([]Decree, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+decreeColumns+` FROM decrees `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Decree
	for rows.Next() {
		d, err := scanDecree(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func getDecree(ctx context.Context, q queryer, n int64) (Decree, error) {
	ds, err := queryDecrees(ctx, q, `WHERE id = ?`, n)
	switch {
	case err != nil:
		return Decree{}, err
	case len(ds) == 0:
		return Decree{}, ErrNoDecree
	}
	return ds[0], nil
}

// insertDecree writes a decree in the state it has and gives it its number.
func insertDecree(ctx context.Context, tx queryer, d Decree, note string, now time.Time) (int64, error) {
	effect, err := json.Marshal(d.Effect)
	if err != nil {
		return 0, err
	}
	origin, err := json.Marshal(d.Origin)
	if err != nil {
		return 0, err
	}
	var action []byte
	if d.Action != nil {
		if action, err = json.Marshal(d.Action); err != nil {
			return 0, err
		}
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO decrees (kind, state, effect_kind, effect, target_kind, target, workspace, mesa, scope, author_id, author_level, text, origin, pin, request, action, target_ref, request_type, impression, suspended_by, created_at, until, ended_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0)`,
		string(d.Kind), string(d.State), d.Effect.Kind, string(effect), string(d.Target.Kind), d.Target.Name, d.Workspace, d.Mesa, d.Scope, d.Author, string(d.AuthorLevel),
		d.Text, string(origin), boolInt(d.PIN), d.Request, string(action), string(d.TargetRef), d.RequestType, d.Impression, d.SuspendedBy, ms(now), ms(d.Until))
	if err != nil {
		return 0, err
	}
	n, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	return n, addEvent(ctx, tx, n, d.State, d.Author, note, now)
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func addEvent(ctx context.Context, tx queryer, n int64, state State, by, note string, now time.Time) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO decree_events (decree, at, state, by, note) VALUES (?, ?, ?, ?, ?)`, n, ms(now), string(state), by, note)
	return err
}

// transition moves a decree from one of the states to another, once; it is
// false when the decree was not in any of them.
func transition(ctx context.Context, tx queryer, n int64, to State, by, note string, now time.Time, from ...State) (bool, error) {
	if len(from) == 0 {
		return false, errors.New("decrees: a transition needs a state to leave")
	}
	set, args := `state = ?`, []any{string(to)}
	if to.Ended() {
		set += `, ended_at = ?, suspended_by = ''`
		args = append(args, ms(now))
	}
	args = append(args, n)
	marks := ""
	for i, f := range from {
		if i > 0 {
			marks += ", "
		}
		marks += "?"
		args = append(args, string(f))
	}
	res, err := tx.ExecContext(ctx, `UPDATE decrees SET `+set+` WHERE id = ? AND state IN (`+marks+`)`, args...)
	if err != nil {
		return false, err
	}
	if changed, _ := res.RowsAffected(); changed == 0 {
		return false, nil
	}
	if to.Ended() || to == Suspended {
		if err := endSubsOf(ctx, tx, n, now); err != nil {
			return false, err
		}
	}
	return true, addEvent(ctx, tx, n, to, by, note, now)
}

// readUser reads a user as the users package keeps him, from the same
// transaction the decision is made in: what a decree is authorized by is what
// is stored now, never what the caller said.
func readUser(ctx context.Context, q queryer, id string) (users.User, error) {
	var u users.User
	var level, state string
	err := q.QueryRowContext(ctx, `SELECT id, name, level, state, telegram_id FROM users WHERE id = ?`, id).Scan(&u.ID, &u.Name, &level, &state, &u.TelegramID)
	if errors.Is(err, sql.ErrNoRows) {
		return users.User{}, fmt.Errorf("%w: %s", users.ErrNoUser, id)
	}
	if err != nil {
		return users.User{}, err
	}
	u.Level, u.State = users.Level(level), users.State(state)
	rows, err := q.QueryContext(ctx, `SELECT workspace FROM user_workspaces WHERE user_id = ?`, id)
	if err != nil {
		return users.User{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var w string
		if err := rows.Scan(&w); err != nil {
			return users.User{}, err
		}
		u.Workspaces = append(u.Workspaces, w)
	}
	return u, rows.Err()
}

// classLevel is the level the effective rules give a person.
func classLevel(l users.Level) classes.Level {
	switch l {
	case users.Owner:
		return classes.LevelOwner
	case users.Administrator:
		return classes.LevelAdmin
	case users.Approver:
		return classes.LevelApprover
	}
	return ""
}

// rank orders the levels of the people, the agents below all of them.
func rank(l classes.Level) int {
	switch l {
	case classes.LevelOwner:
		return 3
	case classes.LevelAdmin:
		return 2
	case classes.LevelApprover:
		return 1
	}
	return 0
}
