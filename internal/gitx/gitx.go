// Package gitx is the single wrapper around the system git binary (MinVersion
// or newer; docs/decisoes/0001-stack-go.md). Nothing else in the Mestre starts
// git or links a Go git library. The package declares the operations the
// engine needs (mirror, worktrees, bundles, info/exclude, skip-worktree,
// fast-forward merges, commits with explicit identities) as two small
// interfaces, Git and Repo. Real implements them over the binary; the
// in-memory fake of internal/testkit/fakegit implements them for the fast
// suite (docs/qualidade/dubles.md).
//
// Paths passed to the interfaces are OS paths; paths inside a repository
// (file names, patterns) and refs are always slash-separated.
package gitx

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"time"
)

// Errors returned by every implementation; test them with errors.Is.
var (
	ErrGitNotFound                = errors.New("gitx: git binary not found in PATH")
	ErrGitTooOld                  = errors.New("gitx: git version too old")
	ErrExternalProcessInFastSuite = errors.New("external process in fast suite: use the fake git")
	ErrInvalidArgument            = errors.New("gitx: invalid argument")
	ErrNotARepo                   = errors.New("gitx: not a git repository")
	ErrBareRepo                   = errors.New("gitx: repository has no working tree")
	ErrRefNotFound                = errors.New("gitx: ref not found")
	ErrFileNotFound               = errors.New("gitx: file not found at ref")
	ErrBranchExists               = errors.New("gitx: branch already exists")
	ErrBranchInUse                = errors.New("gitx: branch is checked out in a worktree")
	ErrPathExists                 = errors.New("gitx: path already exists")
	ErrNoSuchWorktree             = errors.New("gitx: no such worktree")
	ErrDirtyWorktree              = errors.New("gitx: worktree has uncommitted changes")
	ErrNotFastForward             = errors.New("gitx: not a fast-forward")
	ErrLocalChanges               = errors.New("gitx: local changes would be overwritten")
	ErrMergeConflict              = errors.New("gitx: the merge has conflicts")
	ErrWorktreeBusy               = errors.New("gitx: worktree files are in use; try again later")
	ErrNothingToCommit            = errors.New("gitx: nothing to commit")
	ErrEmptyBundle                = errors.New("gitx: bundle would be empty")
	ErrInvalidBundle              = errors.New("gitx: invalid bundle")
	ErrMissingPrerequisite        = errors.New("gitx: repository lacks the bundle prerequisite commits")
	ErrConfigNotSet               = errors.New("gitx: config key not set")
	ErrUnsafeRepo                 = errors.New("gitx: the repository configuration could make git run a command")
	// The refusals of a push, told apart by what the remote said.
	ErrPushProtected = errors.New("gitx: the remote refused the push: a protection or ruleset of the branch")
	ErrPushWorkflow  = errors.New("gitx: the remote refused the push: it changes workflows and the App has no permission for them")
	ErrPushSignature = errors.New("gitx: the remote refused the push: the rules ask for signed commits")
	ErrPushRejected  = errors.New("gitx: the remote refused the push")
	// ErrDiffTooLarge: the change adds too many lines to be read.
	ErrDiffTooLarge = errors.New("gitx: the change is too large to read")
)

// NoLazyFetchVersion is the first git that honours GIT_NO_LAZY_FETCH, which
// every call sets: from it on, git never fetches a missing object from a
// promisor remote on its own. Before it the engine refuses a repository
// with a promisor remote (ErrUnsafeRepo).
var NoLazyFetchVersion = Version{Major: 2, Minor: 44}

// MinVersion is the oldest supported git on this system, a security floor
// (docs/decisoes/0001-stack-go.md): it is past the safe.directory fix
// (CVE-2022-24765, 2.35.2) and every feature gitx uses (GIT_CONFIG_GLOBAL
// 2.32, GIT_CONFIG_COUNT 2.31).
var MinVersion = MinVersionFor(runtime.GOOS)

// MinVersionFor is the floor for a GOOS. Linux: 2.43.0, the git of Ubuntu
// 24.04 LTS, the reference distribution; distributions backport security
// fixes without changing the version, so the floor is its package. Windows:
// 2.50.1, because Git for Windows never backports and that release closes
// the last clone and credential-helper CVEs (CVE-2024-32002, CVE-2025-48384,
// CVE-2025-48386).
func MinVersionFor(goos string) Version {
	if goos == "windows" {
		return Version{Major: 2, Minor: 50, Patch: 1}
	}
	return Version{Major: 2, Minor: 43}
}

// Version is a git version; Raw is the output of `git version`.
type Version struct {
	Major, Minor, Patch int
	Raw                 string
}

// String returns "major.minor.patch".
func (v Version) String() string { return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch) }

// Less reports whether v is older than o (Raw is ignored).
func (v Version) Less(o Version) bool {
	if v.Major != o.Major {
		return v.Major < o.Major
	}
	if v.Minor != o.Minor {
		return v.Minor < o.Minor
	}
	return v.Patch < o.Patch
}

// Hash is a full object name.
type Hash string

// Short returns the first 7 characters.
func (h Hash) Short() string {
	if len(h) > 7 {
		return string(h[:7])
	}
	return string(h)
}

// Signature is an author or committer. A zero When means "now" by the
// implementation's clock.
type Signature struct {
	Name  string
	Email string
	When  time.Time
}

// Ref is a ref and the object it points at.
type Ref struct {
	Name string // full name, e.g. refs/heads/main
	Hash Hash
}

// CommitInfo is one commit as listed by Repo.Log.
type CommitInfo struct {
	Hash      Hash
	Parents   []Hash
	Author    Signature
	Committer Signature
	Message   string // full message without the trailing newline
}

// Subject returns the first line of the message.
func (c CommitInfo) Subject() string {
	for i := range len(c.Message) {
		if c.Message[i] == '\n' {
			return c.Message[:i]
		}
	}
	return c.Message
}

// Worktree is one entry of Repo.ListWorktrees. The first entry is the main
// one (the bare mirror itself, or the repository's own checkout).
type Worktree struct {
	Path     string
	Head     Hash   // "" for an unborn branch
	Branch   string // short name; "" when detached or bare
	Detached bool
	Bare     bool
}

// StatusEntry is one line of `git status --porcelain`.
type StatusEntry struct {
	Index    byte // staged state: ' ', 'M', 'A', 'D', 'R', 'C', '?'
	Worktree byte // unstaged state
	Path     string
	OrigPath string // source of a rename or copy
}

// TrackedStatus is what one `git status --branch` says about a worktree, for
// the decision on a commit: the branch checked out and the tracked entries
// (staged and, in the worktree, changed). Untracked files are not read.
type TrackedStatus struct {
	Branch   string // short name; "" when detached
	Detached bool
	Entries  []StatusEntry // never untracked
}

// Untracked reports whether the file is new to git.
func (e StatusEntry) Untracked() bool { return e.Index == '?' && e.Worktree == '?' }

// FileChange is one file that differs between two commits: Status is the
// letter of the name-status listing ('A' added, 'M' modified, 'D' deleted,
// 'T' type changed; renames are not detected, so a rename is a deletion and an
// addition).
type FileChange struct {
	Status byte
	Path   string
}

// String is the line of the name-status listing ("M src/login.go").
func (c FileChange) String() string { return string(c.Status) + " " + c.Path }

// AddedLine is a line a change adds: the file, the number of the line in the new
// version and its text (without the newline).
type AddedLine struct {
	Path string
	Line int
	Text string
}

// BundleInfo is what the header of a bundle file declares.
type BundleInfo struct {
	Refs          []Ref  // refs the bundle carries
	Prerequisites []Hash // commits the receiving repository must already have
}

// InitOptions configures Git.Init.
type InitOptions struct {
	Bare          bool
	InitialBranch string // default "main"
}

// CloneOptions configures Git.Clone.
type CloneOptions struct {
	// Bare clones without a working tree: the workspace mirror
	// (docs/orquestracao/workspaces.md#espelho-e-clone-principal-na-cidadela).
	Bare bool
	// Auth authenticates an HTTP(S) url through the environment of git; it
	// is not kept in the clone.
	Auth *Auth
}

// WorktreeOptions configures Repo.AddWorktree.
type WorktreeOptions struct {
	Branch string // branch checked out in the new worktree
	// Create makes Branch at StartPoint (default HEAD); otherwise Branch
	// must exist and not be checked out elsewhere.
	Create     bool
	StartPoint string
}

// CommitOptions configures Repo.Commit. The identity is the only one the
// commit gets: the process environment of the engine and the user's git
// configuration never leak into it.
type CommitOptions struct {
	Message    string
	Author     Signature
	Committer  Signature // zero: same as Author
	AllowEmpty bool
}

// Git creates and opens repositories.
type Git interface {
	// Version returns the git version, or ErrGitNotFound / ErrGitTooOld.
	Version(ctx context.Context) (Version, error)
	// Init creates a repository in dir (created if missing).
	Init(ctx context.Context, dir string, opts InitOptions) (Repo, error)
	// Clone copies url (a URL or a local path) into dir. A url with
	// credentials is refused (ErrCredentialsInURL): they go in opts.Auth.
	Clone(ctx context.Context, url, dir string, opts CloneOptions) (Repo, error)
	// Open returns the repository or worktree rooted at dir, or ErrNotARepo.
	// It follows the .git file and the commondir of a worktree, so a table
	// worktree an agent ran in is opened by its record (WorktreeOpener).
	Open(ctx context.Context, dir string) (Repo, error)
}

// WorktreeOpener opens a worktree only by the engine's record of it
// (Real.OpenWorktree): every engine git call on a table worktree goes
// through it, never through Open, which follows the .git file and the
// commondir an agent can rewrite (docs/arquitetura/seguranca.md#git-do-motor).
// A worktree that no longer points at its record is ErrWorktreeMoved.
type WorktreeOpener interface {
	OpenWorktree(ctx context.Context, rec WorktreeRecord) (Repo, error)
}

// Repo is one repository or one of its worktrees. Worktrees of a repository
// share refs and objects; implementations serialize the operations that
// change them, while reads run concurrently
// (docs/decisoes/0004-mesa-igual-worktree.md).
type Repo interface {
	// Dir is the path the repository was opened with.
	Dir() string

	GetConfig(ctx context.Context, key string) (string, error) // ErrConfigNotSet
	SetConfig(ctx context.Context, key, value string) error

	// ResolveRef returns the commit of a branch, ref, hash or HEAD
	// (ErrRefNotFound).
	ResolveRef(ctx context.Context, ref string) (Hash, error)
	// ListRefs lists the refs under the given prefixes (all if none), sorted.
	ListRefs(ctx context.Context, prefixes ...string) ([]Ref, error)
	// CreateBranch creates a local branch at startPoint (ErrBranchExists).
	CreateBranch(ctx context.Context, name, startPoint string) error
	// DeleteBranch deletes a local branch even if unmerged (ErrBranchInUse).
	DeleteBranch(ctx context.Context, name string) error
	// UpdateRef points a full ref name at a commit, creating it.
	UpdateRef(ctx context.Context, ref string, to Hash) error
	// DeleteRef deletes a full ref name; a missing ref is not an error.
	DeleteRef(ctx context.Context, ref string) error
	// IsAncestor reports whether ancestor is reachable from descendant.
	IsAncestor(ctx context.Context, ancestor, descendant string) (bool, error)
	// Log lists the commits of a revision or range ("main", "a..b"), newest
	// first.
	Log(ctx context.Context, revRange string) ([]CommitInfo, error)
	// DiffNameStatus lists the files that differ between two commits (hashes,
	// branches or refs), sorted by path (ErrRefNotFound). It never runs a
	// diff driver or a text conversion.
	DiffNameStatus(ctx context.Context, from, to string) ([]FileChange, error)
	// ReadFile returns path as committed at ref (ErrRefNotFound,
	// ErrFileNotFound).
	ReadFile(ctx context.Context, ref, path string) ([]byte, error)

	// Fetch fetches refspecs from a remote name, URL or path. A URL with
	// credentials is refused (ErrCredentialsInURL).
	Fetch(ctx context.Context, remote string, refspecs ...string) error
	// FetchWithAuth is Fetch with auth handed to git in its environment.
	FetchWithAuth(ctx context.Context, auth Auth, remote string, refspecs ...string) error
	// PushWithAuth pushes the commit (a full hash, the one that was checked) to
	// the branch of a remote name, URL or path, with auth handed to git in its
	// environment: only that ref, never a delete, a glob or another destination,
	// and the commit that was read, not what the branch is by the time git looks.
	// force pushes a commit that is not a fast-forward of the remote's. A refusal
	// of the remote is ErrNotFastForward, ErrPushProtected, ErrPushWorkflow,
	// ErrPushSignature or ErrPushRejected.
	PushWithAuth(ctx context.Context, auth Auth, remote, branch, commit string, force bool) error
	// PushWithLease is PushWithAuth that replaces a branch the commit is not a
	// fast-forward of only if the remote still has expected (the lease of a
	// forced push, with the commit named): whoever moved the branch since it was
	// read is not overwritten (ErrNotFastForward or ErrPushRejected). An empty
	// expected says the branch must not exist yet.
	PushWithLease(ctx context.Context, auth Auth, remote, branch, commit, expected string) error
	// AddedLinesEach lists the lines added by each commit of base..head (merges
	// against their first parent): a line that one commit adds and a later one
	// takes out is listed, since the push sends both. Like AddedLines, the files are
	// read as text and a change of more than MaxAddedLines lines is ErrDiffTooLarge.
	AddedLinesEach(ctx context.Context, base, head string) ([]AddedLine, error)
	// AddedLines lists the lines added from the merge base of base and head up to
	// head (git diff base...head), by file and line of head. Every file is read as
	// text (a NUL byte or an attribute does not hide a line), submodules are not
	// followed, and a change that adds more than MaxAddedLines lines is
	// ErrDiffTooLarge. It never runs a diff driver or a text conversion.
	AddedLines(ctx context.Context, base, head string) ([]AddedLine, error)
	// CreateBundle writes a bundle of revs (a ref such as "main" or an
	// incremental range such as "<hash>..main"). ErrEmptyBundle when the
	// range holds no commit.
	CreateBundle(ctx context.Context, file string, revs ...string) error
	// VerifyBundle checks that file is a bundle this repository can apply
	// (ErrInvalidBundle, ErrMissingPrerequisite).
	VerifyBundle(ctx context.Context, file string) error
	// FetchBundle imports the refs of a bundle through refspecs such as
	// "refs/heads/b:refs/mestre/cidadela/b". Without a leading "+" a ref
	// moves only forward (ErrNotFastForward).
	FetchBundle(ctx context.Context, file string, refspecs ...string) error

	// AddWorktree creates a worktree at path and returns its handle
	// (ErrBranchInUse, ErrBranchExists, ErrPathExists, ErrRefNotFound).
	AddWorktree(ctx context.Context, path string, opts WorktreeOptions) (Repo, error)
	// RemoveWorktree removes the worktree at path; without force it refuses
	// one with uncommitted changes (ErrDirtyWorktree, ErrNoSuchWorktree).
	RemoveWorktree(ctx context.Context, path string, force bool) error
	ListWorktrees(ctx context.Context) ([]Worktree, error)

	// The methods below need a working tree (ErrBareRepo otherwise).

	// Status lists changed and untracked files, honoring info/exclude and
	// leaving out skip-worktree files; empty means clean.
	Status(ctx context.Context) ([]StatusEntry, error)
	// TrackedStatus reads the branch and the tracked entries of the worktree
	// at this moment, in one git call (the index is always read anew).
	TrackedStatus(ctx context.Context) (TrackedStatus, error)
	// Stage stages the paths, deletions included; no paths stages all.
	Stage(ctx context.Context, paths ...string) error
	// Commit commits the staged changes (ErrNothingToCommit) and returns the
	// new HEAD.
	Commit(ctx context.Context, opts CommitOptions) (Hash, error)
	// MergeFFOnly fast-forwards the checked-out branch to ref and returns
	// the new HEAD (ErrNotFastForward, ErrLocalChanges). Nothing changes on
	// failure.
	MergeFFOnly(ctx context.Context, ref string) (Hash, error)
	// Merge merges ref into the checked-out branch, with a merge commit when
	// the histories differ (a fast-forward is a fast-forward), by the identity
	// of who and without hooks or signatures, and returns the new HEAD. A merge
	// that conflicts is undone, leaving the worktree as it was (ErrMergeConflict;
	// ErrLocalChanges, ErrRefNotFound).
	Merge(ctx context.Context, ref string, who Signature) (Hash, error)
	// HeadBranch is the branch HEAD points at ("" when detached).
	HeadBranch(ctx context.Context) (string, error)
	// SetSkipWorktree turns the skip-worktree flag of tracked files on or off.
	SetSkipWorktree(ctx context.Context, on bool, paths ...string) error
	SkipWorktreePaths(ctx context.Context) ([]string, error)

	// SetExcludeBlock writes the patterns as the named block of the
	// repository's info/exclude (shared by all its worktrees), replacing
	// the previous block of that name; no patterns removes the block. Lines
	// outside the managed blocks are never touched.
	SetExcludeBlock(ctx context.Context, name string, patterns []string) error
	ExcludeBlock(ctx context.Context, name string) ([]string, error)
}

// MaxAddedLines is the most lines AddedLines reads of one change.
const MaxAddedLines = 200_000

// ValidateBranch checks the name of a branch as git does (git check-ref-format
// --branch), without starting a process: no empty or "-" start, no "..", no
// control characters, space, ~ ^ : ? * [ \ or "@{", no ".lock" end, no "/" at the
// ends or twice in a row, no "." at the start of a part.
func ValidateBranch(b string) error {
	bad := b == "" || b == "@" || strings.HasPrefix(b, "-") || strings.HasPrefix(b, "/") || strings.HasSuffix(b, "/") || strings.HasSuffix(b, ".") ||
		strings.Contains(b, "..") || strings.Contains(b, "//") || strings.Contains(b, "@{") || strings.HasSuffix(b, ".lock") ||
		strings.ContainsAny(b, " ~^:?*[\\\x7f") || strings.HasPrefix(b, "refs/")
	for _, r := range b {
		bad = bad || r < 0x20
	}
	for _, part := range strings.Split(b, "/") {
		bad = bad || strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".lock")
	}
	if bad {
		return fmt.Errorf("%w: branch %q", ErrInvalidArgument, b)
	}
	return nil
}

// ValidateCommit checks a full commit hash (40 or 64 hexadecimal digits).
func ValidateCommit(c string) error {
	if (len(c) != 40 && len(c) != 64) || strings.Trim(c, "0123456789abcdef") != "" {
		return fmt.Errorf("%w: commit %q", ErrInvalidArgument, c)
	}
	return nil
}
