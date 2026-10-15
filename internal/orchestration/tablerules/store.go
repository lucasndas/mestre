package tablerules

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/orchestration/chain"
	"github.com/lucasndas/mestre/internal/store"
)

// Store is the table_rules, table_rule_states and table_rule_uses tables of
// the Guild database.
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

const ruleColumns = `id, workspace, table_id, class, request, state, level, author, risk, until, mission, body, created_at, ended_at`

type scanner interface{ Scan(...any) error }

func scanRule(sc scanner) (Rule, error) {
	var (
		r                                Rule
		state, level, risk, doc          string
		request, until, created, endedAt int64
	)
	if err := sc.Scan(&r.ID, &r.Workspace, &r.Table, &r.Class, &request, &state, &level, &r.Author, &risk, &until, &r.Mission, &doc, &created, &endedAt); err != nil {
		return Rule{}, err
	}
	var b body
	if err := json.Unmarshal([]byte(doc), &b); err != nil {
		return Rule{}, fmt.Errorf("table rule %d: %w", r.ID, err)
	}
	r.Request, r.State, r.Level, r.Risk = request, State(state), chain.LevelKind(level), classes.Risk(risk)
	r.Pattern, r.ToolKind, r.Action = b.Pattern, b.ToolKind, b.Action
	r.Until, r.CreatedAt, r.EndedAt = fromMs(until), fromMs(created), fromMs(endedAt)
	return r, nil
}

// Insert registers a rule in the state it has and gives it its number.
func (s *Store) Insert(ctx context.Context, r Rule, reason string) (Rule, error) {
	doc, err := json.Marshal(body{Pattern: r.Pattern, ToolKind: r.ToolKind, Action: r.Action})
	if err != nil {
		return Rule{}, err
	}
	err = s.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `INSERT INTO table_rules (workspace, table_id, class, request, state, level, author, risk, until, mission, body, created_at, ended_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0)`, r.Workspace, r.Table, r.Class, r.Request, string(r.State), string(r.Level), r.Author, string(r.Risk),
			ms(r.Until), r.Mission, string(doc), ms(r.CreatedAt))
		if err != nil {
			return err
		}
		if r.ID, err = res.LastInsertId(); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO table_rule_states (rule, at, state, reason, by) VALUES (?, ?, ?, ?, ?)`, r.ID, ms(r.CreatedAt), string(r.State), reason, r.Author)
		return err
	})
	if err != nil {
		return Rule{}, fmt.Errorf("register a table rule: %w", err)
	}
	return r, nil
}

// Get returns a rule.
func (s *Store) Get(ctx context.Context, id int64) (Rule, error) {
	r, err := scanRule(s.DB.Reader().QueryRowContext(ctx, `SELECT `+ruleColumns+` FROM table_rules WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Rule{}, ErrNoRule
	}
	return r, err
}

func (s *Store) query(ctx context.Context, where string, args ...any) ([]Rule, error) {
	rows, err := s.DB.Reader().QueryContext(ctx, `SELECT `+ruleColumns+` FROM table_rules WHERE `+where+` ORDER BY id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Rule
	for rows.Next() {
		r, err := scanRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Live lists the rules that are not over (active or suspended), of one
// workspace ("" for all).
func (s *Store) Live(ctx context.Context, workspace string) ([]Rule, error) {
	if workspace == "" {
		return s.query(ctx, `state IN ('active', 'suspended')`)
	}
	return s.query(ctx, `state IN ('active', 'suspended') AND workspace = ?`, workspace)
}

// OfTable lists the rules of a table, over or not.
func (s *Store) OfTable(ctx context.Context, workspace, table string) ([]Rule, error) {
	return s.query(ctx, `workspace = ? AND table_id = ?`, workspace, table)
}

// Active lists the rules in force for the characters of a class in a table.
func (s *Store) Active(ctx context.Context, workspace, table, class string) ([]Rule, error) {
	return s.query(ctx, `state = 'active' AND workspace = ? AND table_id = ? AND class = ?`, workspace, table, class)
}

// Transition changes the state of a rule that is in one of from, writes the
// change to the history and says whether it happened.
func (s *Store) Transition(ctx context.Context, id int64, to State, at time.Time, reason, by string, from ...State) (bool, error) {
	marks := strings.TrimSuffix(strings.Repeat("?,", len(from)), ",")
	end := int64(0)
	if to.Ended() {
		end = ms(at)
	}
	args := []any{string(to), end, end, id}
	for _, f := range from {
		args = append(args, string(f))
	}
	var done bool
	err := s.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE table_rules SET state = ?, ended_at = CASE WHEN ? > 0 THEN ? ELSE ended_at END WHERE id = ? AND state IN (`+marks+`)`, args...)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil || n == 0 {
			return err
		}
		done = true
		_, err = tx.ExecContext(ctx, `INSERT INTO table_rule_states (rule, at, state, reason, by) VALUES (?, ?, ?, ?, ?)`, id, ms(at), string(to), reason, by)
		return err
	})
	return done, err
}

// History lists the changes of state of a rule.
func (s *Store) History(ctx context.Context, id int64) ([]Change, error) {
	rows, err := s.DB.Reader().QueryContext(ctx, `SELECT at, state, reason, by FROM table_rule_states WHERE rule = ? ORDER BY id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Change
	for rows.Next() {
		var c Change
		var at int64
		var st string
		if err := rows.Scan(&at, &st, &c.Reason, &c.By); err != nil {
			return nil, err
		}
		c.At, c.State = fromMs(at), State(st)
		out = append(out, c)
	}
	return out, rows.Err()
}

// AddUse records what a rule let through.
func (s *Store) AddUse(ctx context.Context, rule int64, u Use) error {
	return s.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO table_rule_uses (rule, at, character, action) VALUES (?, ?, ?, ?)`, rule, ms(u.At), u.Character, u.Action)
		return err
	})
}

// Uses lists what a rule let through.
func (s *Store) Uses(ctx context.Context, rule int64) ([]Use, error) {
	rows, err := s.DB.Reader().QueryContext(ctx, `SELECT at, character, action FROM table_rule_uses WHERE rule = ? ORDER BY id`, rule)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Use
	for rows.Next() {
		var u Use
		var at int64
		if err := rows.Scan(&at, &u.Character, &u.Action); err != nil {
			return nil, err
		}
		u.At = fromMs(at)
		out = append(out, u)
	}
	return out, rows.Err()
}
