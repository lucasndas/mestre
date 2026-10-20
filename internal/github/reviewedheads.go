package github

import (
	"context"
	"database/sql"
	"errors"

	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/pkg/clock"
)

// ReviewedHeads keeps, by repository and PR, the commit the Grão-Mestre last
// read with `gh review`.
type ReviewedHeads struct {
	DB    *store.DB
	Clock clock.Clock
}

// Record keeps the commit the Grão-Mestre has just read.
func (r *ReviewedHeads) Record(ctx context.Context, repo string, pr int, sha string) error {
	repo, err := NormalizeRepo(repo)
	if err != nil {
		return err
	}
	return r.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO github_reviewed_heads (repo, pr, head_sha, at) VALUES (?, ?, ?, ?)
			ON CONFLICT (repo, pr) DO UPDATE SET head_sha = excluded.head_sha, at = excluded.at`, repo, pr, sha, r.Clock.Now().UnixMilli())
		return err
	})
}

// Get is the commit last read ("" when the PR was never read).
func (r *ReviewedHeads) Get(ctx context.Context, repo string, pr int) (string, error) {
	repo, err := NormalizeRepo(repo)
	if err != nil {
		return "", err
	}
	var sha string
	err = r.DB.Reader().QueryRowContext(ctx, `SELECT head_sha FROM github_reviewed_heads WHERE repo = ? AND pr = ?`, repo, pr).Scan(&sha)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return sha, err
}
