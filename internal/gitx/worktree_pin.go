package gitx

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/lucasndas/mestre/internal/safefile"
)

// ErrWorktreeMoved is a worktree whose .git file or commondir no longer
// points at the engine's record of it: an agent changed them (both are in
// folders the account of the agents writes). It is an alert, never trusted.
var ErrWorktreeMoved = errors.New("gitx: the worktree does not point at its record")

var _ WorktreeOpener = (*Real)(nil)

// WorktreeRecord is the engine's own record of a worktree that came out of a
// repository (the mirror): the worktree, its git folder in the repository
// (<common>/worktrees/<name>) and the repository's common dir. The engine
// takes it with Record when it creates the worktree, before any agent runs
// there, and keeps it; afterwards it opens the worktree only through it
// (OpenWorktree), never by following the .git file and the commondir of the
// worktree (docs/arquitetura/seguranca.md#git-do-motor).
type WorktreeRecord struct {
	Dir, GitDir, Common string
}

// Record reads the record of the worktree at dir. Call it right after
// AddWorktree, before an agent runs in the worktree: it is the only moment
// what the .git file says is the engine's own.
func (g *Real) Record(ctx context.Context, dir string) (WorktreeRecord, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return WorktreeRecord{}, fmt.Errorf("record %s: %w", dir, err)
	}
	gitdir, common, _, err := g.resolve(ctx, runOpts{dir: abs})
	if err != nil {
		return WorktreeRecord{}, fmt.Errorf("record %s: %w", dir, err)
	}
	rec := WorktreeRecord{Dir: abs, GitDir: gitdir, Common: common}
	if err := rec.check(); err != nil {
		return WorktreeRecord{}, err
	}
	return rec, nil
}

// check refuses a record that is not a linked worktree of its common dir.
func (rec WorktreeRecord) check() error {
	if !filepath.IsAbs(rec.Dir) || !filepath.IsAbs(rec.GitDir) || !filepath.IsAbs(rec.Common) ||
		lockKey(filepath.Dir(rec.GitDir)) != lockKey(filepath.Join(rec.Common, "worktrees")) {
		return fmt.Errorf("%w: %s is not a worktree of %s (git folder %s)", ErrInvalidArgument, rec.Dir, rec.Common, rec.GitDir)
	}
	return nil
}

// pin is the environment that makes git use the record: GIT_DIR (so the
// .git file of the worktree is not read), GIT_COMMON_DIR (the configuration
// and the objects of the recorded common dir) and GIT_WORK_TREE. git still
// reads the refs through the commondir file of the git folder (the files
// backend does not look at GIT_COMMON_DIR), so verify checks it before every
// call.
func (rec WorktreeRecord) pin() []string {
	return []string{"GIT_DIR=" + rec.GitDir, "GIT_COMMON_DIR=" + rec.Common, "GIT_WORK_TREE=" + rec.Dir}
}

// maxPointer bounds the .git file and the commondir.
const maxPointer = 4096

// verify reads the .git file of the worktree and the commondir of its git
// folder, without following a link (internal/safefile), and compares them
// with the record; it starts no process, so it runs before every call of a
// worktree opened by its record. A change between this check and the call
// is the residual risk (docs/arquitetura/seguranca.md#git-do-motor).
func (rec WorktreeRecord) verify() error {
	pointer := func(file, prefix, base string) (string, error) {
		b, _, err := safefile.Read(file, maxPointer)
		if err != nil {
			return "", err
		}
		v, ok := strings.CutPrefix(strings.TrimSpace(string(b)), prefix)
		if !ok {
			return "", fmt.Errorf("%s does not start with %q", file, prefix)
		}
		v = filepath.FromSlash(strings.TrimSpace(v))
		if !filepath.IsAbs(v) {
			v = filepath.Join(base, v)
		}
		return filepath.Clean(v), nil
	}
	gitdir, err := pointer(filepath.Join(rec.Dir, ".git"), "gitdir:", rec.Dir)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrWorktreeMoved, err)
	}
	common, err := pointer(filepath.Join(rec.GitDir, "commondir"), "", rec.GitDir)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrWorktreeMoved, err)
	}
	if lockKey(gitdir) != lockKey(rec.GitDir) || lockKey(common) != lockKey(rec.Common) {
		return fmt.Errorf("%w: %s points at git folder %s and common dir %s; the record says %s and %s",
			ErrWorktreeMoved, rec.Dir, gitdir, common, rec.GitDir, rec.Common)
	}
	return nil
}

// resolve asks git for the absolute git dir and common dir it sees.
func (g *Real) resolve(ctx context.Context, ro runOpts) (gitdir, common string, bare bool, err error) {
	out, err := g.run(ctx, ro, "rev-parse", "--path-format=absolute", "--git-dir", "--git-common-dir", "--is-bare-repository")
	if err != nil {
		if has(err, "not a git repository") {
			err = as(ErrNotARepo, err)
		}
		return "", "", false, err
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != 3 {
		return "", "", false, fmt.Errorf("unexpected rev-parse output %q", out)
	}
	clean := func(s string) string { return filepath.Clean(filepath.FromSlash(strings.TrimSpace(s))) }
	return clean(lines[0]), clean(lines[1]), strings.TrimSpace(lines[2]) == "true", nil
}

// checkTTL bounds how long a worktree that git resolved to its record is not
// asked again. Every call still runs verify (the .git file and the commondir
// read in process, no link followed), which is what an agent can change; the
// resolve by git adds what only changes with the repository itself, so the
// bound only limits that.
const checkTTL = 5 * time.Minute

// CheckWorktree compares what git resolves from the worktree itself (its
// .git file and commondir) with the record: a difference is ErrWorktreeMoved,
// which the engine raises as an alert.
func (g *Real) CheckWorktree(ctx context.Context, rec WorktreeRecord) error {
	return g.checkWorktree(ctx, rec)
}

// checkWorktree is CheckWorktree. verify runs every time; the process that
// resolves the worktree runs only when the record was not resolved in the
// last checkTTL or when a folder of the record is reached through a link
// (what only the realpath of git sees), and a failure forgets the record.
func (g *Real) checkWorktree(ctx context.Context, rec WorktreeRecord) error {
	if err := rec.check(); err != nil {
		return err
	}
	if err := rec.verify(); err != nil {
		g.forget(rec)
		return err
	}
	now := g.clk.Now()
	g.checkMu.Lock()
	at, ok := g.checked[rec]
	g.checkMu.Unlock()
	if age := now.Sub(at); ok && age >= 0 && age < checkTTL && rec.plain() {
		return nil
	}
	gitdir, common, bare, err := g.resolve(ctx, runOpts{dir: rec.Dir})
	if err != nil {
		g.forget(rec)
		return fmt.Errorf("check worktree %s: %w", rec.Dir, err)
	}
	if lockKey(gitdir) != lockKey(rec.GitDir) || lockKey(common) != lockKey(rec.Common) {
		g.forget(rec)
		return fmt.Errorf("%w: %s resolves to git folder %s and common dir %s; the record says %s and %s",
			ErrWorktreeMoved, rec.Dir, gitdir, common, rec.GitDir, rec.Common)
	}
	if bare {
		g.forget(rec)
		return fmt.Errorf("%w: %s is a bare repository", ErrWorktreeMoved, rec.Dir)
	}
	g.checkMu.Lock()
	for k, t := range g.checked { // forget the expired ones
		if age := now.Sub(t); age < 0 || age >= checkTTL {
			delete(g.checked, k)
		}
	}
	g.checked[rec] = now
	g.checkMu.Unlock()
	return nil
}

// plain reports whether the git folder and the common dir of the record are
// real folders, with no link in their path: only then the strings that
// verify compares are what git resolves.
func (rec WorktreeRecord) plain() bool {
	for _, p := range []string{rec.GitDir, rec.Common} {
		real, err := filepath.EvalSymlinks(p)
		if err != nil || lockKey(real) != lockKey(p) {
			return false
		}
	}
	return true
}

func (g *Real) forget(rec WorktreeRecord) {
	g.checkMu.Lock()
	delete(g.checked, rec)
	g.checkMu.Unlock()
}

// OpenWorktree opens the worktree of the record: every call is checked
// against it first (its .git file and commondir, in process) and runs pinned
// to it (GIT_DIR, GIT_COMMON_DIR, GIT_WORK_TREE), so an engine operation does
// not follow a .git file or a commondir an agent rewrote (ErrWorktreeMoved),
// and the lock is the one of the recorded common dir. A worktree that points
// elsewhere at the opening is ErrWorktreeMoved, not opened. At most one git
// process runs here (none while the record is in the cache of
// checkWorktree).
func (g *Real) OpenWorktree(ctx context.Context, rec WorktreeRecord) (Repo, error) {
	if err := g.checkWorktree(ctx, rec); err != nil {
		return nil, err
	}
	return &repo{g: g, dir: rec.Dir, common: rec.Common, rec: &rec}, nil
}
