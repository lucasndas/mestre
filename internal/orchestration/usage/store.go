package usage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/store"
)

// Store is the usage_* tables of the Guild database.
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

// Insert adds a row of use. day is the date in the time zone of the Guild.
func (s *Store) Insert(ctx context.Context, r Record, day string) error {
	var cost sql.NullFloat64
	if r.CostUSD != nil {
		cost = sql.NullFloat64{Float64: *r.CostUSD, Valid: true}
	}
	return s.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO usage_records (character_id, step, quest, workspace, table_id, profile, provider, model, at, day, source, origin,
			input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, reasoning_tokens, has_tokens, cost_usd, active_ms, turns, estimated)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			r.Character, r.Step, r.Quest, r.Workspace, r.Table, r.Profile, string(r.Provider), r.Model, ms(r.At), day, r.Source, string(r.Origin),
			r.Tokens.Input, r.Tokens.Output, r.Tokens.CacheRead, r.Tokens.CacheWrite, r.Tokens.Reasoning, b2i(r.Tokens.Known), cost,
			r.Active.Milliseconds(), r.Turns, b2i(r.Estimated))
		return err
	})
}

const sumColumns = `COALESCE(SUM(cost_usd), 0), COUNT(cost_usd),
	COALESCE(SUM(CASE WHEN has_tokens = 1 THEN input_tokens + output_tokens + cache_read_tokens + cache_write_tokens + reasoning_tokens ELSE 0 END), 0),
	COALESCE(SUM(has_tokens), 0), COALESCE(SUM(active_ms), 0), COALESCE(SUM(turns), 0)`

func (s *Store) sum(ctx context.Context, where string, args ...any) (Totals, error) {
	var (
		t               Totals
		costN, tokensN  int64
		activeMs, turns int64
	)
	err := s.DB.Reader().QueryRowContext(ctx, `SELECT `+sumColumns+` FROM usage_records WHERE `+where, args...).Scan(&t.CostUSD, &costN, &t.Tokens, &tokensN, &activeMs, &turns)
	t.HasCost, t.HasTokens = costN > 0, tokensN > 0
	t.Active, t.Turns = time.Duration(activeMs)*time.Millisecond, int(turns)
	return t, err
}

// Sum is the use a scope has in a period (the date, for the scopes per day).
// A scope of the work of the steps never counts the rows of the conformance
// or of the class test; the total of the day counts everything.
func (s *Store) Sum(ctx context.Context, scope Scope, key, period string) (Totals, error) {
	switch scope {
	case ScopeQuest:
		return s.sum(ctx, `quest = ? AND origin = 'etapa'`, key)
	case ScopeTable:
		ws, table, ok := strings.Cut(key, "/")
		if !ok {
			return Totals{}, fmt.Errorf("usage: %q is not the key of a table", key)
		}
		return s.sum(ctx, `workspace = ? AND table_id = ? AND origin = 'etapa'`, ws, table)
	case ScopeWorkspace:
		return s.sum(ctx, `workspace = ? AND origin = 'etapa'`, key)
	case ScopeDay:
		return s.sum(ctx, `workspace = ? AND day = ? AND origin = 'etapa'`, key, period)
	case ScopeDayTotal:
		return s.sum(ctx, `day = ?`, period)
	}
	return Totals{}, fmt.Errorf("usage: unknown scope %q", scope)
}

// SumOf is the use of one character.
func (s *Store) SumOf(ctx context.Context, character string) (Totals, error) {
	return s.sum(ctx, `character_id = ?`, character)
}

// SumAccount is the use of an account profile of a provider on a day.
func (s *Store) SumAccount(ctx context.Context, provider, profile, day string) (Totals, error) {
	return s.sum(ctx, `provider = ? AND profile = ? AND day = ?`, provider, profile, day)
}

// Limit finds the limit of a scope.
func (s *Store) Limit(ctx context.Context, scope Scope, key string) (Limit, bool, error) {
	l := Limit{Scope: scope, Key: key}
	var unit string
	err := s.DB.Reader().QueryRowContext(ctx, `SELECT unit, value FROM usage_limits WHERE scope = ? AND key = ?`, string(scope), key).Scan(&unit, &l.Value)
	if errors.Is(err, sql.ErrNoRows) {
		return Limit{}, false, nil
	}
	l.Unit = classes.Unit(unit)
	return l, err == nil, err
}

// Limits lists every limit that is set.
func (s *Store) Limits(ctx context.Context) ([]Limit, error) {
	rows, err := s.DB.Reader().QueryContext(ctx, `SELECT scope, key, unit, value FROM usage_limits ORDER BY scope, key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Limit
	for rows.Next() {
		var l Limit
		var scope, unit string
		if err := rows.Scan(&scope, &l.Key, &unit, &l.Value); err != nil {
			return nil, err
		}
		l.Scope, l.Unit = Scope(scope), classes.Unit(unit)
		out = append(out, l)
	}
	return out, rows.Err()
}

// PutLimit saves a limit.
func (s *Store) PutLimit(ctx context.Context, l Limit) error {
	return s.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		// a changed limit speaks again when it is reached, and what was approved
		// above the old one is in the old unit
		var unit string
		var value float64
		err := tx.QueryRowContext(ctx, `SELECT unit, value FROM usage_limits WHERE scope = ? AND key = ?`, string(l.Scope), l.Key).Scan(&unit, &value)
		if err == nil && unit == string(l.Unit) && value == l.Value {
			return nil
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err := clearLimitState(ctx, tx, l.Scope, l.Key, err == nil && unit != string(l.Unit)); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO usage_limits (scope, key, unit, value) VALUES (?, ?, ?, ?)
			ON CONFLICT (scope, key) DO UPDATE SET unit = excluded.unit, value = excluded.value`, string(l.Scope), l.Key, string(l.Unit), l.Value)
		return err
	})
}

func clearLimitState(ctx context.Context, tx *sql.Tx, scope Scope, key string, extras bool) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM usage_alerts WHERE scope = ? AND key = ?`, string(scope), key); err != nil {
		return err
	}
	if !extras {
		return nil
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM usage_extras WHERE scope = ? AND key = ?`, string(scope), key)
	return err
}

// DeleteLimit removes a limit.
func (s *Store) DeleteLimit(ctx context.Context, scope Scope, key string) error {
	return s.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		if err := clearLimitState(ctx, tx, scope, key, true); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM usage_limits WHERE scope = ? AND key = ?`, string(scope), key)
		return err
	})
}

// Extra is what was approved above the limit in the period.
func (s *Store) Extra(ctx context.Context, scope Scope, key, period string) (float64, error) {
	var v float64
	err := s.DB.Reader().QueryRowContext(ctx, `SELECT extra FROM usage_extras WHERE scope = ? AND key = ? AND period = ?`, string(scope), key, period).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return v, err
}

// Alerted says whether the warning of a level was given for the limit in the
// period.
func (s *Store) Alerted(ctx context.Context, scope Scope, key, period string, level int) (bool, error) {
	var n int
	err := s.DB.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM usage_alerts WHERE scope = ? AND key = ? AND period = ? AND level = ?`, string(scope), key, period, level).Scan(&n)
	return n > 0, err
}

// MarkAlert records the warning of a level; it says whether it was new (the
// one that records it is the one that gives the warning).
func (s *Store) MarkAlert(ctx context.Context, scope Scope, key, period string, level int, at time.Time) (bool, error) {
	fresh := false
	err := s.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `INSERT INTO usage_alerts (scope, key, period, level, at) VALUES (?, ?, ?, ?, ?) ON CONFLICT DO NOTHING`,
			string(scope), key, period, level, ms(at))
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		fresh = n > 0
		return err
	})
	return fresh, err
}

// request is a custo_extra request a limit opened.
type request struct {
	ID               int64
	Scope            Scope
	Key, Period      string
	Unit             classes.Unit
	Workspace, Table string
	Used, Cap, Extra float64
	Paused           []string
	State            string
	At               time.Time
}

// target is what the request's limit paused, to tell two limits on the same
// thing apart from two on different ones.
func (r request) target() string {
	switch r.Scope {
	case ScopeQuest:
		return "q:" + r.Key
	case ScopeTable:
		return "t:" + r.Key
	case ScopeWorkspace, ScopeDay:
		return "w:" + r.Key
	}
	return "all"
}

func (s *Store) openRequests(ctx context.Context) ([]request, error) {
	rows, err := s.DB.Reader().QueryContext(ctx, `SELECT request FROM usage_requests WHERE state = ?`, reqOpen)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	var out []request
	for _, id := range ids {
		r, ok, err := s.request(ctx, id)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, r)
		}
	}
	return out, nil
}

// ClearAlert forgets the warning of a level, so it can be given again.
func (s *Store) ClearAlert(ctx context.Context, scope Scope, key, period string, level int) error {
	return s.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM usage_alerts WHERE scope = ? AND key = ? AND period = ? AND level = ?`, string(scope), key, period, level)
		return err
	})
}

// waitOf says whether a character waits for the quota.
func (s *Store) waitOf(ctx context.Context, character string) (bool, error) {
	var n int
	err := s.DB.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM usage_waits WHERE character_id = ?`, character).Scan(&n)
	return n > 0, err
}

const (
	reqOpen     = "open"
	reqApproved = "approved"
	reqDenied   = "denied"
)

func (s *Store) putRequest(ctx context.Context, r request) error {
	paused, _ := json.Marshal(r.Paused)
	if r.Paused == nil {
		paused = []byte("[]")
	}
	return s.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO usage_requests (request, scope, key, period, unit, workspace, table_id, used, cap, extra, paused, state, at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, r.ID, string(r.Scope), r.Key, r.Period, string(r.Unit), r.Workspace, r.Table, r.Used, r.Cap, r.Extra, string(paused), r.State, ms(r.At))
		return err
	})
}

func (s *Store) request(ctx context.Context, id int64) (request, bool, error) {
	var (
		r             request
		scope, unit   string
		paused, state string
		at            int64
	)
	err := s.DB.Reader().QueryRowContext(ctx, `SELECT scope, key, period, unit, workspace, table_id, used, cap, extra, paused, state, at FROM usage_requests WHERE request = ?`, id).
		Scan(&scope, &r.Key, &r.Period, &unit, &r.Workspace, &r.Table, &r.Used, &r.Cap, &r.Extra, &paused, &state, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return request{}, false, nil
	}
	if err != nil {
		return request{}, false, err
	}
	r.ID, r.Scope, r.Unit, r.State, r.At = id, Scope(scope), classes.Unit(unit), state, fromMs(at)
	_ = json.Unmarshal([]byte(paused), &r.Paused)
	return r, true, nil
}

// settle ends an open request in one step, adding the extra when approved
// and clearing the warning of 100% so the next time the limit is reached it
// speaks again. It says whether this call was the one that ended it.
func (s *Store) settle(ctx context.Context, r request, approved bool) (bool, error) {
	won := false
	err := s.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		state := reqDenied
		if approved {
			state = reqApproved
		}
		res, err := tx.ExecContext(ctx, `UPDATE usage_requests SET state = ? WHERE request = ? AND state = ?`, state, r.ID, reqOpen)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil || n == 0 {
			return err
		}
		won = true
		if !approved {
			return nil
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO usage_extras (scope, key, period, extra) VALUES (?, ?, ?, ?)
			ON CONFLICT (scope, key, period) DO UPDATE SET extra = usage_extras.extra + excluded.extra`, string(r.Scope), r.Key, r.Period, r.Extra); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM usage_alerts WHERE scope = ? AND key = ? AND period = ? AND level = 100`, string(r.Scope), r.Key, r.Period)
		return err
	})
	return won, err
}

// window is the last reading of a window of the plan of a provider.
type window struct {
	Provider, Profile, Name, Bucket string
	UsedPct                         float64
	ResetsAt, ReadAt                time.Time
	NotifiedResets                  time.Time
	WarnedRead                      time.Time
}

func (s *Store) putWindow(ctx context.Context, w window) (cleared bool, err error) {
	err = s.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		var notified, resets int64
		switch e := tx.QueryRowContext(ctx, `SELECT notified_resets, resets_at FROM usage_windows WHERE provider = ? AND profile = ? AND win = ? AND bucket = ?`,
			w.Provider, w.Profile, w.Name, w.Bucket).Scan(&notified, &resets); {
		case e == nil:
			// a new reset drops what was said about the old one; the hold it had
			// is over and what waited must be delivered
			cleared = notified > 0 && resets != ms(w.ResetsAt)
		case !errors.Is(e, sql.ErrNoRows):
			return e
		}
		_, e := tx.ExecContext(ctx, `INSERT INTO usage_windows (provider, profile, win, bucket, used_pct, resets_at, read_at) VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (provider, profile, win, bucket) DO UPDATE SET used_pct = excluded.used_pct,
				notified_resets = CASE WHEN usage_windows.resets_at = excluded.resets_at THEN usage_windows.notified_resets ELSE 0 END,
				resets_at = excluded.resets_at, read_at = excluded.read_at`,
			w.Provider, w.Profile, w.Name, w.Bucket, w.UsedPct, ms(w.ResetsAt), ms(w.ReadAt))
		return e
	})
	return cleared, err
}

func scanWindows(rows *sql.Rows) ([]window, error) {
	defer rows.Close()
	var out []window
	for rows.Next() {
		var w window
		var resets, read, notified, warned int64
		if err := rows.Scan(&w.Provider, &w.Profile, &w.Name, &w.Bucket, &w.UsedPct, &resets, &read, &notified, &warned); err != nil {
			return nil, err
		}
		w.ResetsAt, w.ReadAt, w.NotifiedResets, w.WarnedRead = fromMs(resets), fromMs(read), fromMs(notified), fromMs(warned)
		out = append(out, w)
	}
	return out, rows.Err()
}

const windowColumns = `provider, profile, win, bucket, used_pct, resets_at, read_at, notified_resets, warned_read`

func (s *Store) windows(ctx context.Context, provider, profile string) ([]window, error) {
	rows, err := s.DB.Reader().QueryContext(ctx, `SELECT `+windowColumns+` FROM usage_windows WHERE provider = ? AND profile = ? ORDER BY win, bucket`, provider, profile)
	if err != nil {
		return nil, err
	}
	return scanWindows(rows)
}

// heldDue lists the windows whose reset the start was held until and which
// has passed.
func (s *Store) heldDue(ctx context.Context, now time.Time) ([]window, error) {
	rows, err := s.DB.Reader().QueryContext(ctx, `SELECT `+windowColumns+` FROM usage_windows WHERE notified_resets > 0 AND notified_resets <= ?`, ms(now))
	if err != nil {
		return nil, err
	}
	return scanWindows(rows)
}

// markHeld records that the start is held until a reset and the Mestre was
// told; it says whether it was new.
func (s *Store) markHeld(ctx context.Context, w window, until time.Time) (bool, error) {
	fresh := false
	err := s.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE usage_windows SET notified_resets = ? WHERE provider = ? AND profile = ? AND win = ? AND bucket = ? AND notified_resets <> ?`,
			ms(until), w.Provider, w.Profile, w.Name, w.Bucket, ms(until))
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		fresh = n > 0
		return err
	})
	return fresh, err
}

// clearHeld forgets the hold of a window once it was lifted.
func (s *Store) clearHeld(ctx context.Context, w window) error {
	return s.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE usage_windows SET notified_resets = 0 WHERE provider = ? AND profile = ? AND win = ? AND bucket = ?`, w.Provider, w.Profile, w.Name, w.Bucket)
		return err
	})
}

// markWarned records the reading the warning of unconfirmed use was given
// for; it says whether it was new.
func (s *Store) markWarned(ctx context.Context, w window) (bool, error) {
	fresh := false
	err := s.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE usage_windows SET warned_read = read_at WHERE provider = ? AND profile = ? AND win = ? AND bucket = ? AND warned_read <> read_at`,
			w.Provider, w.Profile, w.Name, w.Bucket)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		fresh = n > 0
		return err
	})
	return fresh, err
}

// Threshold is the start threshold of a workspace, in percent.
func (s *Store) Threshold(ctx context.Context, workspace string) (float64, error) {
	var v float64
	err := s.DB.Reader().QueryRowContext(ctx, `SELECT pct FROM usage_thresholds WHERE workspace = ?`, workspace).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return DefaultThreshold, nil
	}
	return v, err
}

func (s *Store) putThreshold(ctx context.Context, workspace string, pct float64) error {
	return s.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO usage_thresholds (workspace, pct) VALUES (?, ?) ON CONFLICT (workspace) DO UPDATE SET pct = excluded.pct`, workspace, pct)
		return err
	})
}

// wait is a character that waits for the quota.
type wait struct {
	Character, Provider, Profile string
	Until, Since                 time.Time
}

func (s *Store) putWait(ctx context.Context, w wait) (bool, error) {
	fresh := false
	err := s.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM usage_waits WHERE character_id = ?`, w.Character).Scan(&n); err != nil {
			return err
		}
		fresh = n == 0
		_, err := tx.ExecContext(ctx, `INSERT INTO usage_waits (character_id, provider, profile, until, since) VALUES (?, ?, ?, ?, ?)
			ON CONFLICT (character_id) DO UPDATE SET until = excluded.until`, w.Character, w.Provider, w.Profile, ms(w.Until), ms(w.Since))
		return err
	})
	return fresh, err
}

// takeWait removes the wait of a character; it says whether there was one.
func (s *Store) takeWait(ctx context.Context, character string) (bool, error) {
	had := false
	err := s.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `DELETE FROM usage_waits WHERE character_id = ?`, character)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		had = n > 0
		return err
	})
	return had, err
}

// dueWaits lists the characters whose quota is due back.
func (s *Store) dueWaits(ctx context.Context, now time.Time) ([]string, error) {
	rows, err := s.DB.Reader().QueryContext(ctx, `SELECT character_id FROM usage_waits WHERE until > 0 AND until <= ? ORDER BY until, character_id`, ms(now))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// cursor is the totals the last reading of a character gave.
type cursor struct {
	Session                                         string
	Input, Output, CacheRead, CacheWrite, Reasoning int64
	Cost                                            float64
}

func (s *Store) cursor(ctx context.Context, character string) (cursor, bool, error) {
	var c cursor
	err := s.DB.Reader().QueryRowContext(ctx, `SELECT session, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, reasoning_tokens, cost_usd FROM usage_cursors WHERE character_id = ?`, character).
		Scan(&c.Session, &c.Input, &c.Output, &c.CacheRead, &c.CacheWrite, &c.Reasoning, &c.Cost)
	if errors.Is(err, sql.ErrNoRows) {
		return cursor{}, false, nil
	}
	return c, err == nil, err
}

func (s *Store) putCursor(ctx context.Context, character string, c cursor) error {
	return s.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO usage_cursors (character_id, session, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, reasoning_tokens, cost_usd)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT (character_id) DO UPDATE SET session = excluded.session, input_tokens = excluded.input_tokens,
			output_tokens = excluded.output_tokens, cache_read_tokens = excluded.cache_read_tokens, cache_write_tokens = excluded.cache_write_tokens,
			reasoning_tokens = excluded.reasoning_tokens, cost_usd = excluded.cost_usd`,
			character, c.Session, c.Input, c.Output, c.CacheRead, c.CacheWrite, c.Reasoning, c.Cost)
		return err
	})
}
