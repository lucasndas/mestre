package app

import (
	"context"
	"database/sql"
	"errors"

	"github.com/mestre-ia/mestre/internal/github"
	"github.com/mestre-ia/mestre/internal/orchestration/quests"
	"github.com/mestre-ia/mestre/internal/orchestration/tables"
	"github.com/mestre-ia/mestre/internal/store"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// issueOwner says whether an issue is one the engine registered for a table:
// the issue of a mission is written with its proofs, so a mission takes none
// that belongs to another table or to nobody.
type issueOwner struct {
	db  *store.DB
	clk clock.Clock
}

var _ quests.IssueOwner = issueOwner{}

// Owns implements quests.IssueOwner.
func (o issueOwner) Owns(ctx context.Context, workspace, table string, issue int) (bool, error) {
	var repo string
	err := o.db.Reader().QueryRowContext(ctx, `SELECT repo FROM workspaces WHERE slug = ?`, workspace).Scan(&repo)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	obj, found, err := (&github.Objects{DB: o.db, Clock: o.clk}).Get(ctx, repo, github.KindIssue, issue)
	if err != nil {
		return false, err
	}
	if found {
		return obj.Kind == github.KindIssue && obj.Table == workspace+"/"+table, nil
	}
	// The epic itself is the table's, though the engine never registered it
	// (it is the root of the tree the guard walks).
	t, err := tables.Store{DB: o.db}.Get(ctx, workspace, table)
	if errors.Is(err, tables.ErrNoTable) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return issue != 0 && t.Epic == issue, nil
}
