package github

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/pkg/clock"
)

// ObjectKind is what the engine created on GitHub.
type ObjectKind string

// The kinds of github_objects.
const (
	KindIssue   ObjectKind = "issue"
	KindPR      ObjectKind = "pr"
	KindComment ObjectKind = "comment"
)

// Object is a row of github_objects.
type Object struct {
	Repo         string
	Kind         ObjectKind
	Number       int
	GitHubID     int64
	Table        string
	ParentNumber int
	Op           string
}

// Objects is the registry of what the engine made on GitHub, by table.
type Objects struct {
	DB    *store.DB
	Clock clock.Clock
}

// ErrOtherTable: the object is registered for another table.
var ErrOtherTable = errors.New("github: the object belongs to another table")

// Get returns the object, if registered.
func (o *Objects) Get(ctx context.Context, repo string, kind ObjectKind, number int) (Object, bool, error) {
	var x Object
	err := o.DB.Reader().QueryRowContext(ctx, `SELECT repo, kind, number, github_id, table_id, parent_number, op FROM github_objects WHERE repo = ? AND kind = ? AND number = ?`,
		repo, string(kind), number).Scan(&x.Repo, &x.Kind, &x.Number, &x.GitHubID, &x.Table, &x.ParentNumber, &x.Op)
	if errors.Is(err, sql.ErrNoRows) {
		return Object{}, false, nil
	}
	return x, err == nil, err
}

// ByOp returns the object an operation made, if it did.
func (o *Objects) ByOp(ctx context.Context, op string) (Object, bool, error) {
	var x Object
	err := o.DB.Reader().QueryRowContext(ctx, `SELECT repo, kind, number, github_id, table_id, parent_number, op FROM github_objects WHERE op = ?`, op).
		Scan(&x.Repo, &x.Kind, &x.Number, &x.GitHubID, &x.Table, &x.ParentNumber, &x.Op)
	if errors.Is(err, sql.ErrNoRows) {
		return Object{}, false, nil
	}
	return x, err == nil, err
}

// Register records the object for its table. Registering again for the same
// table is fine; for another table it is ErrOtherTable.
func (o *Objects) Register(ctx context.Context, x Object) error {
	return o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		var table string
		err := tx.QueryRowContext(ctx, `SELECT table_id FROM github_objects WHERE repo = ? AND kind = ? AND number = ?`, x.Repo, string(x.Kind), x.Number).Scan(&table)
		switch {
		case err == nil && table != x.Table:
			return fmt.Errorf("%w: %s#%d is of %s", ErrOtherTable, x.Repo, x.Number, table)
		case err == nil:
			return nil
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO github_objects (repo, kind, number, github_id, table_id, parent_number, op, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			x.Repo, string(x.Kind), x.Number, x.GitHubID, x.Table, x.ParentNumber, x.Op, o.Clock.Now().UnixMilli())
		return err
	})
}
