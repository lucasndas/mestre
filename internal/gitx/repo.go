package gitx

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
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
}

var _ Repo = (*repo)(nil)

func (r *repo) Dir() string { return r.dir }

func (r *repo) git(ctx context.Context, args ...string) ([]byte, error) {
	return r.run(ctx, runOpts{}, args...)
}

// run runs git in the folder of the repository. A worktree opened by its
// record is checked against it first (verify) and runs pinned to it.
func (r *repo) run(ctx context.Context, ro runOpts, args ...string) ([]byte, error) {
	ro.dir = r.dir
	if r.rec != nil {
		if err := r.rec.verify(); err != nil {
			return nil, err
		}
		ro.env = append(r.rec.pin(), ro.env...)
	}
	return r.g.run(ctx, ro, args...)
}

// noFilters returns the options of a call that may read or write files of a
// working tree (add, commit, status, checkout): every filter driver defined
// in the configuration the repository sees is emptied on the command line,
// so a driver set in a config file the engine does not control never runs
// as the engine. A driver added between this listing and the call is the
// residual risk; the configuration of the mirror is not writable by
// mestre-agente (docs/arquitetura/seguranca.md#git-do-motor).
func (r *repo) noFilters(ctx context.Context) (runOpts, error) {
	var ro runOpts
	out, err := r.git(ctx, "config", "--null", "--name-only", "--get-regexp", `^filter\.`)
	if err != nil {
		if exitedWithoutMessage(err, 1) {
			return ro, nil // no filter configured
		}
		return ro, fmt.Errorf("list filter drivers: %w", err)
	}
	seen := map[string]bool{}
	for key := range strings.SplitSeq(string(out), "\x00") {
		key = strings.TrimSpace(key)
		dot := strings.LastIndexByte(key, '.')
		if !strings.HasPrefix(strings.ToLower(key), "filter.") || dot <= len("filter.") {
			continue
		}
		driver := key[len("filter."):dot]
		if seen[driver] {
			continue
		}
		seen[driver] = true
		ro.config = append(ro.config,
			"filter."+driver+".clean=", "filter."+driver+".smudge=",
			"filter."+driver+".process=", "filter."+driver+".required=false")
	}
	return ro, nil
}

// treeGit runs a command that touches the working tree with noFilters.
func (r *repo) treeGit(ctx context.Context, ro runOpts, args ...string) ([]byte, error) {
	safe, err := r.noFilters(ctx)
	if err != nil {
		return nil, err
	}
	safe.stdin, safe.env = ro.stdin, ro.env
	return r.run(ctx, safe, args...)
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

func (r *repo) ReadFile(ctx context.Context, ref, path string) ([]byte, error) {
	if path == "" || strings.ContainsAny(path, "\x00\n") {
		return nil, fmt.Errorf("%w: path %q", ErrInvalidArgument, path)
	}
	h, err := r.ResolveRef(ctx, ref)
	if err != nil {
		return nil, err
	}
	out, err := r.git(ctx, "cat-file", "blob", string(h)+":"+path)
	if err != nil {
		if has(err, "not a valid object name", "does not exist") {
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
	return r.Fetch(ctx, abs, refspecs...)
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
	out, err := r.treeGit(ctx, runOpts{}, "status", "--porcelain=v1", "-z", "--untracked-files=all", "--no-renames")
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
