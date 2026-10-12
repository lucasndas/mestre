// Package fakegit is the in-memory git of the fast suite
// (docs/qualidade/dubles.md#outros-dublês): refs, commits, files,
// worktrees, bundles and info/exclude kept as plain data. The internal/gitx
// interface arrives in M1 and this fake implements it there; criteria that
// need the real git behavior stay in the full suite.
package fakegit

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sort"
	"sync"
)

// Errors returned by the fake.
var (
	ErrUnknownRef     = errors.New("fakegit: unknown ref")
	ErrBranchInUse    = errors.New("fakegit: branch already checked out in a worktree")
	ErrNotFastForward = errors.New("fakegit: not a fast-forward")
	ErrNoSuchFile     = errors.New("fakegit: no such file")
)

// Hash identifies a commit.
type Hash string

// Commit is one snapshot.
type Commit struct {
	Hash    Hash
	Parent  Hash // "" for the root commit
	Message string
	Files   map[string]string // full tree: path -> content
}

// Bundle carries commits and refs between repositories, like git bundle.
type Bundle struct {
	Refs    map[string]Hash
	Commits []Commit
}

// Repo is one repository.
type Repo struct {
	mu        sync.Mutex
	commits   map[Hash]Commit
	branches  map[string]Hash
	worktrees map[string]string // path -> branch
	exclude   []string
}

// New returns an empty repository.
func New() *Repo {
	return &Repo{commits: map[Hash]Commit{}, branches: map[string]Hash{}, worktrees: map[string]string{}}
}

// Commit records files on top of branch (created if missing). An empty
// content deletes the path.
func (r *Repo) Commit(branch, msg string, files map[string]string) Hash {
	r.mu.Lock()
	defer r.mu.Unlock()
	parent := r.branches[branch]
	tree := map[string]string{}
	if parent != "" {
		maps.Copy(tree, r.commits[parent].Files)
	}
	for p, c := range files {
		if c == "" {
			delete(tree, p)
		} else {
			tree[p] = c
		}
	}
	h := sha1.New()
	fmt.Fprintf(h, "parent %s\nmessage %s\n", parent, msg)
	for _, p := range slices.Sorted(maps.Keys(tree)) {
		fmt.Fprintf(h, "%s\x00%s\x00", p, tree[p])
	}
	c := Commit{Hash: Hash(hex.EncodeToString(h.Sum(nil))), Parent: parent, Message: msg, Files: tree}
	r.commits[c.Hash] = c
	r.branches[branch] = c.Hash
	return c.Hash
}

// Branch creates name at the commit of from (a branch or a hash).
func (r *Repo) Branch(name, from string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	h, err := r.resolveLocked(from)
	if err != nil {
		return err
	}
	r.branches[name] = h
	return nil
}

// Resolve returns the commit of a branch or hash.
func (r *Repo) Resolve(ref string) (Hash, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.resolveLocked(ref)
}

func (r *Repo) resolveLocked(ref string) (Hash, error) {
	if h, ok := r.branches[ref]; ok {
		return h, nil
	}
	if _, ok := r.commits[Hash(ref)]; ok {
		return Hash(ref), nil
	}
	return "", fmt.Errorf("%w: %s", ErrUnknownRef, ref)
}

// ReadFile returns path at ref.
func (r *Repo) ReadFile(ref, path string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	h, err := r.resolveLocked(ref)
	if err != nil {
		return "", err
	}
	c, ok := r.commits[h].Files[path]
	if !ok {
		return "", fmt.Errorf("%w: %s:%s", ErrNoSuchFile, ref, path)
	}
	return c, nil
}

// Branches returns the branch names, sorted.
func (r *Repo) Branches() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Sorted(maps.Keys(r.branches))
}

// AddWorktree checks branch out at path; a branch lives in one worktree.
func (r *Repo) AddWorktree(path, branch string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.branches[branch]; !ok {
		return fmt.Errorf("%w: %s", ErrUnknownRef, branch)
	}
	for p, b := range r.worktrees {
		if b == branch && p != path {
			return fmt.Errorf("%w: %s at %s", ErrBranchInUse, branch, p)
		}
	}
	r.worktrees[path] = branch
	return nil
}

// RemoveWorktree forgets the worktree at path.
func (r *Repo) RemoveWorktree(path string) {
	r.mu.Lock()
	delete(r.worktrees, path)
	r.mu.Unlock()
}

// Worktrees returns path -> branch.
func (r *Repo) Worktrees() map[string]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return maps.Clone(r.worktrees)
}

// AddExclude appends a pattern to info/exclude, once.
func (r *Repo) AddExclude(pattern string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !slices.Contains(r.exclude, pattern) {
		r.exclude = append(r.exclude, pattern)
	}
}

// Exclude returns the info/exclude patterns.
func (r *Repo) Exclude() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.exclude)
}

// Bundle packs branches with every commit reachable from them.
func (r *Repo) Bundle(branches ...string) (Bundle, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	b := Bundle{Refs: map[string]Hash{}}
	seen := map[Hash]bool{}
	for _, name := range branches {
		h, ok := r.branches[name]
		if !ok {
			return Bundle{}, fmt.Errorf("%w: %s", ErrUnknownRef, name)
		}
		b.Refs[name] = h
		for ; h != "" && !seen[h]; h = r.commits[h].Parent {
			seen[h] = true
			b.Commits = append(b.Commits, r.commits[h])
		}
	}
	sort.Slice(b.Commits, func(i, j int) bool { return b.Commits[i].Hash < b.Commits[j].Hash })
	return b, nil
}

// Fetch imports a bundle; each branch moves only by fast-forward.
func (r *Repo) Fetch(b Bundle) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, c := range b.Commits {
		r.commits[c.Hash] = c
	}
	for name, h := range b.Refs {
		if cur, ok := r.branches[name]; ok && !r.isAncestorLocked(cur, h) {
			return fmt.Errorf("%w: %s", ErrNotFastForward, name)
		}
		r.branches[name] = h
	}
	return nil
}

func (r *Repo) isAncestorLocked(anc, h Hash) bool {
	for ; h != ""; h = r.commits[h].Parent {
		if h == anc {
			return true
		}
	}
	return false
}
