package characters

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lucasndas/mestre/internal/store"
)

// Registry keeps the characters, their state history and the requests that
// wait for a vacancy in the Guild database (tables characters,
// character_state_log, character_dones and character_slot_waits), on the
// Citadel; the panel reads the same rows.
type Registry struct{ DB *store.DB }

const columns = `id, name, kind, role, workspace, table_id, machine_id, sheet, base, class, account, terminal_id, pty_pid, pty_started, pty_version, dir,
	state, reason, reduced_monitoring, pinned, lifecycle, max_idle_ms, created_by, created_by_kind, quest, quest_step,
	provider_session_id, task_open, idle_since, stop_requested_at, stop_requested_by, stop_reason,
	created_at, started_at, last_signal_at, stopped_at`

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

type scanner interface{ Scan(dest ...any) error }

func scan(r scanner) (Character, error) {
	var (
		c                                    Character
		kind, role, state, lifecycle, byKind string
		pinned, taskOpen, ptyPID             int
		maxIdle, idle, stopAt                int64
		created, started, signal, stopped    int64
	)
	if err := r.Scan(&c.ID, &c.Name, &kind, &role, &c.Workspace, &c.Table, &c.Machine, &c.Sheet, &c.Base, &c.Class,
		&c.Account, &c.TerminalID, &ptyPID, &c.PTY.Started, &c.PTY.Version, &c.Dir, &state, &c.Reason, &c.Reduced, &pinned, &lifecycle, &maxIdle,
		&c.CreatedBy.ID, &byKind, &c.Quest, &c.Step, &c.ProviderSessionID, &taskOpen, &idle, &stopAt, &c.StopRequestedBy,
		&c.StopReason, &created, &started, &signal, &stopped); err != nil {
		return Character{}, err
	}
	c.Kind, c.Role, c.State, c.Lifecycle = Kind(kind), Role(role), State(state), Lifecycle(lifecycle)
	c.CreatedBy.Kind = CreatorKind(byKind)
	c.PTY.PID = ptyPID
	c.Pinned, c.TaskOpen = pinned != 0, taskOpen != 0
	c.MaxIdle = time.Duration(maxIdle) * time.Millisecond
	c.IdleSince, c.StopRequestedAt = fromMs(idle), fromMs(stopAt)
	c.CreatedAt, c.StartedAt, c.LastSignalAt, c.StoppedAt = fromMs(created), fromMs(started), fromMs(signal), fromMs(stopped)
	return c, nil
}

// Get returns the character with the id (ErrNoCharacter).
func (r Registry) Get(ctx context.Context, id string) (Character, error) {
	c, err := scan(r.DB.Reader().QueryRowContext(ctx, `SELECT `+columns+` FROM characters WHERE id = ?`, id))
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return Character{}, fmt.Errorf("%w: %s", ErrNoCharacter, id)
	case err != nil:
		return Character{}, fmt.Errorf("character %s: %w", id, err)
	}
	return c, nil
}

func (r Registry) list(ctx context.Context, where string, args ...any) ([]Character, error) {
	q := `SELECT ` + columns + ` FROM characters`
	if where != "" {
		q += ` WHERE ` + where
	}
	rows, err := r.DB.Reader().QueryContext(ctx, q+` ORDER BY created_at, id`, args...)
	if err != nil {
		return nil, fmt.Errorf("list characters: %w", err)
	}
	defer rows.Close()
	var out []Character
	for rows.Next() {
		c, err := scan(rows)
		if err != nil {
			return nil, fmt.Errorf("list characters: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Alive lists the characters that are not stopped, oldest first.
func (r Registry) Alive(ctx context.Context) ([]Character, error) {
	return r.list(ctx, `state <> 'stopped'`)
}

// AliveOf lists the living characters of a workspace.
func (r Registry) AliveOf(ctx context.Context, workspace string) ([]Character, error) {
	return r.list(ctx, `state <> 'stopped' AND workspace = ?`, workspace)
}

// OnMachine lists the living characters that run on a machine.
func (r Registry) OnMachine(ctx context.Context, machine string) ([]Character, error) {
	return r.list(ctx, `state <> 'stopped' AND machine_id = ?`, machine)
}

// ByName finds the living character with the name in the table, without
// difference of case.
func (r Registry) ByName(ctx context.Context, workspace, table, name string) (Character, error) {
	cs, err := r.list(ctx, `state <> 'stopped' AND workspace = ? AND table_id = ? AND name_key = ?`, workspace, table, nameKey(name))
	switch {
	case err != nil:
		return Character{}, err
	case len(cs) == 0:
		return Character{}, fmt.Errorf("%w: %s", ErrNoCharacter, name)
	}
	return cs[0], nil
}

// TableMaster is the living table master of a table, if there is one.
func (r Registry) TableMaster(ctx context.Context, workspace, table string) (Character, bool, error) {
	cs, err := r.list(ctx, `state NOT IN ('stopped', 'stopping') AND role = 'table_master' AND workspace = ? AND table_id = ?`, workspace, table)
	if err != nil || len(cs) == 0 {
		return Character{}, false, err
	}
	return cs[0], true, nil
}

// GrandMaster is the living Grand Master of a workspace, if there is one.
func (r Registry) GrandMaster(ctx context.Context, workspace string) (Character, bool, error) {
	cs, err := r.list(ctx, `state NOT IN ('stopped', 'stopping') AND role = 'grand_master' AND workspace = ?`, workspace)
	if err != nil || len(cs) == 0 {
		return Character{}, false, err
	}
	return cs[0], true, nil
}

// NameTaken is the check of a name in a table; the insert repeats it in its
// own transaction.
func (r Registry) NameTaken(ctx context.Context, workspace, table, name string) (bool, error) {
	var n int
	err := r.DB.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM characters WHERE state <> 'stopped' AND workspace = ? AND table_id = ? AND name_key = ?`,
		workspace, table, nameKey(name)).Scan(&n)
	return n > 0, err
}

// Insert creates the row of a character, with its first line of the state
// history, refusing a name a living character of the table already has.
func (r Registry) Insert(ctx context.Context, c Character) error {
	return r.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM characters WHERE state <> 'stopped' AND workspace = ? AND table_id = ? AND name_key = ?`,
			c.Workspace, c.Table, nameKey(c.Name)).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return fmt.Errorf("%w: %s", ErrNameTaken, c.Name)
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO characters (`+columns+`, name_key) VALUES (`+placeholders(37)+`)`,
			c.ID, c.Name, string(c.Kind), string(c.Role), c.Workspace, c.Table, c.Machine, c.Sheet, c.Base, c.Class, c.Account,
			c.TerminalID, c.PTY.PID, c.PTY.Started, c.PTY.Version, c.Dir, string(c.State), c.Reason, c.Reduced, b2i(c.Pinned), string(c.Lifecycle), c.MaxIdle.Milliseconds(),
			c.CreatedBy.ID, string(c.CreatedBy.Kind), c.Quest, c.Step, c.ProviderSessionID, b2i(c.TaskOpen), ms(c.IdleSince),
			ms(c.StopRequestedAt), c.StopRequestedBy, c.StopReason, ms(c.CreatedAt), ms(c.StartedAt), ms(c.LastSignalAt),
			ms(c.StoppedAt), nameKey(c.Name))
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO character_state_log (character_id, from_state, to_state, reason, at) VALUES (?, '', ?, ?, ?)`,
			c.ID, string(c.State), c.Reason, ms(c.CreatedAt))
		return err
	})
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Delete removes a character that never lived (its creation failed) with its
// history.
func (r Registry) Delete(ctx context.Context, id string) error {
	return r.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		for _, q := range []string{
			`DELETE FROM character_dones WHERE character_id = ?`,
			`DELETE FROM character_state_log WHERE character_id = ?`,
			`DELETE FROM characters WHERE id = ?`,
		} {
			if _, err := tx.ExecContext(ctx, q, id); err != nil {
				return err
			}
		}
		return nil
	})
}

// Change is what a transition changes besides the state.
type Change struct {
	// Reason is the reason of waiting and blocked, or the event that caused
	// the change; it goes in the history.
	Reason string
	// At is the time of the transition, by the injectable clock.
	At time.Time
	// Session, when not empty, is the id of the session of the provider.
	Session string
	// Terminal, when not empty, is the terminal that runs the character.
	Terminal string
	// PTY, when it has a PID, is the mestre-pty of that terminal.
	PTY PTYInfo
	// Machine and Dir, when not empty, move the character (a resumption on
	// another machine).
	Machine, Dir string
	// Reduced, when set, is the reason of the reduced monitoring.
	Reduced *string
}

// Transition moves a character to a state and writes the line of the
// history, in one transaction. It returns the previous state. Entering idle
// starts the stretch of idle time; leaving it ends it; entering a final state
// writes when it ended. A character already in that state is not a
// transition: nothing goes to the history (changed is false), though a new
// reason, session or terminal is kept. Validating the edge is the
// caller's business: the machine is the truth about its processes.
func (r Registry) Transition(ctx context.Context, id string, to State, ch Change) (from State, changed bool, err error) {
	err = r.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		var cur, reason string
		if err := tx.QueryRowContext(ctx, `SELECT state, reason FROM characters WHERE id = ?`, id).Scan(&cur, &reason); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("%w: %s", ErrNoCharacter, id)
			}
			return err
		}
		from = State(cur)
		if from == to {
			// Not a transition: the reason may change, the rest is kept.
			var sets []string
			var args []any
			if reason != ch.Reason {
				sets, args = append(sets, `reason = ?`), append(args, ch.Reason)
			}
			for col, v := range map[string]string{"provider_session_id": ch.Session, "terminal_id": ch.Terminal, "machine_id": ch.Machine, "dir": ch.Dir} {
				if v != "" {
					sets, args = append(sets, col+` = ?`), append(args, v)
				}
			}
			if ch.Reduced != nil {
				sets, args = append(sets, `reduced_monitoring = ?`), append(args, *ch.Reduced)
			}
			if ch.PTY.PID != 0 {
				sets, args = append(sets, `pty_pid = ?, pty_started = ?, pty_version = ?`), append(args, ch.PTY.PID, ch.PTY.Started, ch.PTY.Version)
			}
			if len(sets) == 0 {
				return nil
			}
			_, err := tx.ExecContext(ctx, `UPDATE characters SET `+strings.Join(sets, ", ")+` WHERE id = ?`, append(args, id)...)
			return err
		}
		changed = true
		sets := []string{`state = ?`, `reason = ?`, `last_signal_at = ?`}
		args := []any{string(to), ch.Reason, ms(ch.At)}
		switch {
		case to == Idle && from != Idle:
			sets, args = append(sets, `idle_since = ?`), append(args, ms(ch.At))
		case to != Idle && from == Idle:
			sets = append(sets, `idle_since = 0`)
		}
		if to == Starting && from != Starting {
			sets, args = append(sets, `started_at = ?`), append(args, ms(ch.At))
		}
		if to == Stopped {
			sets, args = append(sets, `stopped_at = ?`), append(args, ms(ch.At))
		}
		for col, v := range map[string]string{"provider_session_id": ch.Session, "terminal_id": ch.Terminal, "machine_id": ch.Machine, "dir": ch.Dir} {
			if v != "" {
				sets, args = append(sets, col+` = ?`), append(args, v)
			}
		}
		if ch.Reduced != nil {
			sets, args = append(sets, `reduced_monitoring = ?`), append(args, *ch.Reduced)
		}
		if ch.PTY.PID != 0 {
			sets, args = append(sets, `pty_pid = ?, pty_started = ?, pty_version = ?`), append(args, ch.PTY.PID, ch.PTY.Started, ch.PTY.Version)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE characters SET `+strings.Join(sets, ", ")+` WHERE id = ?`, append(args, id)...); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO character_state_log (character_id, from_state, to_state, reason, at) VALUES (?, ?, ?, ?, ?)`,
			id, cur, string(to), ch.Reason, ms(ch.At))
		return err
	})
	return from, changed, err
}

func (r Registry) exec(ctx context.Context, id, set string, args ...any) error {
	res, err := r.DB.Writer().ExecContext(ctx, `UPDATE characters SET `+set+` WHERE id = ?`, append(args, id)...)
	if err != nil {
		return fmt.Errorf("character %s: %w", id, err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return fmt.Errorf("%w: %s", ErrNoCharacter, id)
	}
	return nil
}

// SetPinned pins or unpins a character.
func (r Registry) SetPinned(ctx context.Context, id string, pinned bool) error {
	return r.exec(ctx, id, `pinned = ?`, b2i(pinned))
}

// SetTerminal records the terminal that runs the character.
func (r Registry) SetTerminal(ctx context.Context, id, terminal string) error {
	return r.exec(ctx, id, `terminal_id = ?`, terminal)
}

// SetTaskOpen marks a task or step delivered (true) or answered by a done.
func (r Registry) SetTaskOpen(ctx context.Context, id string, open bool) error {
	return r.exec(ctx, id, `task_open = ?`, b2i(open))
}

// SetStop records a safe end that waits for the next safe point, or clears it.
func (r Registry) SetStop(ctx context.Context, id string, at time.Time, by, reason string) error {
	return r.exec(ctx, id, `stop_requested_at = ?, stop_requested_by = ?, stop_reason = ?`, ms(at), by, reason)
}

// SetReduced marks the monitoring of the character as reduced, with the reason
// ("" clears it).
func (r Registry) SetReduced(ctx context.Context, id, reason string) error {
	return r.exec(ctx, id, `reduced_monitoring = ?`, reason)
}

// SetSession records the id of the session of the provider.
func (r Registry) SetSession(ctx context.Context, id, session string) error {
	return r.exec(ctx, id, `provider_session_id = ?`, session)
}

// SetCreator replaces who created the character (the creator was replaced).
func (r Registry) SetCreator(ctx context.Context, id string, by Creator) error {
	return r.exec(ctx, id, `created_by = ?, created_by_kind = ?`, by.ID, string(by.Kind))
}

// Touch records a signal of the character at a time.
func (r Registry) Touch(ctx context.Context, id string, at time.Time) error {
	return r.exec(ctx, id, `last_signal_at = ?`, ms(at))
}

// Done is a done a character delivered.
type Done struct {
	At   time.Time
	Text string
}

// AddDone keeps the text of a done.
func (r Registry) AddDone(ctx context.Context, id string, d Done) error {
	_, err := r.DB.Writer().ExecContext(ctx, `INSERT INTO character_dones (character_id, at, text) VALUES (?, ?, ?)`, id, ms(d.At), d.Text)
	return err
}

// Dones lists the dones a character delivered, oldest first.
func (r Registry) Dones(ctx context.Context, id string) ([]Done, error) {
	rows, err := r.DB.Reader().QueryContext(ctx, `SELECT at, text FROM character_dones WHERE character_id = ? ORDER BY at, rowid`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Done
	for rows.Next() {
		var d Done
		var at int64
		if err := rows.Scan(&at, &d.Text); err != nil {
			return nil, err
		}
		d.At = fromMs(at)
		out = append(out, d)
	}
	return out, rows.Err()
}

// LogLine is a line of the state history.
type LogLine struct {
	ID        int64
	Character string
	From, To  State
	Reason    string
	At        time.Time
}

// Log lists the state history of a character, in order.
func (r Registry) Log(ctx context.Context, id string) ([]LogLine, error) {
	rows, err := r.DB.Reader().QueryContext(ctx, `SELECT id, character_id, from_state, to_state, reason, at FROM character_state_log WHERE character_id = ? ORDER BY id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LogLine
	for rows.Next() {
		var l LogLine
		var from, to string
		var at int64
		if err := rows.Scan(&l.ID, &l.Character, &from, &to, &l.Reason, &at); err != nil {
			return nil, err
		}
		l.From, l.To, l.At = State(from), State(to), fromMs(at)
		out = append(out, l)
	}
	return out, rows.Err()
}

// DefaultLogRetention is how long the history of states is kept
// (docs/orquestracao/personagens.md#estados).
const DefaultLogRetention = 90 * 24 * time.Hour

// PruneLog deletes the lines older than the retention and returns how many.
func (r Registry) PruneLog(ctx context.Context, now time.Time, retention time.Duration) (int64, error) {
	res, err := r.DB.Writer().ExecContext(ctx, `DELETE FROM character_state_log WHERE at < ?`, ms(now.Add(-retention)))
	if err != nil {
		return 0, fmt.Errorf("clean the state history: %w", err)
	}
	return res.RowsAffected()
}

// SheetUsers lists the names of the living characters born from a sheet
// (presets.Users): a sheet in use is not deleted.
func (r Registry) SheetUsers(ctx context.Context, sheet string) ([]string, error) {
	cs, err := r.list(ctx, `state <> 'stopped' AND sheet = ?`, sheet)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Name
	}
	return out, nil
}

// SlotState is the state of a request that waits for a vacancy.
type SlotState string

// The states of a wait for a vacancy, as the waits of the document name them.
const (
	SlotActive    SlotState = "active"
	SlotSatisfied SlotState = "satisfied"
	SlotExpired   SlotState = "expired"
	SlotCancelled SlotState = "cancelled"
)

// SlotWait is a request that waits for a vacancy (waiting_slot).
type SlotWait struct {
	ID string
	// Request is the JSON of the CreateRequest.
	Request []byte
	State   SlotState
	// Limit says which limit held it ("machine", "table", "workspace",
	// "sheet") and Holders who occupies it.
	Limit, Holders       string
	CreatedAt, ExpiresAt time.Time
	EndedAt              time.Time
}

// AddSlotWait queues a request.
func (r Registry) AddSlotWait(ctx context.Context, w SlotWait) error {
	_, err := r.DB.Writer().ExecContext(ctx, `INSERT INTO character_slot_waits (id, request, state, limit_name, holders, created_at, expires_at) VALUES (?, ?, 'active', ?, ?, ?, ?)`,
		w.ID, string(w.Request), w.Limit, w.Holders, ms(w.CreatedAt), ms(w.ExpiresAt))
	return err
}

// ActiveSlotWaits lists the requests that wait, in the order they arrived.
func (r Registry) ActiveSlotWaits(ctx context.Context) ([]SlotWait, error) {
	return r.slotWaits(ctx, `state = 'active'`)
}

// SlotWaitByID finds a wait.
func (r Registry) SlotWaitByID(ctx context.Context, id string) (SlotWait, error) {
	ws, err := r.slotWaits(ctx, `id = ?`, id)
	if err != nil {
		return SlotWait{}, err
	}
	if len(ws) == 0 {
		return SlotWait{}, fmt.Errorf("no such wait for a vacancy: %s", id)
	}
	return ws[0], nil
}

func (r Registry) slotWaits(ctx context.Context, where string, args ...any) ([]SlotWait, error) {
	rows, err := r.DB.Reader().QueryContext(ctx, `SELECT id, request, state, limit_name, holders, created_at, expires_at, ended_at
		FROM character_slot_waits WHERE `+where+` ORDER BY created_at, rowid`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SlotWait
	for rows.Next() {
		var w SlotWait
		var req, state string
		var created, expires, ended int64
		if err := rows.Scan(&w.ID, &req, &state, &w.Limit, &w.Holders, &created, &expires, &ended); err != nil {
			return nil, err
		}
		w.Request, w.State = []byte(req), SlotState(state)
		w.CreatedAt, w.ExpiresAt, w.EndedAt = fromMs(created), fromMs(expires), fromMs(ended)
		out = append(out, w)
	}
	return out, rows.Err()
}

// EndSlotWait closes a wait with a state, once: it reports false if it was no
// longer active (someone else closed it first).
func (r Registry) EndSlotWait(ctx context.Context, id string, to SlotState, at time.Time) (bool, error) {
	res, err := r.DB.Writer().ExecContext(ctx, `UPDATE character_slot_waits SET state = ?, ended_at = ? WHERE id = ? AND state = 'active'`, string(to), ms(at), id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// SetSlotHold records which limit holds a wait and who occupies it.
func (r Registry) SetSlotHold(ctx context.Context, id, limit, holders string) error {
	_, err := r.DB.Writer().ExecContext(ctx, `UPDATE character_slot_waits SET limit_name = ?, holders = ? WHERE id = ?`, limit, holders, id)
	return err
}

// Audit writes a line of the audit trail of the Guild (audit_log).
func (r Registry) Audit(ctx context.Context, id string, at time.Time, machine, action string, detail []byte) error {
	if len(detail) == 0 {
		detail = []byte("{}")
	}
	_, err := r.DB.Writer().ExecContext(ctx, `INSERT INTO audit_log (id, at, machine_id, action, detail) VALUES (?, ?, ?, ?, ?) ON CONFLICT(id) DO NOTHING`,
		id, ms(at), machine, action, string(detail))
	return err
}
