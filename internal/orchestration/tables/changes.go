package tables

import (
	"context"
	"errors"

	"github.com/lucasndas/mestre/internal/gitx"
)

// Changes is what the branch of a table did since a commit
// (docs/orquestracao/missoes.md#execução).
type Changes struct {
	// Head is the commit at the head of the branch now.
	Head string
	// Files are the files that changed between the commit and Head, as the
	// name-status listing of the version control says them.
	Files []gitx.FileChange
	// Uncommitted are the files with changes that are not committed in the
	// worktree on the machine. It is only known for the Citadel's own
	// machine: a Tower tells it in the report of its sending (Tower.Done).
	Uncommitted []string
}

// Changes reads the branch of the table in the mirror, where the commits of
// the Towers arrive (Tower.Done) and whose refs the worktree of the Citadel
// shares, and the worktree on this machine for what is not committed. since
// "" gives only the head.
func (s *Service) Changes(ctx context.Context, workspace, table, machine, since string) (Changes, error) {
	t, err := s.store.Get(ctx, workspace, table)
	if err != nil {
		return Changes{}, err
	}
	mirror, err := s.o.Workspaces.OpenMirror(ctx, workspace)
	if err != nil {
		return Changes{}, err
	}
	head, err := mirror.ResolveRef(ctx, "refs/heads/"+t.Branch)
	if err != nil {
		return Changes{}, err
	}
	out := Changes{Head: string(head)}
	if since != "" && since != out.Head {
		if out.Files, err = mirror.DiffNameStatus(ctx, since, out.Head); err != nil {
			return Changes{}, err
		}
	}
	if machine != s.o.Machine {
		return out, nil
	}
	w, err := s.store.Worktree(ctx, workspace, table, machine)
	if errors.Is(err, ErrNoWorktree) {
		return out, nil
	}
	if err != nil {
		return Changes{}, err
	}
	repo, err := s.worktreeRepo(ctx, w)
	if err != nil {
		return Changes{}, err
	}
	st, err := repo.Status(ctx)
	if err != nil {
		return Changes{}, err
	}
	for _, e := range st {
		out.Uncommitted = append(out.Uncommitted, e.Path)
	}
	return out, nil
}
