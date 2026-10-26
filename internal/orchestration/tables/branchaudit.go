package tables

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/mestre-ia/mestre/internal/gitx"
)

// BranchDeleteAction is the audit action of the record the engine keeps of the
// tip of a branch before it takes the branch out of the mirror
// (docs/orquestracao/mesas.md#encerramento).
const BranchDeleteAction = "table.branch_delete"

// deleteBranchAudited takes the branch of a table out of the mirror, but first
// records in the audit trail the table, the branch and the commit it points at:
// the branch is recovered with `git branch <name> <sha>` while git does not
// collect the commit. Without the record written the removal is not asked. A
// branch that is not there needs neither.
func (s *Service) deleteBranchAudited(ctx context.Context, mirror gitx.Repo, t Table) error {
	tip, err := mirror.ResolveRef(ctx, "refs/heads/"+t.Branch)
	switch {
	case errors.Is(err, gitx.ErrRefNotFound):
		return nil
	case err != nil:
		return fmt.Errorf("read the tip of the branch %s: %w", t.Branch, err)
	}
	detail, err := json.Marshal(map[string]string{"workspace": t.Workspace, "table": t.ID, "branch": t.Branch, "sha": string(tip)})
	if err != nil {
		return err
	}
	id := fmt.Sprintf("%s:%s/%s:%s", BranchDeleteAction, t.Workspace, t.ID, tip)
	audit := s.o.Audit
	if audit == nil {
		audit = s.o.Registry.Audit
	}
	if err := audit(ctx, id, s.clk.Now(), s.o.Machine, BranchDeleteAction, detail); err != nil {
		return fmt.Errorf("record the tip of the branch %s before deleting it: %w", t.Branch, err)
	}
	if err := mirror.DeleteBranch(ctx, t.Branch); err != nil && !errors.Is(err, gitx.ErrRefNotFound) {
		return fmt.Errorf("delete the branch %s: %w", t.Branch, err)
	}
	return nil
}
