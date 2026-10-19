package github

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"

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
	// Fingerprint is the hash of the impression of the problem an issue is about
	// (`issue create --fingerprint`).
	Fingerprint string
}

// Objects is the registry of what the engine made on GitHub, by table.
type Objects struct {
	DB    *store.DB
	Clock clock.Clock
}

// ErrOtherTable: the object is registered for another table.
var ErrOtherTable = errors.New("github: the object belongs to another table")

// repoPattern is a repository as GitHub names it: owner and name, nothing else.
var repoPattern = regexp.MustCompile(`^[A-Za-z0-9-]{1,39}/[A-Za-z0-9._-]{1,100}$`)

// NormalizeRepo checks and canonicalises `owner/name`: GitHub treats the case as
// one repository, and a path, a query or a fragment in the name would make a
// request go to another object.
func NormalizeRepo(repo string) (string, error) {
	if !repoPattern.MatchString(repo) || strings.Contains(repo, "..") || strings.HasSuffix(repo, "/.") {
		return "", fmt.Errorf("github: %q is not a repository", repo)
	}
	return strings.ToLower(repo), nil
}

// shared says the kinds that share the numbers of a repository (an issue and a
// PR never have the same number).
func shared(kind ObjectKind) bool { return kind == KindIssue || kind == KindPR }

// Get returns the object, if registered. An issue and a PR are looked up by the
// number alone; a comment has a namespace of its own.
func (o *Objects) Get(ctx context.Context, repo string, kind ObjectKind, number int) (Object, bool, error) {
	repo, err := NormalizeRepo(repo)
	if err != nil {
		return Object{}, false, err
	}
	kinds := []any{string(kind), string(kind)}
	if shared(kind) {
		kinds = []any{string(KindIssue), string(KindPR)}
	}
	var x Object
	err = o.DB.Reader().QueryRowContext(ctx, `SELECT repo, kind, number, github_id, table_id, parent_number, op FROM github_objects WHERE repo = ? AND kind IN (?, ?) AND number = ?`,
		append([]any{repo}, append(kinds, number)...)...).Scan(&x.Repo, &x.Kind, &x.Number, &x.GitHubID, &x.Table, &x.ParentNumber, &x.Op)
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
	repo, err := NormalizeRepo(x.Repo)
	if err != nil {
		return err
	}
	x.Repo = repo
	kinds := []any{string(x.Kind), string(x.Kind)}
	if shared(x.Kind) {
		kinds = []any{string(KindIssue), string(KindPR)}
	}
	return o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		var table string
		err := tx.QueryRowContext(ctx, `SELECT table_id FROM github_objects WHERE repo = ? AND kind IN (?, ?) AND number = ?`, append([]any{x.Repo}, append(kinds, x.Number)...)...).Scan(&table)
		switch {
		case err == nil && table == "" && x.Table != "":
			// made by the Grão-Mestre, no table yet: the first that climbs to it has it
			_, err := tx.ExecContext(ctx, `UPDATE github_objects SET table_id = ? WHERE repo = ? AND kind IN (?, ?) AND number = ?`, append([]any{x.Table, x.Repo}, append(kinds, x.Number)...)...)
			return err
		case err == nil && table != x.Table:
			return fmt.Errorf("%w: %s#%d is of %s", ErrOtherTable, x.Repo, x.Number, table)
		case err == nil:
			return nil
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO github_objects (repo, kind, number, github_id, table_id, parent_number, op, fingerprint, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			x.Repo, string(x.Kind), x.Number, x.GitHubID, x.Table, x.ParentNumber, x.Op, x.Fingerprint, o.Clock.Now().UnixMilli())
		return err
	})
}

// ByFingerprint finds the issues of a repository made for an impression of a
// problem, the newest first.
func (o *Objects) ByFingerprint(ctx context.Context, repo, fingerprint string) ([]Object, error) {
	repo, err := NormalizeRepo(repo)
	if err != nil {
		return nil, err
	}
	rows, err := o.DB.Reader().QueryContext(ctx, `SELECT repo, kind, number, github_id, table_id, parent_number, op, fingerprint FROM github_objects WHERE repo = ? AND kind = 'issue' AND fingerprint = ? ORDER BY number DESC`, repo, fingerprint)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Object
	for rows.Next() {
		var x Object
		var kind string
		if err := rows.Scan(&x.Repo, &kind, &x.Number, &x.GitHubID, &x.Table, &x.ParentNumber, &x.Op, &x.Fingerprint); err != nil {
			return nil, err
		}
		x.Kind = ObjectKind(kind)
		out = append(out, x)
	}
	return out, rows.Err()
}
