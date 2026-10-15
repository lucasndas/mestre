package characters

import (
	"context"
	"errors"

	"github.com/lucasndas/mestre/internal/providers"
	"github.com/lucasndas/mestre/internal/sessionzero"
)

// TableFiles is the session zero of the tables (sessionzero.Table) for the
// Runtime: the check before each character and the trust written again when a
// provider shows its trust screen. Requests builds the request for the
// worktree of a table on this machine (its record, the mirror, the account
// profiles that will run characters there): the tables of stage 8 know it.
type TableFiles struct {
	Table    *sessionzero.Table
	Requests func(ctx context.Context, workspace, table, dir string) (sessionzero.TableRequest, error)
}

var (
	_ TableChecker = TableFiles{}
	_ Retruster    = TableFiles{}
)

func (t TableFiles) request(ctx context.Context, workspace, table, dir string) (sessionzero.TableRequest, error) {
	if t.Table == nil || t.Requests == nil {
		return sessionzero.TableRequest{}, errors.New("characters: the session zero of the table is not wired")
	}
	return t.Requests(ctx, workspace, table, dir)
}

// CheckStart implements TableChecker: Table.CheckStart, which verifies and
// corrects the files of the providers and refuses (sessionzero.BlockedError)
// the provider whose files were not generated.
func (t TableFiles) CheckStart(ctx context.Context, workspace, table, dir string, provider providers.ID) error {
	req, err := t.request(ctx, workspace, table, dir)
	if err != nil {
		return err
	}
	_, err = t.Table.CheckStart(ctx, req, provider)
	return err
}

// Retrust implements Retruster: Table.Prepare writes the trust of the
// worktree in the account profiles again.
func (t TableFiles) Retrust(ctx context.Context, workspace, table, dir string) error {
	req, err := t.request(ctx, workspace, table, dir)
	if err != nil {
		return err
	}
	_, err = t.Table.Prepare(ctx, req)
	return err
}
