package classes

import (
	"context"
	"path/filepath"

	"github.com/lucasndas/mestre/internal/gitx"
)

// GitxState is the GitState of the engine, over internal/gitx.
type GitxState struct{ Git gitx.Git }

// Branch implements GitState: the branch of the worktree in git worktree
// list.
func (g GitxState) Branch(ctx context.Context, worktree string) (string, error) {
	r, err := g.Git.Open(ctx, worktree)
	if err != nil {
		return "", err
	}
	wts, err := r.ListWorktrees(ctx)
	if err != nil {
		return "", err
	}
	want := pathKey(resolved(worktree))
	for _, w := range wts {
		if pathKey(resolved(w.Path)) == want {
			if w.Detached || w.Branch == "" {
				return "", errDetached
			}
			return w.Branch, nil
		}
	}
	return "", errNoWorktree
}

// Committed implements GitState from git status: the staged entries and,
// with all, every changed tracked file.
func (g GitxState) Committed(ctx context.Context, worktree string, all bool) ([]string, error) {
	r, err := g.Git.Open(ctx, worktree)
	if err != nil {
		return nil, err
	}
	st, err := r.Status(ctx)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range st {
		if e.Untracked() {
			continue
		}
		if e.Index != ' ' || (all && e.Worktree != ' ') {
			out = append(out, filepath.ToSlash(e.Path))
			if e.OrigPath != "" {
				out = append(out, filepath.ToSlash(e.OrigPath))
			}
		}
	}
	return out, nil
}

type gitStateError string

func (e gitStateError) Error() string { return string(e) }

const (
	errDetached   gitStateError = "HEAD is detached"
	errNoWorktree gitStateError = "the worktree is not in git worktree list"
)

func resolved(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}
