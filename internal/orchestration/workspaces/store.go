package workspaces

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/mestre-ia/mestre/internal/store"
)

// Store keeps the workspaces in the Guild database (table workspaces), on the
// Citadel; the panel reads the same rows.
type Store struct{ DB *store.DB }

const columns = `slug, repo, repo_key, clone_url, default_branch, state, stage,
	stage_error_code, stage_error_reason, stage_failures, stage_failed_at, stage_retry_at,
	created_at, updated_at, last_fetch_at, fetch_failing_since, fetch_error, fetch_alerted,
	advance_blocked_since, advance_reason, advance_alerted`

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

func scan(r scanner) (Workspace, error) {
	var (
		w                                           Workspace
		repo, key, state, stage                     string
		code, reason                                string
		failures                                    int
		failedAt, retryAt, created, updated, lastAt int64
		failingSince, blockedSince                  int64
		fetchErr, advReason                         string
		fetchAlerted, advAlerted                    int
	)
	if err := r.Scan(&w.Slug, &repo, &key, &w.CloneURL, &w.DefaultBranch, &state, &stage,
		&code, &reason, &failures, &failedAt, &retryAt, &created, &updated, &lastAt,
		&failingSince, &fetchErr, &fetchAlerted, &blockedSince, &advReason, &advAlerted); err != nil {
		return Workspace{}, err
	}
	parsed, err := ParseRepo(repo)
	if err != nil {
		return Workspace{}, fmt.Errorf("workspace %s: %w", w.Slug, err)
	}
	w.Repo, w.State, w.Stage = parsed, State(state), Stage(stage)
	if failures > 0 {
		w.StageFailure = &StageFailure{Code: code, Reason: reason, Failures: failures, At: fromMs(failedAt), RetryAt: fromMs(retryAt)}
	}
	w.CreatedAt, w.UpdatedAt = fromMs(created), fromMs(updated)
	w.Fetch = FetchStatus{LastAt: fromMs(lastAt), FailingSince: fromMs(failingSince), Error: fetchErr, Alerted: fetchAlerted != 0}
	w.Advance = AdvanceStatus{BlockedSince: fromMs(blockedSince), Reason: advReason, Alerted: advAlerted != 0}
	return w, nil
}

// Get returns the workspace with the slug (ErrNotFound).
func (s Store) Get(ctx context.Context, slug string) (Workspace, error) {
	w, err := scan(s.DB.Reader().QueryRowContext(ctx, `SELECT `+columns+` FROM workspaces WHERE slug = ?`, slug))
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return Workspace{}, fmt.Errorf("%w: %s", ErrNotFound, slug)
	case err != nil:
		return Workspace{}, fmt.Errorf("workspace %s: %w", slug, err)
	}
	return w, nil
}

// List returns every workspace, ordered by slug.
func (s Store) List(ctx context.Context) ([]Workspace, error) {
	rows, err := s.DB.Reader().QueryContext(ctx, `SELECT `+columns+` FROM workspaces ORDER BY slug`)
	if err != nil {
		return nil, fmt.Errorf("list workspaces: %w", err)
	}
	defer rows.Close()
	var out []Workspace
	for rows.Next() {
		w, err := scan(rows)
		if err != nil {
			return nil, fmt.Errorf("list workspaces: %w", err)
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// SlugTaken reports whether a workspace has the slug.
func (s Store) SlugTaken(ctx context.Context, slug string) (bool, error) {
	var n int
	if err := s.DB.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM workspaces WHERE slug = ?`, slug).Scan(&n); err != nil {
		return false, fmt.Errorf("slug %s: %w", slug, err)
	}
	return n > 0, nil
}

// RepoSlug returns the slug of the workspace that has the repository, if any.
func (s Store) RepoSlug(ctx context.Context, repo Repo) (string, bool, error) {
	var slug string
	err := s.DB.Reader().QueryRowContext(ctx, `SELECT slug FROM workspaces WHERE repo_key = ?`, repo.Key()).Scan(&slug)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "", false, nil
	case err != nil:
		return "", false, fmt.Errorf("repository %s: %w", repo, err)
	}
	return slug, true, nil
}

// Insert creates the workspace, refusing a repository that is already one
// (RepoTakenError) and a slug that is used (SlugError), in one transaction:
// two creations at once cannot both win.
func (s Store) Insert(ctx context.Context, w Workspace) error {
	return s.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		var other string
		err := tx.QueryRowContext(ctx, `SELECT slug FROM workspaces WHERE repo_key = ?`, w.Repo.Key()).Scan(&other)
		switch {
		case err == nil:
			return &RepoTakenError{Repo: w.Repo.String(), Slug: other}
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}
		taken := func(slug string) bool {
			var n int
			return tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspaces WHERE slug = ?`, slug).Scan(&n) != nil || n > 0
		}
		if taken(w.Slug) {
			return &SlugError{Slug: w.Slug, Reason: "já é de outro workspace", Suggestion: SuggestSlug(w.Slug, taken), kind: ErrSlugTaken}
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO workspaces (slug, repo, repo_key, clone_url, default_branch, state, stage, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			w.Slug, w.Repo.String(), w.Repo.Key(), w.CloneURL, w.DefaultBranch, string(w.State), string(w.Stage), ms(w.CreatedAt), ms(w.UpdatedAt))
		return err
	})
}

func (s Store) exec(ctx context.Context, slug string, now time.Time, set string, args ...any) error {
	args = append(append(args, ms(now)), slug)
	res, err := s.DB.Writer().ExecContext(ctx, `UPDATE workspaces SET `+set+`, updated_at = ? WHERE slug = ?`, args...)
	if err != nil {
		return fmt.Errorf("workspace %s: %w", slug, err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return fmt.Errorf("%w: %s", ErrNotFound, slug)
	}
	return nil
}

// SetState moves the workspace to a state, only from one of the states in
// from (ErrBadTransition otherwise).
func (s Store) SetState(ctx context.Context, slug string, now time.Time, to State, from ...State) error {
	w, err := s.Get(ctx, slug)
	if err != nil {
		return err
	}
	ok := false
	for _, f := range from {
		ok = ok || w.State == f
	}
	if !ok {
		return fmt.Errorf("%w: %s is %s", ErrBadTransition, slug, w.State)
	}
	return s.exec(ctx, slug, now, `state = ?`, string(to))
}

// StageDone moves a preparing workspace past the stage: to the next one, or
// to active after the last. A stage that is not the current one changes
// nothing (ErrBadTransition).
func (s Store) StageDone(ctx context.Context, slug string, now time.Time, stage Stage) error {
	next, state := StageNone, StateActive
	if i := stage.index(); i >= 0 && i+1 < len(Stages) {
		next, state = Stages[i+1], StatePreparing
	}
	res, err := s.DB.Writer().ExecContext(ctx, `UPDATE workspaces SET state = ?, stage = ?, stage_error_code = '', stage_error_reason = '',
		stage_failures = 0, stage_failed_at = 0, stage_retry_at = 0, updated_at = ? WHERE slug = ? AND state = 'preparing' AND stage = ?`,
		string(state), string(next), ms(now), slug, string(stage))
	if err != nil {
		return fmt.Errorf("workspace %s: %w", slug, err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return fmt.Errorf("%w: %s is not at stage %s", ErrBadTransition, slug, stage)
	}
	return nil
}

// StageNone is the stage of a workspace that is not preparing.
const StageNone Stage = ""

// StageFailed marks the failure of the current stage.
func (s Store) StageFailed(ctx context.Context, slug string, now time.Time, f StageFailure) error {
	return s.exec(ctx, slug, now, `stage_error_code = ?, stage_error_reason = ?, stage_failures = ?, stage_failed_at = ?, stage_retry_at = ?`,
		f.Code, f.Reason, f.Failures, ms(f.At), ms(f.RetryAt))
}

// FetchOK records a successful fetch: the outage, if any, is over.
func (s Store) FetchOK(ctx context.Context, slug string, now time.Time) error {
	return s.exec(ctx, slug, now, `last_fetch_at = ?, fetch_failing_since = 0, fetch_error = '', fetch_alerted = 0`, ms(now))
}

// FetchFailed records a failed fetch; since is the start of the outage and
// is kept from the first failure.
func (s Store) FetchFailed(ctx context.Context, slug string, now, since time.Time, reason string) error {
	return s.exec(ctx, slug, now, `fetch_failing_since = ?, fetch_error = ?`, ms(since), reason)
}

// FetchAlerted records that the alert of the outage was sent.
func (s Store) FetchAlerted(ctx context.Context, slug string, now time.Time) error {
	return s.exec(ctx, slug, now, `fetch_alerted = 1`)
}

// AdvanceBlocked records that the advance of the main clone is held back;
// since is kept from the first time.
func (s Store) AdvanceBlocked(ctx context.Context, slug string, now, since time.Time, reason string) error {
	return s.exec(ctx, slug, now, `advance_blocked_since = ?, advance_reason = ?`, ms(since), reason)
}

// AdvanceClear records that the main clone is up to date.
func (s Store) AdvanceClear(ctx context.Context, slug string, now time.Time) error {
	return s.exec(ctx, slug, now, `advance_blocked_since = 0, advance_reason = '', advance_alerted = 0`)
}

// AdvanceAlerted records that the Grand Master was told.
func (s Store) AdvanceAlerted(ctx context.Context, slug string, now time.Time) error {
	return s.exec(ctx, slug, now, `advance_alerted = 1`)
}

// ClearStageFailure forgets the failure of the current stage: it is waiting
// for something outside, which is not an error.
func (s Store) ClearStageFailure(ctx context.Context, slug string, now time.Time) error {
	return s.exec(ctx, slug, now, `stage_error_code = '', stage_error_reason = '', stage_failures = 0, stage_failed_at = 0, stage_retry_at = 0`)
}
