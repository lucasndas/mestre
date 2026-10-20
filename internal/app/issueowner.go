package app

import (
	"context"
	"database/sql"
	"errors"

	"github.com/lucasndas/mestre/internal/github"
	"github.com/lucasndas/mestre/internal/orchestration/quests"
	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/pkg/clock"
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
	if err != nil || !found {
		return false, err
	}
	return obj.Kind == github.KindIssue && obj.Table == workspace+"/"+table, nil
}
