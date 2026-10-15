package tables

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lucasndas/mestre/internal/store"
)

// Store is the tables, table_worktrees and bundle_transfers tables of the
// Guild database.
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

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

type scanner interface{ Scan(dest ...any) error }

const tableColumns = `workspace, id, epic, epic_title, branch, base_branch, base_commit, state, paused, paused_reason, paused_by, paused_at,
	pr_number, pr_head, merged, merged_head, merged_at, keep_worktrees, cancel_reason, cancel_remote_done, cancel_branch_until, branch_deleted,
	opened_by, created_at, updated_at, closed_at`

func scanTable(r scanner) (Table, error) {
	var t Table
	var state string
	var paused, merged, keep, deleted, remote int
	var pausedAt, mergedAt, until, created, updated, closed int64
	if err := r.Scan(&t.Workspace, &t.ID, &t.Epic, &t.EpicTitle, &t.Branch, &t.BaseBranch, &t.BaseCommit, &state, &paused, &t.PausedReason, &t.PausedBy, &pausedAt,
		&t.PRNumber, &t.PRHead, &merged, &t.MergedHead, &mergedAt, &keep, &t.CancelReason, &remote, &until, &deleted,
		&t.OpenedBy, &created, &updated, &closed); err != nil {
		return Table{}, err
	}
	t.State, t.Paused, t.Merged, t.KeepWorktrees, t.BranchDeleted, t.CancelRemoteDone = State(state), paused != 0, merged != 0, keep != 0, deleted != 0, remote != 0
	t.PausedAt, t.MergedAt, t.CancelBranchUntil = fromMs(pausedAt), fromMs(mergedAt), fromMs(until)
	t.CreatedAt, t.UpdatedAt, t.ClosedAt = fromMs(created), fromMs(updated), fromMs(closed)
	return t, nil
}

// Get returns a table (ErrNoTable).
func (s Store) Get(ctx context.Context, workspace, id string) (Table, error) {
	t, err := scanTable(s.DB.Reader().QueryRowContext(ctx, `SELECT `+tableColumns+` FROM tables WHERE workspace = ? AND id = ?`, workspace, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Table{}, fmt.Errorf("%w: %s", ErrNoTable, id)
	}
	return t, err
}

func (s Store) list(ctx context.Context, where string, args ...any) ([]Table, error) {
	rows, err := s.DB.Reader().QueryContext(ctx, `SELECT `+tableColumns+` FROM tables `+where+` ORDER BY created_at, id`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Table
	for rows.Next() {
		t, err := scanTable(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// List returns the tables of a workspace ("" for every workspace).
func (s Store) List(ctx context.Context, workspace string) ([]Table, error) {
	if workspace == "" {
		return s.list(ctx, ``)
	}
	return s.list(ctx, `WHERE workspace = ?`, workspace)
}

// Live returns the tables that are not over, of every workspace.
func (s Store) Live(ctx context.Context) ([]Table, error) {
	return s.list(ctx, `WHERE state NOT IN ('closed', 'cancelled')`)
}

// ByEpic returns the table of the epic that is not over, if there is one.
func (s Store) ByEpic(ctx context.Context, workspace string, epic int) (Table, bool, error) {
	ts, err := s.list(ctx, `WHERE workspace = ? AND epic = ? AND state NOT IN ('closed', 'cancelled')`, workspace, epic)
	if err != nil || len(ts) == 0 {
		return Table{}, false, err
	}
	return ts[0], true, nil
}

// Insert adds a table. The epic of a table that is not over is unique, and
// so is the id: either ends as ErrTableExists.
func (s Store) Insert(ctx context.Context, t Table) error {
	err := s.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO tables (workspace, id, epic, epic_title, branch, base_branch, base_commit, state, opened_by, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			t.Workspace, t.ID, t.Epic, t.EpicTitle, t.Branch, t.BaseBranch, t.BaseCommit, string(t.State), t.OpenedBy, ms(t.CreatedAt), ms(t.UpdatedAt))
		return err
	})
	if err != nil && isUnique(err) {
		return fmt.Errorf("%w: epic %d", ErrTableExists, t.Epic)
	}
	return err
}

// Delete removes a table that never got going (the rollback of an opening).
func (s Store) Delete(ctx context.Context, workspace, id string) error {
	return s.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM table_worktrees WHERE workspace = ? AND table_id = ?`, workspace, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM table_ref_alerts WHERE workspace = ? AND table_id = ?`, workspace, id); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM tables WHERE workspace = ? AND id = ?`, workspace, id)
		return err
	})
}

// Move applies an event: the state changes only if the table is still in
// from. It returns whether it did.
func (s Store) Move(ctx context.Context, workspace, id string, from, to State, at time.Time) (bool, error) {
	closed := int64(0)
	if Over(to) {
		closed = ms(at)
	}
	var n int64
	err := s.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		r, err := tx.ExecContext(ctx, `UPDATE tables SET state = ?, updated_at = ?, closed_at = CASE WHEN ? > 0 THEN ? ELSE closed_at END
			WHERE workspace = ? AND id = ? AND state = ?`, string(to), ms(at), closed, closed, workspace, id, string(from))
		if err != nil {
			return err
		}
		n, _ = r.RowsAffected()
		return nil
	})
	return n > 0, err
}

func (s Store) exec(ctx context.Context, workspace, id string, at time.Time, set string, args ...any) error {
	return s.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		r, err := tx.ExecContext(ctx, `UPDATE tables SET `+set+`, updated_at = ? WHERE workspace = ? AND id = ?`, append(args, ms(at), workspace, id)...)
		if err != nil {
			return err
		}
		if n, _ := r.RowsAffected(); n == 0 {
			return fmt.Errorf("%w: %s", ErrNoTable, id)
		}
		return nil
	})
}

// SetPaused puts or takes the mark of the pause.
func (s Store) SetPaused(ctx context.Context, workspace, id string, at time.Time, paused bool, reason, by string) error {
	if !paused {
		return s.exec(ctx, workspace, id, at, `paused = 0, paused_reason = '', paused_by = '', paused_at = 0`)
	}
	return s.exec(ctx, workspace, id, at, `paused = 1, paused_reason = ?, paused_by = ?, paused_at = ?`, reason, by, ms(at))
}

// SetPR records the pull request of the table.
func (s Store) SetPR(ctx context.Context, workspace, id string, at time.Time, number int, head string) error {
	return s.exec(ctx, workspace, id, at, `pr_number = ?, pr_head = ?`, number, head)
}

// SetMerged records the confirmation of the merge, and the commit merged.
func (s Store) SetMerged(ctx context.Context, workspace, id string, at time.Time, head string) error {
	return s.exec(ctx, workspace, id, at, `merged = 1, merged_head = ?, merged_at = ?`, head, ms(at))
}

// SetKeep records that the Grand Master decided to keep the worktrees.
func (s Store) SetKeep(ctx context.Context, workspace, id string, at time.Time) error {
	return s.exec(ctx, workspace, id, at, `keep_worktrees = 1`)
}

// SetCancel records the reason of the cancellation and when the branch may go.
func (s Store) SetCancel(ctx context.Context, workspace, id string, at time.Time, reason string, until time.Time) error {
	return s.exec(ctx, workspace, id, at, `cancel_reason = ?, cancel_branch_until = ?`, reason, ms(until))
}

// SetRemoteClosed records that the pull request and the epic were closed.
func (s Store) SetRemoteClosed(ctx context.Context, workspace, id string, at time.Time) error {
	return s.exec(ctx, workspace, id, at, `cancel_remote_done = 1`)
}

// SetBranchDeleted records that the branch left the mirror.
func (s Store) SetBranchDeleted(ctx context.Context, workspace, id string, at time.Time) error {
	return s.exec(ctx, workspace, id, at, `branch_deleted = 1`)
}

// Worktrees returns the worktrees of a table.
func (s Store) Worktrees(ctx context.Context, workspace, table string) ([]Worktree, error) {
	return s.worktrees(ctx, `WHERE workspace = ? AND table_id = ? ORDER BY machine_id`, workspace, table)
}

// AllWorktrees returns the worktrees of every table of a workspace.
func (s Store) AllWorktrees(ctx context.Context, workspace string) ([]Worktree, error) {
	return s.worktrees(ctx, `WHERE workspace = ? ORDER BY table_id, machine_id`, workspace)
}

func (s Store) worktrees(ctx context.Context, where string, args ...any) ([]Worktree, error) {
	rows, err := s.DB.Reader().QueryContext(ctx, `SELECT workspace, table_id, machine_id, dir, git_dir, common_dir, last_commit, ref_commit, created_at, updated_at FROM table_worktrees `+where, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Worktree
	for rows.Next() {
		var w Worktree
		var created, updated int64
		if err := rows.Scan(&w.Workspace, &w.Table, &w.Machine, &w.Dir, &w.GitDir, &w.Common, &w.LastCommit, &w.RefCommit, &created, &updated); err != nil {
			return nil, err
		}
		w.CreatedAt, w.UpdatedAt = fromMs(created), fromMs(updated)
		out = append(out, w)
	}
	return out, rows.Err()
}

// Worktree returns the worktree of a table on a machine (ErrNoWorktree).
func (s Store) Worktree(ctx context.Context, workspace, table, machine string) (Worktree, error) {
	ws, err := s.worktrees(ctx, `WHERE workspace = ? AND table_id = ? AND machine_id = ?`, workspace, table, machine)
	if err != nil {
		return Worktree{}, err
	}
	if len(ws) == 0 {
		return Worktree{}, fmt.Errorf("%w: %s on %s", ErrNoWorktree, table, machine)
	}
	return ws[0], nil
}

// PutWorktree records the worktree of a table on a machine, replacing the
// record that was there.
func (s Store) PutWorktree(ctx context.Context, w Worktree) error {
	return s.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO table_worktrees (workspace, table_id, machine_id, dir, git_dir, common_dir, last_commit, ref_commit, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (workspace, table_id, machine_id) DO UPDATE SET dir = excluded.dir, git_dir = excluded.git_dir, common_dir = excluded.common_dir,
				last_commit = excluded.last_commit, ref_commit = excluded.ref_commit, updated_at = excluded.updated_at`,
			w.Workspace, w.Table, w.Machine, w.Dir, w.GitDir, w.Common, w.LastCommit, w.RefCommit, ms(w.CreatedAt), ms(w.UpdatedAt))
		return err
	})
}

// SetLastCommit registers the commit the engine saw at the tip of the branch
// on a machine.
func (s Store) SetLastCommit(ctx context.Context, workspace, table, machine, commit string, at time.Time) error {
	return s.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE table_worktrees SET last_commit = ?, updated_at = ? WHERE workspace = ? AND table_id = ? AND machine_id = ?`,
			commit, ms(at), workspace, table, machine)
		return err
	})
}

// SetRefCommit registers the commit of the ref a Tower sent.
func (s Store) SetRefCommit(ctx context.Context, workspace, table, machine, commit string, at time.Time) error {
	return s.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE table_worktrees SET ref_commit = ?, updated_at = ? WHERE workspace = ? AND table_id = ? AND machine_id = ?`,
			commit, ms(at), workspace, table, machine)
		return err
	})
}

// DropWorktree forgets the worktree of a table on a machine.
func (s Store) DropWorktree(ctx context.Context, workspace, table, machine string) error {
	return s.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM table_worktrees WHERE workspace = ? AND table_id = ? AND machine_id = ?`, workspace, table, machine)
		return err
	})
}

// Transfer is a bundle that crossed.
type Transfer struct {
	SHA256, Workspace, Table, Machine, Direction, Tip, Outcome string
	At                                                         time.Time
}

// Directions of a transfer.
const (
	ToTower   = "to_tower"
	ToCitadel = "to_citadel"
)

// Transferred returns the record of a bundle that already crossed.
func (s Store) Transferred(ctx context.Context, sha, machine, direction string) (Transfer, bool, error) {
	var t Transfer
	var at int64
	err := s.DB.Reader().QueryRowContext(ctx, `SELECT sha256, workspace, table_id, machine_id, direction, tip, outcome, at FROM bundle_transfers
		WHERE sha256 = ? AND machine_id = ? AND direction = ?`, sha, machine, direction).
		Scan(&t.SHA256, &t.Workspace, &t.Table, &t.Machine, &t.Direction, &t.Tip, &t.Outcome, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return Transfer{}, false, nil
	}
	t.At = fromMs(at)
	return t, err == nil, err
}

// PutTransfer records a bundle that crossed.
func (s Store) PutTransfer(ctx context.Context, t Transfer) error {
	return s.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO bundle_transfers (sha256, workspace, table_id, machine_id, direction, tip, outcome, at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			t.SHA256, t.Workspace, t.Table, t.Machine, t.Direction, t.Tip, t.Outcome, ms(t.At))
		return err
	})
}

func isUnique(err error) bool {
	return err != nil && (strings.Contains(err.Error(), "UNIQUE constraint failed") || strings.Contains(err.Error(), "PRIMARY KEY"))
}
