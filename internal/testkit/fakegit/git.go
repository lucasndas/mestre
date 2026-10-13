package fakegit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/lucasndas/mestre/internal/gitx"
	"github.com/lucasndas/mestre/pkg/clock"
)

// Git is the in-memory gitx.Git: a registry of repositories by directory.
// It touches the disk only to write and read bundle files (so the bytes can
// travel like the real ones) and never starts a process. Worktree
// directories are not created; the files of a worktree are the in-memory
// changes set with Handle.SetFile.
type Git struct {
	mu    sync.Mutex // guards the registry and every repository handled here
	clk   clock.Clock
	byDir map[string]*Handle
}

var _ gitx.Git = (*Git)(nil)

// NewGit returns an empty registry. clk dates the commits whose signature has
// no date (clock.Real if nil).
func NewGit(clk clock.Clock) *Git {
	if clk == nil {
		clk = clock.Real{}
	}
	return &Git{clk: clk, byDir: map[string]*Handle{}}
}

// Handle is one repository or worktree of Git; it implements gitx.Repo.
type Handle struct {
	g      *Git
	r      *Repo
	dir    string
	branch string // checked-out branch key; "" for a bare repository

	work   map[string]*string // changes against HEAD; nil = deleted
	staged map[string]bool
	skip   map[string]bool
}

var _ gitx.Repo = (*Handle)(nil)

func absDir(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return filepath.Clean(dir)
	}
	return abs
}

func (g *Git) newHandle(r *Repo, dir, branch string) *Handle {
	h := &Handle{g: g, r: r, dir: dir, branch: branch, work: map[string]*string{}, staged: map[string]bool{}, skip: map[string]bool{}}
	g.byDir[dir] = h
	return h
}

// Version implements gitx.Git.
func (g *Git) Version(context.Context) (gitx.Version, error) {
	return gitx.Version{Major: 2, Minor: 50, Raw: "git version 2.50.0 (fake)"}, nil
}

// Init implements gitx.Git.
func (g *Git) Init(_ context.Context, dir string, opts gitx.InitOptions) (gitx.Repo, error) {
	branch := opts.InitialBranch
	if branch == "" {
		branch = "main"
	}
	if err := gitx.ValidateRef(branch); err != nil {
		return nil, err
	}
	abs := absDir(dir)
	g.mu.Lock()
	defer g.mu.Unlock()
	if h, ok := g.byDir[abs]; ok {
		return h, nil
	}
	r := New()
	r.mainDir, r.bare, r.head = abs, opts.Bare, branch
	if opts.Bare {
		return g.newHandle(r, abs, ""), nil
	}
	r.worktrees[abs] = branch
	return g.newHandle(r, abs, branch), nil
}

// Clone implements gitx.Git. url must be the directory of a repository of
// this Git.
func (g *Git) Clone(_ context.Context, url, dir string, opts gitx.CloneOptions) (gitx.Repo, error) {
	abs := absDir(dir)
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.byDir[abs]; ok {
		return nil, fmt.Errorf("clone into %s: %w", dir, gitx.ErrPathExists)
	}
	src, ok := g.byDir[absDir(url)]
	if !ok {
		return nil, fmt.Errorf("clone %s: %w", gitx.RedactURL(url), gitx.ErrNotARepo)
	}
	commits, refs, head := src.r.snapshot()
	r := New()
	r.mainDir, r.bare, r.head = abs, opts.Bare, head
	r.commits = commits
	r.config["remote.origin.url"] = url
	for _, ref := range refs {
		switch {
		case strings.HasPrefix(ref.Name, "refs/heads/"):
			if opts.Bare || refKey(ref.Name) == head {
				r.branches[refKey(ref.Name)] = ref.Hash
			}
			if !opts.Bare {
				r.branches["refs/remotes/origin/"+strings.TrimPrefix(ref.Name, "refs/heads/")] = ref.Hash
			}
		case strings.HasPrefix(ref.Name, "refs/tags/"):
			r.branches[ref.Name] = ref.Hash
		}
	}
	if opts.Bare {
		return g.newHandle(r, abs, ""), nil
	}
	r.worktrees[abs] = head
	return g.newHandle(r, abs, head), nil
}

// Open implements gitx.Git.
func (g *Git) Open(_ context.Context, dir string) (gitx.Repo, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if h, ok := g.byDir[absDir(dir)]; ok {
		return h, nil
	}
	return nil, fmt.Errorf("open %s: %w", dir, gitx.ErrNotARepo)
}

// Handle returns the repository at dir for the test to seed or inspect (nil
// if there is none).
func (g *Git) Handle(dir string) *Handle {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.byDir[absDir(dir)]
}

// snapshot copies the commits and refs under the lock of the repository.
func (r *Repo) snapshot() (map[Hash]Commit, []gitx.Ref, string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return maps.Clone(r.commits), r.listRefsLocked(), r.head
}

func (r *Repo) listRefsLocked() []gitx.Ref {
	refs := make([]gitx.Ref, 0, len(r.branches))
	for k, h := range r.branches {
		refs = append(refs, gitx.Ref{Name: fullName(k), Hash: h})
	}
	slices.SortFunc(refs, func(a, b gitx.Ref) int { return strings.Compare(a.Name, b.Name) })
	return refs
}

func (r *Repo) ancestorsLocked(h Hash) map[Hash]bool {
	set := map[Hash]bool{}
	for ; h != "" && !set[h]; h = r.commits[h].Parent {
		set[h] = true
	}
	return set
}

// Data returns the data model, to seed commits and branches directly.
func (h *Handle) Data() *Repo { return h.r }

// lock takes the registry lock and the repository lock.
func (h *Handle) lock() (unlock func()) {
	h.g.mu.Lock()
	h.r.mu.Lock()
	return func() {
		h.r.mu.Unlock()
		h.g.mu.Unlock()
	}
}

func (h *Handle) needTree() error {
	if h.branch == "" {
		return fmt.Errorf("%w: %s", gitx.ErrBareRepo, h.dir)
	}
	return nil
}

// resolveLocked resolves a ref, HEAD included.
func (h *Handle) resolveLocked(ref string) (Hash, error) {
	if ref == "HEAD" {
		key := h.branch
		if key == "" {
			key = h.r.head
		}
		if c, ok := h.r.branches[key]; ok {
			return c, nil
		}
		return "", fmt.Errorf("%w: HEAD", gitx.ErrRefNotFound)
	}
	return h.r.resolveLocked(ref)
}

func (h *Handle) treeLocked() map[string]string {
	if c, err := h.resolveLocked("HEAD"); err == nil {
		return h.r.commits[c].Files
	}
	return nil
}

// Dir implements gitx.Repo.
func (h *Handle) Dir() string { return h.dir }

// GetConfig implements gitx.Repo.
func (h *Handle) GetConfig(_ context.Context, key string) (string, error) {
	defer h.lock()()
	if v, ok := h.r.config[key]; ok {
		return v, nil
	}
	return "", fmt.Errorf("get config %s: %w", key, gitx.ErrConfigNotSet)
}

// SetConfig implements gitx.Repo.
func (h *Handle) SetConfig(_ context.Context, key, value string) error {
	if err := gitx.ValidateRef(key); err != nil {
		return err
	}
	defer h.lock()()
	h.r.config[key] = value
	return nil
}

// ResolveRef implements gitx.Repo.
func (h *Handle) ResolveRef(_ context.Context, ref string) (gitx.Hash, error) {
	if err := gitx.ValidateRef(ref); err != nil {
		return "", err
	}
	defer h.lock()()
	return h.resolveLocked(ref)
}

// ListRefs implements gitx.Repo.
func (h *Handle) ListRefs(_ context.Context, prefixes ...string) ([]gitx.Ref, error) {
	defer h.lock()()
	var out []gitx.Ref
	for _, ref := range h.r.listRefsLocked() {
		if len(prefixes) == 0 {
			out = append(out, ref)
			continue
		}
		for _, p := range prefixes {
			p = strings.TrimSuffix(p, "/")
			if ref.Name == p || strings.HasPrefix(ref.Name, p+"/") {
				out = append(out, ref)
				break
			}
		}
	}
	return out, nil
}

// CreateBranch implements gitx.Repo.
func (h *Handle) CreateBranch(_ context.Context, name, startPoint string) error {
	if err := gitx.ValidateRef(name); err != nil {
		return err
	}
	if startPoint == "" {
		startPoint = "HEAD"
	}
	defer h.lock()()
	key := refKey(name)
	if _, ok := h.r.branches[key]; ok {
		return fmt.Errorf("create branch %s: %w", name, gitx.ErrBranchExists)
	}
	c, err := h.resolveLocked(startPoint)
	if err != nil {
		return fmt.Errorf("create branch %s: %w", name, err)
	}
	h.r.branches[key] = c
	return nil
}

// DeleteBranch implements gitx.Repo.
func (h *Handle) DeleteBranch(_ context.Context, name string) error {
	if err := gitx.ValidateRef(name); err != nil {
		return err
	}
	defer h.lock()()
	key := refKey(name)
	if _, ok := h.r.branches[key]; !ok || strings.HasPrefix(key, "refs/") {
		return fmt.Errorf("delete branch %s: %w", name, gitx.ErrRefNotFound)
	}
	for p, b := range h.r.worktrees {
		if b == key {
			return fmt.Errorf("delete branch %s: %w: at %s", name, gitx.ErrBranchInUse, p)
		}
	}
	delete(h.r.branches, key)
	return nil
}

// UpdateRef implements gitx.Repo.
func (h *Handle) UpdateRef(_ context.Context, ref string, to gitx.Hash) error {
	if err := gitx.ValidateRef(ref); err != nil {
		return err
	}
	defer h.lock()()
	if _, ok := h.r.commits[to]; !ok {
		return fmt.Errorf("update ref %s: %w: %s", ref, gitx.ErrRefNotFound, to)
	}
	h.r.branches[refKey(ref)] = to
	return nil
}

// DeleteRef implements gitx.Repo.
func (h *Handle) DeleteRef(_ context.Context, ref string) error {
	if err := gitx.ValidateRef(ref); err != nil {
		return err
	}
	defer h.lock()()
	delete(h.r.branches, refKey(ref))
	return nil
}

// IsAncestor implements gitx.Repo.
func (h *Handle) IsAncestor(_ context.Context, ancestor, descendant string) (bool, error) {
	defer h.lock()()
	a, err := h.resolveLocked(ancestor)
	if err != nil {
		return false, err
	}
	d, err := h.resolveLocked(descendant)
	if err != nil {
		return false, err
	}
	return h.r.isAncestorLocked(a, d), nil
}

func info(c Commit) gitx.CommitInfo {
	ci := gitx.CommitInfo{Hash: c.Hash, Author: c.Author, Committer: c.Committer, Message: c.Message}
	if c.Parent != "" {
		ci.Parents = []gitx.Hash{c.Parent}
	}
	return ci
}

// Log implements gitx.Repo.
func (h *Handle) Log(_ context.Context, revRange string) ([]gitx.CommitInfo, error) {
	base, tip, err := gitx.ParseRevs(revRange)
	if err != nil {
		return nil, err
	}
	defer h.lock()()
	t, err := h.resolveLocked(tip)
	if err != nil {
		return nil, err
	}
	var excluded map[Hash]bool
	if base != "" {
		b, err := h.resolveLocked(base)
		if err != nil {
			return nil, err
		}
		excluded = h.r.ancestorsLocked(b)
	}
	var out []gitx.CommitInfo
	for c := t; c != "" && !excluded[c]; c = h.r.commits[c].Parent {
		out = append(out, info(h.r.commits[c]))
	}
	return out, nil
}

// ReadFile implements gitx.Repo.
func (h *Handle) ReadFile(_ context.Context, ref, p string) ([]byte, error) {
	defer h.lock()()
	c, err := h.resolveLocked(ref)
	if err != nil {
		return nil, err
	}
	content, ok := h.r.commits[c].Files[p]
	if !ok {
		return nil, fmt.Errorf("read %s:%s: %w", ref, p, gitx.ErrFileNotFound)
	}
	return []byte(content), nil
}

// applyRefspecsLocked updates refs from the refs of a source, like git fetch
// does with explicit refspecs; the commits must be imported already.
func (r *Repo) applyRefspecsLocked(src []gitx.Ref, specs []string) error {
	update := func(dst string, to Hash, force bool) error {
		key := refKey(dst)
		if !strings.HasPrefix(key, "refs/") {
			key = refKey("refs/heads/" + key)
		}
		if cur, ok := r.branches[key]; ok && cur != to && !force && !r.isAncestorLocked(cur, to) {
			return fmt.Errorf("%w: %s", gitx.ErrNotFastForward, dst)
		}
		r.branches[key] = to
		return nil
	}
	for _, spec := range specs {
		force := strings.HasPrefix(spec, "+")
		s, d, ok := strings.Cut(strings.TrimPrefix(spec, "+"), ":")
		if !ok || d == "" {
			continue // fetched without updating a ref
		}
		if strings.Contains(s, "*") {
			pre, suf, _ := strings.Cut(s, "*")
			dpre, dsuf, _ := strings.Cut(d, "*")
			for _, ref := range src {
				if len(ref.Name) >= len(pre)+len(suf) && strings.HasPrefix(ref.Name, pre) && strings.HasSuffix(ref.Name, suf) {
					mid := ref.Name[len(pre) : len(ref.Name)-len(suf)]
					if err := update(dpre+mid+dsuf, ref.Hash, force); err != nil {
						return err
					}
				}
			}
			continue
		}
		var found *gitx.Ref
		for i, ref := range src {
			if ref.Name == s || ref.Name == "refs/heads/"+s || ref.Name == "refs/tags/"+s {
				found = &src[i]
				break
			}
		}
		if found == nil {
			return fmt.Errorf("%w: couldn't find remote ref %s", gitx.ErrRefNotFound, s)
		}
		if err := update(d, found.Hash, force); err != nil {
			return err
		}
	}
	return nil
}

// Fetch implements gitx.Repo. remote is a name with remote.<name>.url set,
// or the directory of another repository of this Git.
func (h *Handle) Fetch(_ context.Context, remote string, refspecs ...string) error {
	if err := gitx.ValidateRef(remote); err != nil {
		return err
	}
	h.g.mu.Lock()
	url := remote
	if u, ok := h.r.configGet("remote." + remote + ".url"); ok {
		url = u
	}
	src, ok := h.g.byDir[absDir(url)]
	h.g.mu.Unlock()
	if !ok {
		return fmt.Errorf("fetch %s: %w", gitx.RedactURL(url), gitx.ErrNotARepo)
	}
	commits, refs, _ := src.r.snapshot()
	defer h.lock()()
	maps.Copy(h.r.commits, commits)
	if len(refspecs) == 0 {
		if f, ok := h.r.config["remote."+remote+".fetch"]; ok {
			refspecs = []string{f}
		}
	}
	return h.r.applyRefspecsLocked(refs, refspecs)
}

func (r *Repo) configGet(key string) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.config[key]
	return v, ok
}

// bundleBody is what follows the text header of a fake bundle file.
type bundleBody struct {
	Commits []Commit
}

// CreateBundle implements gitx.Repo. The file has the text header of real
// bundles (gitx.ReadBundle reads it) followed by the commits as JSON.
func (h *Handle) CreateBundle(_ context.Context, file string, revs ...string) error {
	if len(revs) == 0 {
		return fmt.Errorf("%w: bundle without revisions", gitx.ErrInvalidArgument)
	}
	abs, err := filepath.Abs(file)
	if err != nil {
		return err
	}
	unlock := h.lock()
	var refs []gitx.Ref
	var prereqs []Hash
	include := map[Hash]bool{}
	for _, rev := range revs {
		base, tip, err := gitx.ParseRevs(rev)
		if err != nil {
			unlock()
			return err
		}
		tipHash, ok := h.r.branches[refKey(tip)]
		if !ok {
			unlock()
			return fmt.Errorf("create bundle %s: %w: %s", file, gitx.ErrRefNotFound, tip)
		}
		var excluded map[Hash]bool
		if base != "" {
			b, err := h.resolveLocked(base)
			if err != nil {
				unlock()
				return fmt.Errorf("create bundle %s: %w", file, err)
			}
			excluded = h.r.ancestorsLocked(b)
			if !slices.Contains(prereqs, b) {
				prereqs = append(prereqs, b)
			}
		}
		refs = append(refs, gitx.Ref{Name: fullName(refKey(tip)), Hash: tipHash})
		for c := tipHash; c != "" && !excluded[c]; c = h.r.commits[c].Parent {
			include[c] = true
		}
	}
	var body bundleBody
	for _, c := range slices.Sorted(maps.Keys(include)) {
		body.Commits = append(body.Commits, h.r.commits[c])
	}
	unlock()
	if len(body.Commits) == 0 {
		return fmt.Errorf("create bundle %s: %w", file, gitx.ErrEmptyBundle)
	}
	var buf bytes.Buffer
	buf.WriteString("# v2 git bundle\n")
	for _, p := range prereqs {
		fmt.Fprintf(&buf, "-%s fake\n", p)
	}
	for _, ref := range refs {
		fmt.Fprintf(&buf, "%s %s\n", ref.Hash, ref.Name)
	}
	buf.WriteString("\n")
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		return err
	}
	if err := os.WriteFile(abs, buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("create bundle %s: %w", file, err)
	}
	return nil
}

// readBundle parses a bundle file written by CreateBundle.
func readBundle(file string) (gitx.BundleInfo, bundleBody, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return gitx.BundleInfo{}, bundleBody{}, fmt.Errorf("read bundle: %w", err)
	}
	bi, err := gitx.ParseBundleHeader(bytes.NewReader(data))
	if err != nil {
		return gitx.BundleInfo{}, bundleBody{}, err
	}
	_, rest, ok := bytes.Cut(data, []byte("\n\n"))
	var body bundleBody
	if !ok || json.Unmarshal(rest, &body) != nil {
		return gitx.BundleInfo{}, bundleBody{}, fmt.Errorf("%w: unreadable body", gitx.ErrInvalidBundle)
	}
	return bi, body, nil
}

// VerifyBundle implements gitx.Repo.
func (h *Handle) VerifyBundle(_ context.Context, file string) error {
	_, _, err := h.verify(file)
	return err
}

// verify requires the h lock to be free; it takes it.
func (h *Handle) verify(file string) (gitx.BundleInfo, bundleBody, error) {
	abs, err := filepath.Abs(file)
	if err != nil {
		return gitx.BundleInfo{}, bundleBody{}, err
	}
	bi, body, err := readBundle(abs)
	if err != nil {
		return gitx.BundleInfo{}, bundleBody{}, fmt.Errorf("verify bundle %s: %w", file, err)
	}
	defer h.lock()()
	have := map[Hash]bool{}
	for _, c := range body.Commits {
		have[c.Hash] = true
	}
	for _, p := range bi.Prerequisites {
		if _, ok := h.r.commits[p]; !ok && !have[p] {
			return gitx.BundleInfo{}, bundleBody{}, fmt.Errorf("verify bundle %s: %w: %s", file, gitx.ErrMissingPrerequisite, p)
		}
	}
	return bi, body, nil
}

// FetchBundle implements gitx.Repo.
func (h *Handle) FetchBundle(_ context.Context, file string, refspecs ...string) error {
	if len(refspecs) == 0 {
		return fmt.Errorf("%w: fetching a bundle needs refspecs", gitx.ErrInvalidArgument)
	}
	bi, body, err := h.verify(file)
	if err != nil {
		return err
	}
	defer h.lock()()
	for _, c := range body.Commits {
		h.r.commits[c.Hash] = c
	}
	return h.r.applyRefspecsLocked(bi.Refs, refspecs)
}

// AddWorktree implements gitx.Repo.
func (h *Handle) AddWorktree(_ context.Context, p string, opts gitx.WorktreeOptions) (gitx.Repo, error) {
	if err := gitx.ValidateRef(opts.Branch); err != nil {
		return nil, err
	}
	abs := absDir(p)
	defer h.lock()()
	if _, ok := h.g.byDir[abs]; ok {
		return nil, fmt.Errorf("add worktree %s: %w", p, gitx.ErrPathExists)
	}
	key := refKey(opts.Branch)
	if opts.Create {
		if _, ok := h.r.branches[key]; ok {
			return nil, fmt.Errorf("add worktree %s: %w: %s", p, gitx.ErrBranchExists, opts.Branch)
		}
		start := opts.StartPoint
		if start == "" {
			start = "HEAD"
		}
		c, err := h.resolveLocked(start)
		if err != nil {
			return nil, fmt.Errorf("add worktree %s: %w", p, err)
		}
		h.r.branches[key] = c
	} else if _, ok := h.r.branches[key]; !ok {
		return nil, fmt.Errorf("add worktree %s: %w: %s", p, gitx.ErrRefNotFound, opts.Branch)
	}
	for other, b := range h.r.worktrees {
		if b == key {
			return nil, fmt.Errorf("add worktree %s: %w: %s at %s", p, gitx.ErrBranchInUse, opts.Branch, other)
		}
	}
	h.r.worktrees[abs] = key
	return h.g.newHandle(h.r, abs, key), nil
}

// RemoveWorktree implements gitx.Repo.
func (h *Handle) RemoveWorktree(_ context.Context, p string, force bool) error {
	abs := absDir(p)
	defer h.lock()()
	w, ok := h.g.byDir[abs]
	if !ok || w.r != h.r || abs == h.r.mainDir {
		return fmt.Errorf("remove worktree %s: %w", p, gitx.ErrNoSuchWorktree)
	}
	if !force && len(w.statusLocked()) > 0 {
		return fmt.Errorf("remove worktree %s: %w", p, gitx.ErrDirtyWorktree)
	}
	delete(h.g.byDir, abs)
	delete(h.r.worktrees, abs)
	return nil
}

// ListWorktrees implements gitx.Repo.
func (h *Handle) ListWorktrees(context.Context) ([]gitx.Worktree, error) {
	defer h.lock()()
	entry := func(p, key string) gitx.Worktree {
		w := gitx.Worktree{Path: p, Branch: key}
		w.Head = h.r.branches[key]
		return w
	}
	main := gitx.Worktree{Path: h.r.mainDir, Bare: h.r.bare}
	if !h.r.bare {
		main = entry(h.r.mainDir, h.r.worktrees[h.r.mainDir])
	}
	list := []gitx.Worktree{main}
	for _, p := range slices.Sorted(maps.Keys(h.r.worktrees)) {
		if p != h.r.mainDir {
			list = append(list, entry(p, h.r.worktrees[p]))
		}
	}
	return list, nil
}

// SetFile sets the content of a file in the working tree of h, as the test
// would write it to disk; setting the committed content undoes the change.
func (h *Handle) SetFile(p, content string) {
	defer h.lock()()
	p = path.Clean(filepath.ToSlash(p))
	if old, ok := h.treeLocked()[p]; ok && old == content {
		delete(h.work, p)
		delete(h.staged, p)
		return
	}
	h.work[p] = &content
}

// RemoveFile deletes a file from the working tree of h.
func (h *Handle) RemoveFile(p string) {
	defer h.lock()()
	p = path.Clean(filepath.ToSlash(p))
	if _, ok := h.treeLocked()[p]; ok {
		h.work[p] = nil
		return
	}
	delete(h.work, p)
	delete(h.staged, p)
}

// excludedLocked reports whether p matches an info/exclude pattern (the
// subset of gitignore the Mestre writes: "/dir/", "dir/", "name", "*.ext").
func (h *Handle) excludedLocked(p string) bool {
	patterns := slices.Clone(h.r.exclude)
	for _, b := range h.r.blocks {
		patterns = append(patterns, b...)
	}
	segs := strings.Split(p, "/")
	for _, pat := range patterns {
		pat = strings.TrimSpace(pat)
		if pat == "" || strings.HasPrefix(pat, "#") || strings.HasPrefix(pat, "!") {
			continue
		}
		dirOnly := strings.HasSuffix(pat, "/")
		pat = strings.TrimSuffix(pat, "/")
		anchored := strings.HasPrefix(pat, "/") || strings.Contains(pat, "/")
		psegs := strings.Split(strings.TrimPrefix(pat, "/"), "/")
		starts := []int{0}
		if !anchored {
			starts = starts[:0]
			for i := range segs {
				starts = append(starts, i)
			}
		}
		for _, s := range starts {
			if s+len(psegs) > len(segs) || (dirOnly && s+len(psegs) == len(segs)) {
				continue
			}
			ok := true
			for i, ps := range psegs {
				if m, _ := path.Match(ps, segs[s+i]); !m {
					ok = false
					break
				}
			}
			if ok {
				return true
			}
		}
	}
	return false
}

func (h *Handle) statusLocked() []gitx.StatusEntry {
	tree := h.treeLocked()
	var tracked, untracked []gitx.StatusEntry
	for _, p := range slices.Sorted(maps.Keys(h.work)) {
		if h.skip[p] {
			continue
		}
		_, inTree := tree[p]
		if !inTree && !h.staged[p] && h.excludedLocked(p) {
			continue
		}
		e := gitx.StatusEntry{Path: p, Index: ' ', Worktree: ' '}
		code := byte('M')
		switch {
		case h.work[p] == nil:
			code = 'D'
		case !inTree:
			code = 'A'
		}
		switch {
		case h.staged[p]:
			e.Index = code
		case !inTree:
			e.Index, e.Worktree = '?', '?'
		default:
			e.Worktree = code
		}
		if e.Untracked() {
			untracked = append(untracked, e)
		} else {
			tracked = append(tracked, e)
		}
	}
	return append(tracked, untracked...)
}

// Status implements gitx.Repo.
func (h *Handle) Status(context.Context) ([]gitx.StatusEntry, error) {
	if err := h.needTree(); err != nil {
		return nil, err
	}
	defer h.lock()()
	return h.statusLocked(), nil
}

// Stage implements gitx.Repo.
func (h *Handle) Stage(_ context.Context, paths ...string) error {
	if err := h.needTree(); err != nil {
		return err
	}
	defer h.lock()()
	if len(paths) == 0 {
		for p := range h.work {
			h.staged[p] = true
		}
		return nil
	}
	tree := h.treeLocked()
	for _, spec := range paths {
		spec = path.Clean(filepath.ToSlash(spec))
		matched := false
		for _, p := range slices.Sorted(maps.Keys(h.work)) {
			if p == spec || spec == "." || strings.HasPrefix(p, spec+"/") {
				h.staged[p] = true
				matched = true
			}
		}
		if _, ok := tree[spec]; ok {
			matched = true
		}
		if !matched {
			return fmt.Errorf("stage: pathspec %q did not match any files", spec)
		}
	}
	return nil
}

// cleanMessage applies git's "whitespace" cleanup: no trailing spaces, no
// leading or trailing blank lines, one blank line at most between paragraphs.
func cleanMessage(msg string) string {
	var out []string
	blank := false
	for line := range strings.SplitSeq(strings.ReplaceAll(msg, "\r\n", "\n"), "\n") {
		line = strings.TrimRight(line, " \t")
		if line == "" {
			blank = len(out) > 0
			continue
		}
		if blank {
			out = append(out, "")
			blank = false
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

func (h *Handle) stamp(s gitx.Signature) gitx.Signature {
	when := s.When
	if when.IsZero() {
		when = h.g.clk.Now()
	}
	s.When = time.Unix(when.Unix(), 0).UTC()
	return s
}

// Commit implements gitx.Repo.
func (h *Handle) Commit(_ context.Context, opts gitx.CommitOptions) (gitx.Hash, error) {
	if err := h.needTree(); err != nil {
		return "", err
	}
	author, committer := opts.Author, opts.Committer
	if committer == (gitx.Signature{}) {
		committer = author
	}
	if strings.TrimSpace(opts.Message) == "" || author.Name == "" || author.Email == "" || committer.Name == "" || committer.Email == "" {
		return "", fmt.Errorf("%w: commit needs a message and an author", gitx.ErrInvalidArgument)
	}
	defer h.lock()()
	if len(h.staged) == 0 && !opts.AllowEmpty {
		return "", fmt.Errorf("commit: %w", gitx.ErrNothingToCommit)
	}
	set := map[string]string{}
	var del []string
	for p := range h.staged {
		if h.work[p] == nil {
			del = append(del, p)
		} else {
			set[p] = *h.work[p]
		}
	}
	c := h.r.commitLocked(h.branch, cleanMessage(opts.Message), set, del, h.stamp(author), h.stamp(committer))
	for p := range h.staged {
		delete(h.work, p)
	}
	clear(h.staged)
	return c, nil
}

// MergeFFOnly implements gitx.Repo.
func (h *Handle) MergeFFOnly(_ context.Context, ref string) (gitx.Hash, error) {
	if err := h.needTree(); err != nil {
		return "", err
	}
	if err := gitx.ValidateRef(ref); err != nil {
		return "", err
	}
	defer h.lock()()
	target, err := h.resolveLocked(ref)
	if err != nil {
		return "", fmt.Errorf("merge %s: %w", ref, err)
	}
	cur := h.r.branches[h.branch]
	if cur == target || (cur != "" && h.r.isAncestorLocked(target, cur)) {
		return cur, nil
	}
	if cur != "" && !h.r.isAncestorLocked(cur, target) {
		return "", fmt.Errorf("merge %s: %w", ref, gitx.ErrNotFastForward)
	}
	oldTree, newTree := h.r.commits[cur].Files, h.r.commits[target].Files
	for p := range h.work {
		if h.skip[p] {
			continue
		}
		ov, oin := oldTree[p]
		nv, nin := newTree[p]
		if oin != nin || ov != nv {
			return "", fmt.Errorf("merge %s: %w: %s", ref, gitx.ErrLocalChanges, p)
		}
	}
	h.r.branches[h.branch] = target
	return target, nil
}

// SetSkipWorktree implements gitx.Repo.
func (h *Handle) SetSkipWorktree(_ context.Context, on bool, paths ...string) error {
	if err := h.needTree(); err != nil {
		return err
	}
	if len(paths) == 0 {
		return fmt.Errorf("%w: skip-worktree without paths", gitx.ErrInvalidArgument)
	}
	defer h.lock()()
	tree := h.treeLocked()
	for _, p := range paths {
		p = path.Clean(filepath.ToSlash(p))
		if _, ok := tree[p]; !ok && !h.staged[p] {
			return fmt.Errorf("update-index: unable to mark %s: not tracked", p)
		}
		if on {
			h.skip[p] = true
		} else {
			delete(h.skip, p)
		}
	}
	return nil
}

// SkipWorktreePaths implements gitx.Repo.
func (h *Handle) SkipWorktreePaths(context.Context) ([]string, error) {
	if err := h.needTree(); err != nil {
		return nil, err
	}
	defer h.lock()()
	return slices.Sorted(maps.Keys(h.skip)), nil
}

// SetExcludeBlock implements gitx.Repo.
func (h *Handle) SetExcludeBlock(_ context.Context, name string, patterns []string) error {
	if err := gitx.ValidateExcludeBlock(name, patterns); err != nil {
		return err
	}
	defer h.lock()()
	if len(patterns) == 0 {
		delete(h.r.blocks, name)
		return nil
	}
	h.r.blocks[name] = slices.Clone(patterns)
	return nil
}

// ExcludeBlock implements gitx.Repo.
func (h *Handle) ExcludeBlock(_ context.Context, name string) ([]string, error) {
	if err := gitx.ValidateExcludeBlock(name, nil); err != nil {
		return nil, err
	}
	defer h.lock()()
	return slices.Clone(h.r.blocks[name]), nil
}
