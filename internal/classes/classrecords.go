package classes

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/mestre-ia/mestre/internal/store"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// LevelTableMaster is the Mestre da mesa: the author of a change of a class
// of its table and, as an agent of the approval chain, of the temporary
// table rules (M4), which like the Grand Master's decisions only turn an ask
// into allow.
const LevelTableMaster Level = "table_master"

// Author is who asks for a change of a class, or approves one.
type Author struct {
	Level Level
	// ID names the person or the character, for the record.
	ID string
	// Table is the table the Mestre da mesa is the master of: its right
	// over table classes stops there.
	Table string
}

// ClassKey names a class: its scope, the table of a table class ("" for the
// others) and its name.
type ClassKey struct {
	Scope ClassScope
	Table string
	Name  string
}

func (k ClassKey) String() string {
	if k.Scope == ClassTable {
		return fmt.Sprintf("%s (mesa %s)", k.Name, k.Table)
	}
	return fmt.Sprintf("%s (%s)", k.Name, scopeLabel(k.Scope))
}

func scopeLabel(s ClassScope) string {
	switch s {
	case ClassGlobal:
		return "global"
	case ClassWorkspace:
		return "workspace"
	}
	return "mesa"
}

// ClassRecord is the approved version of a class, as the database keeps it:
// the hash and the package, so the approved version holds whatever the
// files on disk say.
type ClassRecord struct {
	Key        ClassKey
	Hash       string
	Files      Files
	App        string
	Version    int64
	ApprovedAt time.Time
	By         Author
}

// ProposalOrigin is where a proposal came from.
type ProposalOrigin string

// The origins: a change someone made through the Mestre but could not
// apply alone, or the files on disk found different from the approved ones.
const (
	OriginRequest ProposalOrigin = "request"
	OriginDisk    ProposalOrigin = "disk"
)

// ProposalState is where a proposal is.
type ProposalState string

// The states.
const (
	ProposalPending    ProposalState = "pending"
	ProposalApproved   ProposalState = "approved"
	ProposalSuperseded ProposalState = "superseded"
)

// Proposal is a change of a class that waits for approval.
type Proposal struct {
	ID     int64
	Key    ClassKey
	Hash   string
	Files  Files
	Origin ProposalOrigin
	// By is who proposed it ("" for a change found on disk).
	By string
	// Problems are what makes the proposed package invalid; a proposal with
	// problems cannot be approved.
	Problems  []string
	State     ProposalState
	CreatedAt time.Time
}

// ClassRecords keeps the approved versions and the proposals of the classes
// (the Guild database: ClassDB). workspace is "" for the global classes.
type ClassRecords interface {
	// Approved is the approved version of a class.
	Approved(ctx context.Context, workspace string, k ClassKey) (ClassRecord, bool, error)
	// AllApproved lists the approved versions of a workspace.
	AllApproved(ctx context.Context, workspace string) ([]ClassRecord, error)
	// Approve records a new approved version (the version number grows) and
	// closes the proposal `closing` (0: none) as approved, with every other
	// pending proposal of the class superseded.
	Approve(ctx context.Context, workspace string, rec ClassRecord, closing int64) error
	// Propose records a pending proposal. A pending proposal of the same
	// class and hash is returned instead of a new one (created is false); the
	// other pending proposals of the class with the same origin are
	// superseded.
	Propose(ctx context.Context, workspace string, p Proposal) (got Proposal, created bool, err error)
	// Proposal finds one by its number.
	Proposal(ctx context.Context, workspace string, id int64) (Proposal, bool, error)
	// Pending lists the pending proposals of a workspace.
	Pending(ctx context.Context, workspace string) ([]Proposal, error)
	// Supersede closes the pending proposals of a class from an origin.
	Supersede(ctx context.Context, workspace string, k ClassKey, origin ProposalOrigin) error
	// Delete forgets the approved version of a class and supersedes every
	// pending proposal of it.
	Delete(ctx context.Context, workspace string, k ClassKey) error
}

// ClassDB keeps the classes in the Guild database, on the Citadel.
type ClassDB struct {
	DB    *store.DB
	Clock clock.Clock
}

var _ ClassRecords = ClassDB{}

func (c ClassDB) now() int64 { return c.Clock.Now().UTC().UnixMilli() }

func encodeFiles(f Files) ([]byte, error) { return json.Marshal([]File(f)) }

func decodeFiles(b []byte) (Files, error) {
	var fs []File
	if err := json.Unmarshal(b, &fs); err != nil {
		return nil, err
	}
	return NewFiles(fs)
}

// Approved implements ClassRecords.
func (c ClassDB) Approved(ctx context.Context, ws string, k ClassKey) (ClassRecord, bool, error) {
	rows, err := c.records(ctx, `WHERE workspace = ? AND scope = ? AND tbl = ? AND name = ?`, ws, string(k.Scope), k.Table, k.Name)
	if err != nil || len(rows) == 0 {
		return ClassRecord{}, false, err
	}
	return rows[0], true, nil
}

// AllApproved implements ClassRecords.
func (c ClassDB) AllApproved(ctx context.Context, ws string) ([]ClassRecord, error) {
	return c.records(ctx, `WHERE workspace = ? ORDER BY scope, tbl, name`, ws)
}

func (c ClassDB) records(ctx context.Context, where string, args ...any) ([]ClassRecord, error) {
	rows, err := c.DB.Reader().QueryContext(ctx, `SELECT scope, tbl, name, hash, package, app, version, approved_at, approved_level, approved_by
		FROM classes `+where, args...)
	if err != nil {
		return nil, fmt.Errorf("approved classes: %w", err)
	}
	defer rows.Close()
	var out []ClassRecord
	for rows.Next() {
		var (
			r        ClassRecord
			scope    string
			pkg      []byte
			at       int64
			level, b string
		)
		if err := rows.Scan(&scope, &r.Key.Table, &r.Key.Name, &r.Hash, &pkg, &r.App, &r.Version, &at, &level, &b); err != nil {
			return nil, fmt.Errorf("approved classes: %w", err)
		}
		r.Key.Scope = ClassScope(scope)
		if r.Files, err = decodeFiles(pkg); err != nil {
			return nil, fmt.Errorf("approved class %s: %w", r.Key, err)
		}
		r.ApprovedAt, r.By = time.UnixMilli(at).UTC(), Author{Level: Level(level), ID: b}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Approve implements ClassRecords.
func (c ClassDB) Approve(ctx context.Context, ws string, rec ClassRecord, closing int64) error {
	pkg, err := encodeFiles(rec.Files)
	if err != nil {
		return err
	}
	k := rec.Key
	return c.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		var version int64
		err := tx.QueryRowContext(ctx, `SELECT version FROM classes WHERE workspace = ? AND scope = ? AND tbl = ? AND name = ?`,
			ws, string(k.Scope), k.Table, k.Name).Scan(&version)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO classes (workspace, scope, tbl, name, hash, package, app, version, approved_at, approved_level, approved_by)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (workspace, scope, tbl, name) DO UPDATE SET hash = excluded.hash, package = excluded.package, app = excluded.app,
				version = excluded.version, approved_at = excluded.approved_at, approved_level = excluded.approved_level, approved_by = excluded.approved_by`,
			ws, string(k.Scope), k.Table, k.Name, rec.Hash, pkg, rec.App, version+1, rec.ApprovedAt.UTC().UnixMilli(), string(rec.By.Level), rec.By.ID); err != nil {
			return err
		}
		if closing > 0 {
			if _, err := tx.ExecContext(ctx, `UPDATE class_proposals SET state = ? WHERE id = ? AND workspace = ?`, string(ProposalApproved), closing, ws); err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx, `UPDATE class_proposals SET state = ? WHERE workspace = ? AND scope = ? AND tbl = ? AND name = ? AND state = ?`,
			string(ProposalSuperseded), ws, string(k.Scope), k.Table, k.Name, string(ProposalPending))
		return err
	})
}

// Propose implements ClassRecords.
func (c ClassDB) Propose(ctx context.Context, ws string, p Proposal) (Proposal, bool, error) {
	pkg, err := encodeFiles(p.Files)
	if err != nil {
		return Proposal{}, false, err
	}
	problems, err := json.Marshal(p.Problems)
	if err != nil {
		return Proposal{}, false, err
	}
	k := p.Key
	var got Proposal
	created := false
	err = c.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		var id int64
		err := tx.QueryRowContext(ctx, `SELECT id FROM class_proposals WHERE workspace = ? AND scope = ? AND tbl = ? AND name = ? AND hash = ? AND state = ?`,
			ws, string(k.Scope), k.Table, k.Name, p.Hash, string(ProposalPending)).Scan(&id)
		switch {
		case err == nil:
			got, created = p, false
			got.ID = id
			return nil
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE class_proposals SET state = ? WHERE workspace = ? AND scope = ? AND tbl = ? AND name = ? AND origin = ? AND state = ?`,
			string(ProposalSuperseded), ws, string(k.Scope), k.Table, k.Name, string(p.Origin), string(ProposalPending)); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO class_proposals (workspace, scope, tbl, name, hash, package, origin, proposed_by, problems, state, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			ws, string(k.Scope), k.Table, k.Name, p.Hash, pkg, string(p.Origin), p.By, string(problems), string(ProposalPending), c.now())
		if err != nil {
			return err
		}
		id, err = res.LastInsertId()
		got, created = p, true
		got.ID, got.State = id, ProposalPending
		return err
	})
	if err != nil {
		return Proposal{}, false, fmt.Errorf("proposal for class %s: %w", k, err)
	}
	if created {
		got.CreatedAt = time.UnixMilli(c.now()).UTC()
	}
	return got, created, nil
}

// Proposal implements ClassRecords.
func (c ClassDB) Proposal(ctx context.Context, ws string, id int64) (Proposal, bool, error) {
	ps, err := c.proposals(ctx, `WHERE workspace = ? AND id = ?`, ws, id)
	if err != nil || len(ps) == 0 {
		return Proposal{}, false, err
	}
	return ps[0], true, nil
}

// Pending implements ClassRecords.
func (c ClassDB) Pending(ctx context.Context, ws string) ([]Proposal, error) {
	return c.proposals(ctx, `WHERE workspace = ? AND state = ? ORDER BY id`, ws, string(ProposalPending))
}

func (c ClassDB) proposals(ctx context.Context, where string, args ...any) ([]Proposal, error) {
	rows, err := c.DB.Reader().QueryContext(ctx, `SELECT id, scope, tbl, name, hash, package, origin, proposed_by, problems, state, created_at
		FROM class_proposals `+where, args...)
	if err != nil {
		return nil, fmt.Errorf("class proposals: %w", err)
	}
	defer rows.Close()
	var out []Proposal
	for rows.Next() {
		var (
			p                    Proposal
			scope, origin, state string
			pkg                  []byte
			problems             string
			at                   int64
		)
		if err := rows.Scan(&p.ID, &scope, &p.Key.Table, &p.Key.Name, &p.Hash, &pkg, &origin, &p.By, &problems, &state, &at); err != nil {
			return nil, fmt.Errorf("class proposals: %w", err)
		}
		p.Key.Scope, p.Origin, p.State = ClassScope(scope), ProposalOrigin(origin), ProposalState(state)
		if p.Files, err = decodeFiles(pkg); err != nil {
			return nil, fmt.Errorf("class proposal %d: %w", p.ID, err)
		}
		if err := json.Unmarshal([]byte(problems), &p.Problems); err != nil {
			return nil, fmt.Errorf("class proposal %d: %w", p.ID, err)
		}
		p.CreatedAt = time.UnixMilli(at).UTC()
		out = append(out, p)
	}
	return out, rows.Err()
}

// Supersede implements ClassRecords.
func (c ClassDB) Supersede(ctx context.Context, ws string, k ClassKey, origin ProposalOrigin) error {
	return c.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE class_proposals SET state = ? WHERE workspace = ? AND scope = ? AND tbl = ? AND name = ? AND origin = ? AND state = ?`,
			string(ProposalSuperseded), ws, string(k.Scope), k.Table, k.Name, string(origin), string(ProposalPending))
		return err
	})
}

// Delete implements ClassRecords.
func (c ClassDB) Delete(ctx context.Context, ws string, k ClassKey) error {
	return c.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM classes WHERE workspace = ? AND scope = ? AND tbl = ? AND name = ?`, ws, string(k.Scope), k.Table, k.Name); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE class_proposals SET state = ? WHERE workspace = ? AND scope = ? AND tbl = ? AND name = ? AND state = ?`,
			string(ProposalSuperseded), ws, string(k.Scope), k.Table, k.Name, string(ProposalPending))
		return err
	})
}
