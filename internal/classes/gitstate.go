package classes

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"slices"
	"strings"

	"github.com/lucasndas/mestre/internal/gitx"
)

// GitxState is the GitState of the engine, over internal/gitx.
type GitxState struct {
	Git gitx.Git
	// Main is the main branch as a ref of the worktree's repository
	// ("main", "refs/remotes/origin/main"): the base of the table branch is
	// its merge-base with it. Empty: no base, and every script of the
	// worktree counts as changed (TableScripts).
	Main string
}

// maxScriptBytes is the largest script Changed compares; a larger one
// counts as changed.
const maxScriptBytes = 1 << 20

// Changed implements TableScripts: the file, read from the worktree, is
// compared with the same path at the base of the table branch (the
// parents, outside the branch, of its commits since Main; HEAD when it has
// none), at each base when a merge brought Main in.
func (g GitxState) Changed(ctx context.Context, worktree, path string) (bool, error) {
	if g.Main == "" {
		return true, errNoMain
	}
	root := resolved(worktree)
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return true, err
	}
	rel, err := filepath.Rel(root, real)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return true, nil // not a file of the worktree
	}
	data, err := readScript(real)
	if err != nil {
		return true, err
	}
	r, err := g.Git.Open(ctx, worktree)
	if err != nil {
		return true, err
	}
	bases, err := g.bases(ctx, r)
	if err != nil {
		return true, err
	}
	if len(bases) == 0 {
		return true, nil // a branch with no commit in common with Main
	}
	for _, b := range bases {
		at, err := r.ReadFile(ctx, b, filepath.ToSlash(rel))
		if err != nil || !sameText(at, data) {
			return true, nil // created at the table, or changed
		}
	}
	return false, nil
}

// bases are the merge-bases of HEAD with Main: the parents of the
// branch's own commits that are not its own.
func (g GitxState) bases(ctx context.Context, r gitx.Repo) ([]string, error) {
	own, err := r.Log(ctx, g.Main+"..HEAD")
	if err != nil {
		return nil, err
	}
	if len(own) == 0 {
		return []string{"HEAD"}, nil
	}
	in := make(map[gitx.Hash]bool, len(own))
	for _, c := range own {
		in[c.Hash] = true
	}
	var out []string
	for _, c := range own {
		for _, p := range c.Parents {
			if !in[p] && !slices.Contains(out, string(p)) {
				out = append(out, string(p))
			}
		}
	}
	return out, nil
}

// readScript reads a regular file of at most maxScriptBytes, without
// following a link or waiting on a FIFO swapped in for it.
func readScript(p string) ([]byte, error) {
	f, err := openScript(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() || st.Size() > maxScriptBytes {
		return nil, errNotScript
	}
	data, err := io.ReadAll(io.LimitReader(f, maxScriptBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxScriptBytes {
		return nil, errNotScript
	}
	return data, nil
}

// sameText compares two contents up to the line endings, which a checkout
// on Windows may change.
func sameText(a, b []byte) bool {
	crlf, lf := []byte("\r\n"), []byte("\n")
	return bytes.Equal(bytes.ReplaceAll(a, crlf, lf), bytes.ReplaceAll(b, crlf, lf))
}

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
	errNoMain     gitStateError = "the main branch of the repository is not known"
	errNotScript  gitStateError = "not a regular file of at most 1 MiB"
)

func resolved(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}
