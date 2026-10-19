package gitx

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/lucasndas/mestre/internal/privdir"
	"github.com/lucasndas/mestre/pkg/clock"
)

// repo is a repository or worktree opened by Real.
type repo struct {
	g      *Real
	dir    string
	common string // absolute git common dir: the key of the lock
	bare   bool
	// rec is the record of a worktree opened by it (OpenWorktree): every
	// call checks the worktree against it and runs pinned to it. nil for a
	// repository opened by its folder.
	rec *WorktreeRecord
	// gitDir is the folder of a bare repository opened by its folder: every
	// call runs with GIT_DIR on it, so a .git entry created inside it is
	// never followed. Empty otherwise.
	gitDir string
}

var _ Repo = (*repo)(nil)

func (r *repo) Dir() string { return r.dir }

func (r *repo) git(ctx context.Context, args ...string) ([]byte, error) {
	return r.run(ctx, runOpts{}, args...)
}

// run runs git in the folder of the repository. A worktree opened by its
// record is checked against it first (verify) and runs pinned to it; a bare
// repository opened by its folder runs with GIT_DIR. Every call passes the
// guard first.
func (r *repo) run(ctx context.Context, ro runOpts, args ...string) ([]byte, error) {
	ro.dir = r.dir
	switch {
	case r.rec != nil:
		if err := r.rec.verify(); err != nil {
			return nil, err
		}
		ro.env = append(r.rec.pin(), ro.env...)
	case r.gitDir != "":
		ro.env = append([]string{"GIT_DIR=" + r.gitDir}, ro.env...)
	}
	safe, err := r.guard(ctx, ro)
	if err != nil {
		return nil, err
	}
	ro.config = append(safe, ro.config...)
	return r.g.run(ctx, ro, args...)
}

// guard lists, in one git config call, what of the configuration the
// repository sees could make git run a command that globalArgs cannot name
// in advance (docs/arquitetura/seguranca.md#git-do-motor), and returns the
// -c settings of the call:
//
//   - for a call that touches the working tree (ro.tree: add, commit,
//     status, checkout, merge), every filter driver is emptied, so a driver
//     set in a config file the engine does not control never runs as the
//     engine;
//   - a promisor remote (extensions.partialClone, remote.<name>.promisor)
//     makes git fetch a missing object on its own, running the upload-pack
//     of that remote. GIT_NO_LAZY_FETCH turns that off from git 2.44
//     (NoLazyFetchVersion); before it, every call lists, and a repository
//     with a promisor remote is refused (ErrUnsafeRepo), as it is on every
//     call that lists. The engine never makes a partial clone.
//
// A driver or remote added between this listing and the call is the
// residual risk; the configuration of the mirror is not writable by
// mestre-agente, and a worktree runs pinned to it.
func (r *repo) guard(ctx context.Context, ro runOpts) ([]string, error) {
	v, err := r.g.Version(ctx)
	if err != nil {
		return nil, err
	}
	if !ro.tree && !v.Less(NoLazyFetchVersion) {
		return nil, nil
	}
	re := `^(extensions\.partialclone|remote\..+\.promisor)$`
	if ro.tree {
		re = `^(filter\..+|extensions\.partialclone|remote\..+\.promisor)$`
	}
	out, err := r.g.run(ctx, runOpts{dir: ro.dir, env: ro.env}, "config", "--null", "--get-regexp", re)
	if err != nil {
		if exitedWithoutMessage(err, 1) {
			return nil, nil // none configured
		}
		return nil, fmt.Errorf("list the configuration of %s: %w", r.dir, err)
	}
	var config []string
	seen := map[string]bool{}
	for entry := range strings.SplitSeq(string(out), "\x00") {
		key, value, hasValue := strings.Cut(entry, "\n")
		key = strings.TrimSpace(key)
		lower := strings.ToLower(key)
		switch {
		case lower == "extensions.partialclone":
			return nil, fmt.Errorf("%w: %s has a promisor remote (extensions.partialClone)", ErrUnsafeRepo, r.dir)
		case strings.HasPrefix(lower, "remote.") && strings.HasSuffix(lower, ".promisor"):
			if !hasValue || configTrue(value) {
				return nil, fmt.Errorf("%w: %s has a promisor remote (%s)", ErrUnsafeRepo, r.dir, key)
			}
		case strings.HasPrefix(lower, "filter."):
			dot := strings.LastIndexByte(key, '.')
			if dot <= len("filter.") {
				continue
			}
			driver := key[len("filter."):dot]
			if seen[driver] {
				continue
			}
			seen[driver] = true
			config = append(config,
				"filter."+driver+".clean=", "filter."+driver+".smudge=",
				"filter."+driver+".process=", "filter."+driver+".required=false")
		}
	}
	return config, nil
}

// configTrue reads a git boolean: anything git would not call false counts
// as true, so a value git might accept is never let through.
func configTrue(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "false", "no", "off", "0", "":
		return false
	}
	return true
}

// treeGit runs a command that touches the working tree: its guard also
// empties the filter drivers.
func (r *repo) treeGit(ctx context.Context, ro runOpts, args ...string) ([]byte, error) {
	ro.tree = true
	return r.run(ctx, ro, args...)
}

// mutate runs fn holding the lock of the repository.
func (r *repo) mutate(ctx context.Context, fn func() error) error {
	unlock, err := r.g.lock(ctx, r.common)
	if err != nil {
		return err
	}
	defer unlock()
	return fn()
}

func (r *repo) needTree() error {
	if r.bare {
		return fmt.Errorf("%w: %s", ErrBareRepo, r.dir)
	}
	return nil
}

func (r *repo) GetConfig(ctx context.Context, key string) (string, error) {
	if err := ValidateRef(key); err != nil {
		return "", err
	}
	out, err := r.git(ctx, "config", "--get", "--", key)
	if err != nil {
		if exitedWithoutMessage(err, 1) {
			return "", as(ErrConfigNotSet, err)
		}
		return "", fmt.Errorf("get config %s: %w", key, err)
	}
	return strings.TrimSuffix(string(out), "\n"), nil
}

func (r *repo) SetConfig(ctx context.Context, key, value string) error {
	if err := ValidateRef(key); err != nil {
		return err
	}
	if err := CheckConfig(key, value); err != nil {
		return fmt.Errorf("set config %s: %w", key, err)
	}
	return r.mutate(ctx, func() error {
		if _, err := r.git(ctx, "config", "--", key, value); err != nil {
			return fmt.Errorf("set config %s: %w", key, err)
		}
		return nil
	})
}

func (r *repo) ResolveRef(ctx context.Context, ref string) (Hash, error) {
	if err := ValidateRef(ref); err != nil {
		return "", err
	}
	out, err := r.git(ctx, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	if err != nil {
		if exitedWithoutMessage(err, 1) || has(err, "unknown revision", "needed a single revision") {
			return "", fmt.Errorf("resolve %s: %w", ref, as(ErrRefNotFound, err))
		}
		return "", fmt.Errorf("resolve %s: %w", ref, err)
	}
	return Hash(strings.TrimSpace(string(out))), nil
}

func (r *repo) ListRefs(ctx context.Context, prefixes ...string) ([]Ref, error) {
	args := []string{"for-each-ref", "--format=%(refname) %(objectname)"}
	for _, p := range prefixes {
		if err := ValidateRef(p); err != nil {
			return nil, err
		}
		args = append(args, strings.TrimSuffix(p, "/"))
	}
	out, err := r.git(ctx, args...)
	if err != nil {
		return nil, fmt.Errorf("list refs: %w", err)
	}
	return ParseRefs(out), nil
}

func (r *repo) CreateBranch(ctx context.Context, name, startPoint string) error {
	if err := ValidateRef(name); err != nil {
		return err
	}
	args := []string{"branch", "--no-track", "--", name}
	if startPoint != "" {
		if err := ValidateRef(startPoint); err != nil {
			return err
		}
		args = append(args, startPoint)
	}
	return r.mutate(ctx, func() error {
		_, err := r.git(ctx, args...)
		switch {
		case err == nil:
			return nil
		case has(err, "already exists"):
			err = as(ErrBranchExists, err)
		case has(err, "not a valid object name", "not a valid branch point", "not a commit"):
			err = as(ErrRefNotFound, err)
		}
		return fmt.Errorf("create branch %s: %w", name, err)
	})
}

func (r *repo) DeleteBranch(ctx context.Context, name string) error {
	if err := ValidateRef(name); err != nil {
		return err
	}
	return r.mutate(ctx, func() error {
		_, err := r.git(ctx, "branch", "-D", "--", name)
		switch {
		case err == nil:
			return nil
		case has(err, "checked out at", "used by worktree"):
			err = as(ErrBranchInUse, err)
		case has(err, "not found"):
			err = as(ErrRefNotFound, err)
		}
		return fmt.Errorf("delete branch %s: %w", name, err)
	})
}

func (r *repo) UpdateRef(ctx context.Context, ref string, to Hash) error {
	if err := ValidateRef(ref); err != nil {
		return err
	}
	if err := ValidateRef(string(to)); err != nil {
		return err
	}
	return r.mutate(ctx, func() error {
		if _, err := r.git(ctx, "update-ref", "--", ref, string(to)); err != nil {
			return fmt.Errorf("update ref %s: %w", ref, err)
		}
		return nil
	})
}

func (r *repo) DeleteRef(ctx context.Context, ref string) error {
	if err := ValidateRef(ref); err != nil {
		return err
	}
	return r.mutate(ctx, func() error {
		if _, err := r.git(ctx, "update-ref", "-d", "--", ref); err != nil {
			return fmt.Errorf("delete ref %s: %w", ref, err)
		}
		return nil
	})
}

func (r *repo) IsAncestor(ctx context.Context, ancestor, descendant string) (bool, error) {
	if err := ValidateRef(ancestor); err != nil {
		return false, err
	}
	if err := ValidateRef(descendant); err != nil {
		return false, err
	}
	_, err := r.git(ctx, "merge-base", "--is-ancestor", ancestor, descendant)
	switch {
	case err == nil:
		return true, nil
	case exitedWithoutMessage(err, 1):
		return false, nil
	case has(err, "not a valid object name", "not a valid commit"):
		err = as(ErrRefNotFound, err)
	}
	return false, fmt.Errorf("is-ancestor %s %s: %w", ancestor, descendant, err)
}

func (r *repo) Log(ctx context.Context, revRange string) ([]CommitInfo, error) {
	if _, _, err := ParseRevs(revRange); err != nil {
		return nil, err
	}
	out, err := r.git(ctx, "log", "-z", "--no-color", "--format="+logFormat, revRange, "--")
	if err != nil {
		if has(err, "unknown revision", "does not have any commits", "bad revision") {
			err = as(ErrRefNotFound, err)
		}
		return nil, fmt.Errorf("log %s: %w", revRange, err)
	}
	return ParseLog(out)
}

func (r *repo) DiffNameStatus(ctx context.Context, from, to string) ([]FileChange, error) {
	a, err := r.ResolveRef(ctx, from)
	if err != nil {
		return nil, err
	}
	b, err := r.ResolveRef(ctx, to)
	if err != nil {
		return nil, err
	}
	// diff-tree is plumbing: it runs no external diff and no text conversion.
	out, err := r.git(ctx, "diff-tree", "-r", "-z", "--name-status", "--no-renames", string(a), string(b), "--")
	if err != nil {
		return nil, fmt.Errorf("diff %s..%s: %w", from, to, err)
	}
	return ParseNameStatus(out), nil
}

func (r *repo) ReadFile(ctx context.Context, ref, path string) ([]byte, error) {
	path, err := CleanRepoPath(path)
	if err != nil {
		return nil, err
	}
	h, err := r.ResolveRef(ctx, ref)
	if err != nil {
		return nil, err
	}
	out, err := r.git(ctx, "cat-file", "blob", string(h)+":"+path)
	if err != nil {
		// "bad file" is a folder (a tree) or a submodule at the path: not a
		// file either.
		if has(err, "not a valid object name", "does not exist", "exists on disk, but not in", "bad file") {
			err = as(ErrFileNotFound, err)
		}
		return nil, fmt.Errorf("read %s:%s: %w", ref, path, err)
	}
	return out, nil
}

func (r *repo) Fetch(ctx context.Context, remote string, refspecs ...string) error {
	return r.fetch(ctx, nil, remote, refspecs...)
}

func (r *repo) FetchWithAuth(ctx context.Context, auth Auth, remote string, refspecs ...string) error {
	return r.fetch(ctx, &auth, remote, refspecs...)
}

func (r *repo) fetch(ctx context.Context, auth *Auth, remote string, refspecs ...string) error {
	if err := ValidateRef(remote); err != nil {
		return err
	}
	if err := CheckURL(remote); err != nil {
		return fmt.Errorf("fetch: %w", err)
	}
	ro, err := r.g.authOpts(auth)
	if err != nil {
		return fmt.Errorf("fetch %s: %w", remote, err)
	}
	// --upload-pack: the command run for a local or ssh remote comes from
	// here, never from remote.<name>.uploadpack in the repository config.
	args := []string{"fetch", "--no-tags", "--no-write-fetch-head", "--recurse-submodules=no", "--upload-pack=git-upload-pack", "--", remote}
	for _, s := range refspecs {
		if err := ValidateRef(s); err != nil {
			return err
		}
		args = append(args, s)
	}
	return r.mutate(ctx, func() error {
		_, err := r.run(ctx, ro, args...)
		switch {
		case err == nil:
			return nil
		case has(err, "non-fast-forward", "[rejected]"):
			err = as(ErrNotFastForward, err)
		case has(err, "refusing to fetch into", "checked out at"):
			err = as(ErrBranchInUse, err)
		case has(err, "couldn't find remote ref"):
			err = as(ErrRefNotFound, err)
		}
		return fmt.Errorf("fetch %s: %w", RedactURL(remote), err)
	})
}

func (r *repo) CreateBundle(ctx context.Context, file string, revs ...string) error {
	if len(revs) == 0 {
		return fmt.Errorf("%w: bundle without revisions", ErrInvalidArgument)
	}
	abs, err := filepath.Abs(file)
	if err != nil {
		return fmt.Errorf("create bundle %s: %w", file, err)
	}
	args := []string{"bundle", "create", "-q", abs}
	for _, rev := range revs {
		if _, _, err := ParseRevs(rev); err != nil {
			return err
		}
		args = append(args, rev)
	}
	_, err = r.git(ctx, args...)
	switch {
	case err == nil:
		return nil
	case has(err, "empty bundle"):
		err = as(ErrEmptyBundle, err)
	case has(err, "ambiguous argument", "bad revision", "needed a single revision", "unknown revision"):
		err = as(ErrRefNotFound, err)
	}
	return fmt.Errorf("create bundle %s: %w", file, err)
}

func (r *repo) VerifyBundle(ctx context.Context, file string) error {
	abs, err := filepath.Abs(file)
	if err != nil {
		return fmt.Errorf("verify bundle %s: %w", file, err)
	}
	if _, err := ReadBundle(abs); err != nil {
		return fmt.Errorf("verify bundle %s: %w", file, err)
	}
	_, err = r.git(ctx, "bundle", "verify", abs)
	switch {
	case err == nil:
		return nil
	case has(err, "lacks these prerequisite", "prerequisite"):
		err = as(ErrMissingPrerequisite, err)
	case has(err, "not a valid bundle", "does not look like", "unrecognized header", "is not a bundle"):
		err = as(ErrInvalidBundle, err)
	}
	return fmt.Errorf("verify bundle %s: %w", file, err)
}

func (r *repo) FetchBundle(ctx context.Context, file string, refspecs ...string) error {
	if len(refspecs) == 0 {
		return fmt.Errorf("%w: fetching a bundle needs refspecs", ErrInvalidArgument)
	}
	abs, err := filepath.Abs(file)
	if err != nil {
		return fmt.Errorf("fetch bundle %s: %w", file, err)
	}
	if err := r.VerifyBundle(ctx, abs); err != nil {
		return err
	}
	// git does not check the objects of a bundle on fetch (fetch.fsckObjects
	// is ignored for them): they are checked first, in a folder of their own.
	if err := r.fsckBundle(ctx, abs); err != nil {
		return err
	}
	return r.Fetch(ctx, abs, refspecs...)
}

// fsckBundle checks the objects of a bundle as git checks the ones it
// receives from a remote with transfer.fsckObjects: `index-pack --strict`
// indexes the pack in a folder that is thrown away (the objects the
// repository already has serve the thin deltas and the links), so a malformed
// object from another machine never enters the repository of the engine.
func (r *repo) fsckBundle(ctx context.Context, file string) error {
	off, err := bundlePackOffset(file)
	if err != nil {
		return fmt.Errorf("check bundle %s: %w", file, err)
	}
	out, err := r.git(ctx, "rev-parse", "--path-format=absolute", "--git-path", "objects")
	if err != nil {
		return fmt.Errorf("check bundle %s: %w", file, err)
	}
	objects := strings.TrimSpace(string(out))
	quarantine, err := os.MkdirTemp("", "mestre-bundle-")
	if err != nil {
		return fmt.Errorf("check bundle %s: %w", file, err)
	}
	defer func() { _ = os.RemoveAll(quarantine) }()
	if err := privdir.Ensure(filepath.Join(quarantine, "pack")); err != nil {
		return fmt.Errorf("check bundle %s: %w", file, err)
	}
	f, err := os.Open(file)
	if err != nil {
		return fmt.Errorf("check bundle %s: %w", file, err)
	}
	defer func() { _ = f.Close() }()
	ro := runOpts{
		stdinFrom: io.NewSectionReader(f, off, 1<<62),
		env:       []string{"GIT_OBJECT_DIRECTORY=" + quarantine, "GIT_ALTERNATE_OBJECT_DIRECTORIES=" + objects},
	}
	if _, err := r.run(ctx, ro, "index-pack", "--strict", "--fix-thin", "--stdin"); err != nil {
		return fmt.Errorf("check bundle %s: %w", file, as(ErrInvalidBundle, err))
	}
	return nil
}

// bundlePackOffset is where the pack starts in a bundle file: after the empty
// line that ends the header.
func bundlePackOffset(file string) (int64, error) {
	f, err := os.Open(file)
	if err != nil {
		return 0, err
	}
	defer func() { _ = f.Close() }()
	br := bufio.NewReader(f)
	var off int64
	for {
		line, err := br.ReadString('\n')
		off += int64(len(line))
		if err != nil {
			return 0, fmt.Errorf("%w: truncated header", ErrInvalidBundle)
		}
		if strings.TrimRight(line, "\r\n") == "" {
			return off, nil
		}
	}
}

func (r *repo) AddWorktree(ctx context.Context, path string, opts WorktreeOptions) (Repo, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("add worktree %s: %w", path, err)
	}
	if err := ValidateRef(opts.Branch); err != nil {
		return nil, err
	}
	args := []string{"worktree", "add", "-q"}
	if opts.Create {
		args = append(args, "--no-track", "-b", opts.Branch, "--", abs)
		if opts.StartPoint != "" {
			if err := ValidateRef(opts.StartPoint); err != nil {
				return nil, err
			}
			args = append(args, opts.StartPoint)
		}
	} else {
		if _, err := r.ResolveRef(ctx, "refs/heads/"+opts.Branch); err != nil {
			return nil, err
		}
		args = append(args, "--", abs, opts.Branch)
	}
	err = r.mutate(ctx, func() error {
		_, err := r.treeGit(ctx, runOpts{}, args...)
		switch {
		case err == nil:
			return nil
		case has(err, "a branch named"):
			err = as(ErrBranchExists, err)
		case has(err, "already checked out", "already used by worktree"):
			err = as(ErrBranchInUse, err)
		case has(err, "already exists"):
			err = as(ErrPathExists, err)
		case has(err, "invalid reference", "not a valid reference", "not a commit", "not a valid object name"):
			err = as(ErrRefNotFound, err)
		}
		return fmt.Errorf("add worktree %s: %w", path, err)
	})
	if err != nil {
		return nil, err
	}
	return r.g.Open(ctx, abs)
}

func (r *repo) RemoveWorktree(ctx context.Context, path string, force bool) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("remove worktree %s: %w", path, err)
	}
	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, "--", abs)
	return r.mutate(ctx, func() error {
		_, err := r.treeGit(ctx, runOpts{}, args...)
		switch {
		case err == nil:
			return nil
		case has(err, "contains modified or untracked files"):
			err = as(ErrDirtyWorktree, err)
		case has(err, "is not a working tree"):
			err = as(ErrNoSuchWorktree, err)
		case has(err, "used by another process", "Device or resource busy", "Permission denied", "Directory not empty", "Access is denied", "Failed to delete"):
			// On Windows a file kept open (a terminal, an editor, the
			// antivirus) stops the removal; it goes away when it is closed.
			err = as(ErrWorktreeBusy, err)
		}
		return fmt.Errorf("remove worktree %s: %w", path, err)
	})
}

func (r *repo) ListWorktrees(ctx context.Context) ([]Worktree, error) {
	out, err := r.git(ctx, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, fmt.Errorf("list worktrees: %w", err)
	}
	return ParseWorktrees(out), nil
}

func (r *repo) Status(ctx context.Context) ([]StatusEntry, error) {
	if err := r.needTree(); err != nil {
		return nil, err
	}
	// --ignore-submodules=all: git status does not recurse into a submodule
	// a worktree user may have planted, so none of its clean filters run
	// (docs/arquitetura/seguranca.md#git-do-motor).
	out, err := r.treeGit(ctx, runOpts{}, "status", "--porcelain=v1", "-z", "--untracked-files=all", "--no-renames", "--ignore-submodules=all")
	if err != nil {
		return nil, fmt.Errorf("status: %w", err)
	}
	return ParseStatus(out), nil
}

func (r *repo) Stage(ctx context.Context, paths ...string) error {
	if err := r.needTree(); err != nil {
		return err
	}
	args := []string{"add", "-A"}
	if len(paths) > 0 {
		args = append(args, "--")
		args = append(args, paths...)
	}
	return r.mutate(ctx, func() error {
		if _, err := r.treeGit(ctx, runOpts{}, args...); err != nil {
			return fmt.Errorf("stage: %w", err)
		}
		return nil
	})
}

// gitDate renders when in the "<unix seconds> +0000" form git always reads,
// whatever its version or locale; a zero when is replaced by now.
func (r *repo) gitDate(when time.Time) string {
	if when.IsZero() {
		when = r.g.clk.Now()
	}
	return fmt.Sprintf("%d +0000", when.Unix())
}

func (r *repo) Commit(ctx context.Context, opts CommitOptions) (Hash, error) {
	if err := r.needTree(); err != nil {
		return "", err
	}
	author, committer := opts.Author, opts.Committer
	if committer == (Signature{}) {
		committer = author
	}
	if strings.TrimSpace(opts.Message) == "" || author.Name == "" || author.Email == "" || committer.Name == "" || committer.Email == "" {
		return "", fmt.Errorf("%w: commit needs a message and an author", ErrInvalidArgument)
	}
	args := []string{"commit", "--no-verify", "--no-gpg-sign", "--cleanup=whitespace", "--quiet", "-F", "-"}
	if opts.AllowEmpty {
		args = append(args, "--allow-empty")
	}
	env := []string{
		"GIT_AUTHOR_NAME=" + author.Name, "GIT_AUTHOR_EMAIL=" + author.Email, "GIT_AUTHOR_DATE=" + r.gitDate(author.When),
		"GIT_COMMITTER_NAME=" + committer.Name, "GIT_COMMITTER_EMAIL=" + committer.Email, "GIT_COMMITTER_DATE=" + r.gitDate(committer.When),
	}
	var head Hash
	err := r.mutate(ctx, func() error {
		_, err := r.treeGit(ctx, runOpts{stdin: []byte(opts.Message), env: env}, args...)
		if err != nil {
			if has(err, "nothing to commit", "no changes added to commit", "nothing added to commit") {
				err = as(ErrNothingToCommit, err)
			}
			return fmt.Errorf("commit: %w", err)
		}
		head, err = r.ResolveRef(ctx, "HEAD")
		return err
	})
	return head, err
}

func (r *repo) MergeFFOnly(ctx context.Context, ref string) (Hash, error) {
	if err := r.needTree(); err != nil {
		return "", err
	}
	if err := ValidateRef(ref); err != nil {
		return "", err
	}
	var head Hash
	err := r.mutate(ctx, func() error {
		_, err := r.treeGit(ctx, runOpts{}, "merge", "--ff-only", "--no-edit", "--no-stat", "--quiet", ref)
		switch {
		case err == nil:
		case has(err, "not possible to fast-forward"):
			return fmt.Errorf("merge %s: %w", ref, as(ErrNotFastForward, err))
		case has(err, "would be overwritten by merge", "your local changes"):
			return fmt.Errorf("merge %s: %w", ref, as(ErrLocalChanges, err))
		case has(err, "not something we can merge", "unknown revision"):
			return fmt.Errorf("merge %s: %w", ref, as(ErrRefNotFound, err))
		default:
			return fmt.Errorf("merge %s: %w", ref, err)
		}
		head, err = r.ResolveRef(ctx, "HEAD")
		return err
	})
	return head, err
}

func (r *repo) HeadBranch(ctx context.Context) (string, error) {
	if err := r.needTree(); err != nil {
		return "", err
	}
	out, err := r.git(ctx, "symbolic-ref", "-q", "HEAD")
	if err != nil {
		if exitedWithoutMessage(err, 1) {
			return "", nil // detached
		}
		return "", fmt.Errorf("head branch: %w", err)
	}
	return strings.TrimPrefix(strings.TrimSpace(string(out)), "refs/heads/"), nil
}

func (r *repo) SetSkipWorktree(ctx context.Context, on bool, paths ...string) error {
	if err := r.needTree(); err != nil {
		return err
	}
	if len(paths) == 0 {
		return fmt.Errorf("%w: skip-worktree without paths", ErrInvalidArgument)
	}
	flag := "--skip-worktree"
	if !on {
		flag = "--no-skip-worktree"
	}
	return r.mutate(ctx, func() error {
		if _, err := r.git(ctx, append([]string{"update-index", flag, "--"}, paths...)...); err != nil {
			return fmt.Errorf("update-index %s: %w", flag, err)
		}
		return nil
	})
}

func (r *repo) SkipWorktreePaths(ctx context.Context) ([]string, error) {
	if err := r.needTree(); err != nil {
		return nil, err
	}
	out, err := r.git(ctx, "ls-files", "-v", "-z")
	if err != nil {
		return nil, fmt.Errorf("ls-files: %w", err)
	}
	var paths []string
	for rec := range strings.SplitSeq(string(out), "\x00") {
		if len(rec) > 2 && (rec[0] == 'S' || rec[0] == 's') {
			paths = append(paths, rec[2:])
		}
	}
	return paths, nil
}

// excludeFile returns the path of info/exclude, which all worktrees share.
func (r *repo) excludeFile(ctx context.Context) (string, error) {
	out, err := r.git(ctx, "rev-parse", "--path-format=absolute", "--git-path", "info/exclude")
	if err != nil {
		return "", fmt.Errorf("locate info/exclude: %w", err)
	}
	return filepath.Clean(filepath.FromSlash(strings.TrimSpace(string(out)))), nil
}

func (r *repo) SetExcludeBlock(ctx context.Context, name string, patterns []string) error {
	if err := ValidateExcludeBlock(name, patterns); err != nil {
		return err
	}
	file, err := r.excludeFile(ctx)
	if err != nil {
		return err
	}
	return r.mutate(ctx, func() error {
		old, err := os.ReadFile(file)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("read info/exclude: %w", err)
		}
		next := ApplyExcludeBlock(string(old), name, patterns)
		if next == string(old) {
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			return fmt.Errorf("create info dir: %w", err)
		}
		tmp := file + ".mestre-tmp"
		if err := os.WriteFile(tmp, []byte(next), 0o644); err != nil {
			return fmt.Errorf("write info/exclude: %w", err)
		}
		if err := os.Rename(tmp, file); err != nil {
			_ = os.Remove(tmp)
			return fmt.Errorf("replace info/exclude: %w", err)
		}
		return nil
	})
}

func (r *repo) ExcludeBlock(ctx context.Context, name string) ([]string, error) {
	if err := ValidateExcludeBlock(name, nil); err != nil {
		return nil, err
	}
	file, err := r.excludeFile(ctx)
	if err != nil {
		return nil, err
	}
	content, err := os.ReadFile(file)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read info/exclude: %w", err)
	}
	patterns, _ := ParseExcludeBlock(string(content), name)
	return patterns, nil
}

// pushTimeout bounds a push: it holds the lock of the repository over the network.
const pushTimeout = 5 * time.Minute

func (r *repo) PushWithAuth(ctx context.Context, auth Auth, remote, branch string, force bool) error {
	if err := ValidateRef(remote); err != nil {
		return err
	}
	if err := CheckURL(remote); err != nil {
		return fmt.Errorf("push: %w", err)
	}
	if err := ValidateBranch(branch); err != nil {
		return err
	}
	ro, err := r.g.authOpts(&auth)
	if err != nil {
		return fmt.Errorf("push %s: %w", RedactURL(remote), err)
	}
	ctx, cancel := clock.WithTimeout(ctx, r.g.clk, pushTimeout)
	defer cancel()
	// --no-verify: no hook of the repository runs as the engine; --receive-pack
	// is named here, never read from the configuration. The refspec is the branch
	// to the branch of the same name: nothing else.
	args := []string{"push", "--no-verify", "--recurse-submodules=no", "--receive-pack=git-receive-pack"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, "--", remote, "refs/heads/"+branch+":refs/heads/"+branch)
	return r.mutate(ctx, func() error {
		_, err := r.run(ctx, ro, args...)
		if err == nil {
			return nil
		}
		return fmt.Errorf("push %s: %w", RedactURL(remote), classifyPush(err))
	})
}

// classifyPush tells what the remote said. Only what the remote says counts (the
// lines that start with "remote:", and the verdict at the end of a rejected
// line): the names of the refs are in the text too, and a branch can be called
// anything.
func classifyPush(err error) error {
	var ge *Error
	if !errors.As(err, &ge) {
		return err
	}
	var remote strings.Builder
	rejected := ""
	for _, l := range strings.Split(ge.Stderr, "\n") {
		l = strings.TrimSpace(l)
		switch {
		case strings.HasPrefix(l, "remote:"):
			remote.WriteString(strings.ToLower(strings.TrimSpace(strings.TrimPrefix(l, "remote:"))) + "\n")
		case strings.HasPrefix(l, "!") && strings.HasSuffix(l, ")"):
			rejected = strings.ToLower(l[strings.LastIndexByte(l, '(')+1 : len(l)-1])
		}
	}
	said := remote.String()
	contains := func(subs ...string) bool {
		for _, s := range subs {
			if strings.Contains(said, s) {
				return true
			}
		}
		return false
	}
	switch {
	case strings.Contains(said, "workflow") && contains("permission", "scope"):
		return as(ErrPushWorkflow, err)
	case contains("verified signatures", "must have valid signatures"):
		return as(ErrPushSignature, err)
	case contains("protected branch", "gh006", "gh013", "repository rule violations"):
		return as(ErrPushProtected, err)
	case rejected == "non-fast-forward" || rejected == "fetch first":
		return as(ErrNotFastForward, err)
	case strings.HasSuffix(rejected, "hook declined"):
		return as(ErrPushProtected, err)
	case rejected != "":
		return as(ErrPushRejected, err)
	}
	return err
}

func (r *repo) AddedLines(ctx context.Context, base, head string) ([]AddedLine, error) {
	a, err := r.ResolveRef(ctx, base)
	if err != nil {
		return nil, err
	}
	b, err := r.ResolveRef(ctx, head)
	if err != nil {
		return nil, err
	}
	rng := string(a) + "..." + string(b)
	// the size first (a few bytes per file), so a huge file is not read into memory
	stat, err := r.git(ctx, "diff", "--no-color", "--no-ext-diff", "--no-textconv", "--no-renames", "--ignore-submodules=all", "--text", "--numstat", rng, "--")
	if err != nil {
		return nil, fmt.Errorf("diff %s...%s: %w", base, head, err)
	}
	total := 0
	for _, l := range strings.Split(string(stat), "\n") {
		if f := strings.Fields(l); len(f) >= 2 {
			n, _ := strconv.Atoi(f[0])
			total += n
		}
	}
	if total > MaxAddedLines {
		return nil, fmt.Errorf("%w: %d lines added (at most %d)", ErrDiffTooLarge, total, MaxAddedLines)
	}
	out, err := r.git(ctx, "diff", "--no-color", "--no-ext-diff", "--no-textconv", "--no-renames", "--ignore-submodules=all", "--text", "--unified=0", "--no-prefix", rng, "--")
	if err != nil {
		return nil, fmt.Errorf("diff %s...%s: %w", base, head, err)
	}
	return ParseAddedLines(out), nil
}
