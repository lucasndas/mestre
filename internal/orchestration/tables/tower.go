package tables

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/mestre-ia/mestre/internal/gitx"
	"github.com/mestre-ia/mestre/internal/protocol"
	"github.com/mestre-ia/mestre/internal/sessionzero"
	"github.com/mestre-ia/mestre/internal/store"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// TowerOptions are the pieces of the half of the tables that runs on a
// machine that is not the Citadel (and, with no mirror of its own to share, on
// the Citadel's other instances in the tests).
type TowerOptions struct {
	// DB is the machine database (local_worktrees).
	DB    *store.DB
	Git   Git
	Clock clock.Clock
	// Mirror opens the replica of the mirror of a workspace on this machine.
	Mirror func(ctx context.Context, workspace string) (gitx.Repo, error)
	// WorktreeDir is where the worktree of a table goes on this machine:
	// <dados>/workspaces/<slug>/mesas/<mesa>/.
	WorktreeDir func(workspace, table string) string
	// Files is the session zero of the tables on this machine.
	Files    Files
	Profiles Profiler
	// ExcludeMestre says whether .mestre/ stays out of git (default: yes).
	ExcludeMestre func(workspace string) bool
	// Send sends a bundle to the Citadel (protocol.Link.SendBundle).
	Send func(ctx context.Context, meta protocol.Object, src protocol.BundleSource) error
	// Tell tells the table master of a table, through the Citadel (the
	// divergence of a branch, a worktree that conflicts).
	Tell func(ctx context.Context, workspace, table, text string)
	// TempDir is a private folder for the bundles while they are sent.
	TempDir string
	Log     *slog.Logger
}

// Tower is the half of the tables on a machine: it receives the tip of the
// Citadel and brings its worktrees to it, and sends its commits to the
// Citadel after each `mestre done`.
type Tower struct {
	o   TowerOptions
	clk clock.Clock
	log *slog.Logger
	tmp string
}

var _ protocol.Finalizer = (*Tower)(nil)

// NewTower returns the half of a machine.
func NewTower(o TowerOptions) (*Tower, error) {
	switch {
	case o.DB == nil, o.Git == nil, o.Mirror == nil, o.WorktreeDir == nil, o.Files == nil, o.Send == nil, o.Tell == nil:
		return nil, errors.New("tables: the machine needs the database, git, the mirror, the folder of the worktrees, the session zero, the way to send and the way to tell")
	}
	if o.Clock == nil {
		o.Clock = clock.Real{}
	}
	log := o.Log
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	tmp, err := privateTemp(o.TempDir)
	if err != nil {
		return nil, fmt.Errorf("tables: %w", err)
	}
	return &Tower{o: o, clk: o.Clock, log: log, tmp: tmp}, nil
}

// local is a row of local_worktrees.
type local struct {
	Workspace, Table, Branch string
	Rec                      gitx.WorktreeRecord
	// CitadelCommit is the last commit of the branch the Citadel is known
	// to have: the prerequisite of the next incremental bundle.
	CitadelCommit string
}

func (t *Tower) get(ctx context.Context, workspace, table string) (local, bool, error) {
	var l local
	err := t.o.DB.Reader().QueryRowContext(ctx, `SELECT workspace, table_id, branch, dir, git_dir, common_dir, citadel_commit FROM local_worktrees WHERE workspace = ? AND table_id = ?`,
		workspace, table).Scan(&l.Workspace, &l.Table, &l.Branch, &l.Rec.Dir, &l.Rec.GitDir, &l.Rec.Common, &l.CitadelCommit)
	if errors.Is(err, sql.ErrNoRows) {
		return local{}, false, nil
	}
	return l, err == nil, err
}

func (t *Tower) put(ctx context.Context, l local) error {
	now := t.clk.Now().UnixMilli()
	return t.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO local_worktrees (workspace, table_id, branch, dir, git_dir, common_dir, citadel_commit, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (workspace, table_id) DO UPDATE SET branch = excluded.branch, dir = excluded.dir, git_dir = excluded.git_dir, common_dir = excluded.common_dir,
				citadel_commit = excluded.citadel_commit, updated_at = excluded.updated_at`,
			l.Workspace, l.Table, l.Branch, l.Rec.Dir, l.Rec.GitDir, l.Rec.Common, l.CitadelCommit, now, now)
		return err
	})
}

func (t *Tower) setCitadelCommit(ctx context.Context, workspace, table, commit string) error {
	return t.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE local_worktrees SET citadel_commit = ?, updated_at = ? WHERE workspace = ? AND table_id = ?`,
			commit, t.clk.Now().UnixMilli(), workspace, table)
		return err
	})
}

func (t *Tower) all(ctx context.Context) ([]local, error) {
	rows, err := t.o.DB.Reader().QueryContext(ctx, `SELECT workspace, table_id, branch, dir, git_dir, common_dir, citadel_commit FROM local_worktrees ORDER BY workspace, table_id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []local
	for rows.Next() {
		var l local
		if err := rows.Scan(&l.Workspace, &l.Table, &l.Branch, &l.Rec.Dir, &l.Rec.GitDir, &l.Rec.Common, &l.CitadelCommit); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// Finalize receives a bundle from the Citadel: the tip of the branch of the
// table arrives as refs/mestre/cidadela/<branch>; the first one makes the
// worktree, the next ones fast-forward it (never anything else: a branch of
// this machine that diverged stays, and the table master is told).
func (t *Tower) Finalize(ctx context.Context, b protocol.XferBegin, file string) error {
	m, err := MetaOf(b.Meta)
	if err != nil {
		return protocol.NewError(protocol.CodeInvalidPayload, "%v", err)
	}
	return protocol.BundleReceiver{
		Repo: func(ctx context.Context, _ protocol.Object) (protocol.BundleRepo, error) {
			return t.o.Mirror(ctx, m.Workspace)
		},
		Apply: func(ctx context.Context, repo protocol.BundleRepo, _ protocol.Object, f string) error {
			return t.apply(ctx, repo.(gitx.Repo), m, f)
		},
	}.Finalize(ctx, b, file)
}

func (t *Tower) apply(ctx context.Context, mirror gitx.Repo, m BundleMeta, file string) error {
	ref := CitadelRef(m.Branch)
	if err := mirror.FetchBundle(ctx, file, "+refs/heads/"+m.Branch+":"+ref); err != nil {
		return protocol.NewError(protocol.CodeInternal, "importing the bundle: %v", err)
	}
	tip, err := mirror.ResolveRef(ctx, ref)
	if err != nil {
		return err
	}
	l, ok, err := t.get(ctx, m.Workspace, m.Table)
	if err != nil {
		return err
	}
	if !ok {
		return t.firstWorktree(ctx, mirror, m, tip)
	}
	repo, err := t.o.Git.OpenWorktree(ctx, l.Rec)
	if err != nil {
		return err
	}
	cur, err := mirror.ResolveRef(ctx, "refs/heads/"+l.Branch)
	if err != nil {
		return err
	}
	switch {
	case cur == tip:
	case t.isAncestor(ctx, mirror, cur, tip):
		req, err := t.request(ctx, l, repo)
		if err != nil {
			return err
		}
		err = t.o.Files.Sync(ctx, req, func(ctx context.Context) error {
			// The checked-out branch is the one that moves: it must be the table's.
			if err := checkHead(ctx, repo, l.Branch); err != nil {
				return err
			}
			_, err := repo.MergeFFOnly(ctx, ref)
			return err
		})
		if errors.Is(err, errWrongBranch) {
			t.o.Tell(ctx, m.Workspace, m.Table, fmt.Sprintf("A Torre não pôde trazer a ponta da Cidadela: o worktree não está na branch %s (%v). Nada mudou.", l.Branch, err))
		} else if errors.Is(err, gitx.ErrLocalChanges) || errors.Is(err, gitx.ErrNotFastForward) {
			t.o.Tell(ctx, m.Workspace, m.Table, fmt.Sprintf("A Torre não pôde trazer a ponta da Cidadela para a branch %s: o worktree tem alterações que conflitam. Nada mudou.", l.Branch))
		} else if err != nil {
			return err
		}
	case t.isAncestor(ctx, mirror, tip, cur):
		// This machine is ahead of the Citadel: its commits go in the next done.
	default:
		t.o.Tell(ctx, m.Workspace, m.Table, fmt.Sprintf("A branch %s desta Torre divergiu da ponta da Cidadela (%s); a local foi mantida. "+
			"Um personagem daqui resolve com `git rebase` ou `git merge` da ref %s e sinaliza `done`.", l.Branch, tip.Short(), ref))
	}
	return t.setCitadelCommit(ctx, m.Workspace, m.Table, string(tip))
}

func (t *Tower) isAncestor(ctx context.Context, repo gitx.Repo, anc, desc gitx.Hash) bool {
	ok, err := repo.IsAncestor(ctx, string(anc), string(desc))
	return err == nil && ok
}

// firstWorktree makes the worktree of a table on this machine from the tip of
// the Citadel, and runs the session zero of the table in it.
func (t *Tower) firstWorktree(ctx context.Context, mirror gitx.Repo, m BundleMeta, tip gitx.Hash) error {
	cur, err := mirror.ResolveRef(ctx, "refs/heads/"+m.Branch)
	switch {
	case errors.Is(err, gitx.ErrRefNotFound):
		if err := mirror.CreateBranch(ctx, m.Branch, string(tip)); err != nil {
			return err
		}
	case err != nil:
		return err
	case cur == tip:
	default:
		// The replica already has a branch with this name that is not the
		// tip the Citadel sent (a ref planted by an agent, or what is left
		// of an older table): the worktree of a table starts at the tip of
		// the Citadel, never at what is there. Moving it forward loses
		// nothing; anything else is refused.
		if err := t.resetBranch(ctx, mirror, m, cur, tip); err != nil {
			return err
		}
	}
	dir := t.o.WorktreeDir(m.Workspace, m.Table)
	if _, err := mirror.AddWorktree(ctx, dir, gitx.WorktreeOptions{Branch: m.Branch}); err != nil {
		return fmt.Errorf("the worktree of the table was not created: %w", err)
	}
	rec, err := t.o.Git.Record(ctx, dir)
	if err != nil {
		return err
	}
	l := local{Workspace: m.Workspace, Table: m.Table, Branch: m.Branch, Rec: rec, CitadelCommit: string(tip)}
	if err := t.put(ctx, l); err != nil {
		return err
	}
	repo, err := t.o.Git.OpenWorktree(ctx, rec)
	if err != nil {
		return err
	}
	req, err := t.request(ctx, l, repo)
	if err != nil {
		return err
	}
	if _, err := t.o.Files.Prepare(ctx, req); err != nil {
		return fmt.Errorf("the session zero of the table: %w", err)
	}
	return nil
}

// resetBranch takes a branch of the replica that is not at the tip of the
// Citadel to that tip, when that only moves it forward and no worktree has it
// checked out.
func (t *Tower) resetBranch(ctx context.Context, mirror gitx.Repo, m BundleMeta, cur, tip gitx.Hash) error {
	refuse := func(why string) error {
		t.o.Tell(ctx, m.Workspace, m.Table, fmt.Sprintf("A réplica desta Torre já tem a branch %s em %s, que não é a ponta da Cidadela (%s): %s. O worktree da mesa não foi criado.",
			m.Branch, cur.Short(), tip.Short(), why))
		return fmt.Errorf("the replica has the branch %s at %s, not at the tip of the Citadel (%s): %s", m.Branch, cur.Short(), tip.Short(), why)
	}
	if !t.isAncestor(ctx, mirror, cur, tip) {
		return refuse("ela tem commits que a Cidadela não tem")
	}
	wts, err := mirror.ListWorktrees(ctx)
	if err != nil {
		return err
	}
	for _, w := range wts {
		if w.Branch == m.Branch {
			return refuse("ela está em uso em " + w.Path)
		}
	}
	return mirror.UpdateRef(ctx, "refs/heads/"+m.Branch, tip)
}

func (t *Tower) request(ctx context.Context, l local, repo gitx.Repo) (sessionzero.TableRequest, error) {
	req := sessionzero.TableRequest{Workspace: l.Workspace, Table: l.Table, Dir: l.Rec.Dir, Repo: repo, RepoRoot: l.Rec.Common, ExcludeMestre: true}
	if t.o.ExcludeMestre != nil {
		req.ExcludeMestre = t.o.ExcludeMestre(l.Workspace)
	}
	if t.o.Profiles != nil {
		req.Profiles = t.o.Profiles.Profiles(ctx, "")
	}
	return req, nil
}

// DoneReport is what the sending after a `done` tells.
type DoneReport struct {
	// Sent is whether a bundle went to the Citadel (false: it already had
	// everything).
	Sent bool
	Tip  string
	// Warnings go to the result of the done: the changes that were not
	// committed did not travel.
	Warnings []string
}

// Done sends the commits of the branch of the table to the Citadel, after the
// `mestre done` of a character on this machine: an incremental bundle from the
// last commit the Citadel is known to have. Uncommitted changes do not
// travel; the report lists their files. A Citadel that cannot be reached
// leaves the sending pending: Resend does it again when the connection is
// back.
func (t *Tower) Done(ctx context.Context, workspace, table string) (DoneReport, error) {
	l, ok, err := t.get(ctx, workspace, table)
	if err != nil {
		return DoneReport{}, err
	}
	if !ok {
		return DoneReport{}, fmt.Errorf("%w: %s", ErrNoWorktree, table)
	}
	repo, err := t.o.Git.OpenWorktree(ctx, l.Rec)
	if err != nil {
		return DoneReport{}, err
	}
	var rep DoneReport
	if st, err := repo.Status(ctx); err != nil {
		return rep, err
	} else if len(st) > 0 {
		files := make([]string, 0, len(st))
		for _, e := range st {
			files = append(files, e.Path)
		}
		rep.Warnings = append(rep.Warnings, "alterações não commitadas não foram sincronizadas: "+strings.Join(files, ", "))
	}
	mirror, err := t.o.Mirror(ctx, workspace)
	if err != nil {
		return rep, err
	}
	tip, err := mirror.ResolveRef(ctx, "refs/heads/"+l.Branch)
	if err != nil {
		return rep, err
	}
	rep.Tip = string(tip)
	if l.CitadelCommit == string(tip) {
		return rep, nil
	}
	base := ""
	if l.CitadelCommit != "" && t.isAncestor(ctx, mirror, gitx.Hash(l.CitadelCommit), tip) {
		base = l.CitadelCommit
	}
	meta := BundleMeta{Workspace: workspace, Table: table, Branch: l.Branch, Tip: string(tip), Base: base}
	src := bundleSources(mirror, t.tmp, l.Branch, base)
	if err := SendBundleWithRetry(ctx, func(ctx context.Context) error { return t.o.Send(ctx, meta.Object(), src) }); err != nil {
		return rep, fmt.Errorf("sending %s to the Citadel: %w", l.Branch, err)
	}
	rep.Sent = true
	return rep, t.setCitadelCommit(ctx, workspace, table, string(tip))
}

// Resend does again the sending of every table whose branch has commits the
// Citadel does not know: after the connection came back.
func (t *Tower) Resend(ctx context.Context) error {
	ls, err := t.all(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, l := range ls {
		if _, err := t.Done(ctx, l.Workspace, l.Table); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Inspect is the state of the worktree of a table on this machine.
func (t *Tower) Inspect(ctx context.Context, workspace, table string) (Inspection, error) {
	l, ok, err := t.get(ctx, workspace, table)
	if err != nil {
		return Inspection{}, err
	}
	if !ok {
		return Inspection{}, fmt.Errorf("%w: %s", ErrNoWorktree, table)
	}
	repo, err := t.o.Git.OpenWorktree(ctx, l.Rec)
	if err != nil {
		return Inspection{}, err
	}
	tip, err := repo.ResolveRef(ctx, l.Branch)
	if err != nil {
		return Inspection{}, err
	}
	st, err := repo.Status(ctx)
	if err != nil {
		return Inspection{}, err
	}
	in := Inspection{Tip: string(tip)}
	for _, e := range st {
		in.Dirty = append(in.Dirty, e.Path)
	}
	return in, nil
}

// RemoveWorktree removes the worktree of a table on this machine, and the
// trust it gave in the account profiles.
func (t *Tower) RemoveWorktree(ctx context.Context, workspace, table string) error {
	l, ok, err := t.get(ctx, workspace, table)
	if err != nil || !ok {
		return err
	}
	mirror, err := t.o.Mirror(ctx, workspace)
	if err != nil {
		return err
	}
	repo, err := t.o.Git.OpenWorktree(ctx, l.Rec)
	if err != nil {
		return err
	}
	req, err := t.request(ctx, l, repo)
	if err != nil {
		return err
	}
	if err := t.o.Files.Release(req); err != nil {
		t.log.Warn("tables: the trust of the worktree was not taken back", "table", table, "err", err)
	}
	if err := mirror.RemoveWorktree(ctx, l.Rec.Dir, false); err != nil {
		return err
	}
	// The tip of the Citadel the bundles brought is of no use any more.
	if err := mirror.DeleteRef(ctx, CitadelRef(l.Branch)); err != nil {
		t.log.Warn("tables: the ref of the Citadel was not removed from the replica", "table", table, "err", err)
	}
	return t.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM local_worktrees WHERE workspace = ? AND table_id = ?`, workspace, table)
		return err
	})
}
