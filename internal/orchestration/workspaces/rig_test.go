package workspaces_test

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/gitx"
	"github.com/mestre-ia/mestre/internal/orchestration/workspaces"
	"github.com/mestre-ia/mestre/internal/store"
	"github.com/mestre-ia/mestre/internal/testkit/fakegit"
	"github.com/mestre-ia/mestre/internal/testkit/guildtpl"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// t0 is the instant every test starts at.
var t0 = time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)

// rig is a Service over the fake git, a fake clock, the Guild database and
// fakes for GitHub, the alerts and the Grand Master.
type rig struct {
	t      testing.TB
	ctx    context.Context
	clk    *clock.Fake
	db     *store.DB
	git    *fakegit.Git
	remote *fakegit.Handle
	root   string
	svc    *workspaces.Service

	mu         sync.Mutex
	fetches    int
	fetchTimes []time.Time
	fetchErr   error
	cloneErr   error
	moved      bool
	merges     []string
	alertsSent []workspaces.Alert
	alertErr   error
	gmState    workspaces.GrandMasterState
	gmItems    []string
	fetched    chan struct{}
}

func newRig(t testing.TB) *rig {
	t.Helper()
	r := &rig{t: t, ctx: t.Context(), clk: clock.NewFake(t0), git: fakegit.NewGit(nil), root: t.TempDir(), gmState: workspaces.GMIdle,
		fetched: make(chan struct{}, 64)}
	r.git = fakegit.NewGit(r.clk)
	remote, err := r.git.Init(r.ctx, filepath.Join(r.root, "remote", "meu-app"), gitx.InitOptions{})
	if err != nil {
		t.Fatal(err)
	}
	r.remote = r.git.Handle(remote.Dir())
	r.remote.Data().Commit("main", "chore: initial commit", map[string]string{"README.md": "# Meu App\n"})
	r.db = r.openDB()
	r.svc = r.newService(r.db)
	return r
}

func (r *rig) openDB() *store.DB {
	r.t.Helper()
	db, err := guildtpl.Open(r.ctx, filepath.Join(r.root, "mestre.db"), store.Options{Synchronous: "OFF", Clock: r.clk})
	if err != nil {
		r.t.Fatal(err)
	}
	r.t.Cleanup(func() { _ = db.Close() })
	return db
}

func (r *rig) newService(db *store.DB) *workspaces.Service {
	r.t.Helper()
	svc, err := workspaces.New(workspaces.Options{
		DB: db, Git: &spyGit{Git: r.git, r: r}, Layout: workspaces.Layout{Root: filepath.Join(r.root, "workspaces")},
		Clock: r.clk, GitHub: (*ghFake)(r), Alerts: (*alertFake)(r), GrandMaster: (*gmFake)(r),
	})
	if err != nil {
		r.t.Fatal(err)
	}
	return svc
}

// stageLog collects the stages that ran, in order.
type stageLog struct {
	mu    sync.Mutex
	stage []workspaces.Stage
}

func (l *stageLog) list() []workspaces.Stage {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]workspaces.Stage(nil), l.stage...)
}

// registerStages gives the stages of the session zero other than the mirror a
// runner that only records itself.
func (r *rig) registerStages(svc *workspaces.Service, log *stageLog, fail map[workspaces.Stage]error) {
	r.t.Helper()
	for _, st := range workspaces.Stages {
		if st == workspaces.StageMirror {
			continue
		}
		if err := svc.Register(st, func(_ context.Context, w workspaces.Workspace) error {
			log.mu.Lock()
			defer log.mu.Unlock()
			log.stage = append(log.stage, w.Stage)
			return fail[w.Stage]
		}); err != nil {
			r.t.Fatal(err)
		}
	}
}

// active creates the workspace lucas/Meu_App and walks it to active.
func (r *rig) active() workspaces.Workspace {
	r.t.Helper()
	r.registerStages(r.svc, &stageLog{}, nil)
	w, err := r.svc.Create(r.ctx, workspaces.CreateRequest{Repo: "lucas/Meu_App"})
	if err != nil {
		r.t.Fatal(err)
	}
	if err := r.svc.Prepare(r.ctx, w.Slug); err != nil {
		r.t.Fatal(err)
	}
	return r.get(w.Slug)
}

func (r *rig) get(slug string) workspaces.Workspace {
	r.t.Helper()
	w, err := r.svc.Get(r.ctx, slug)
	if err != nil {
		r.t.Fatal(err)
	}
	return w
}

// push makes a new commit on the main branch of the remote.
func (r *rig) push(msg string, files map[string]string) gitx.Hash {
	return r.remote.Data().Commit("main", msg, files)
}

// principal is the main clone of the workspace.
func (r *rig) principal(slug string) *fakegit.Handle {
	r.t.Helper()
	h := r.git.Handle(r.svc.Layout().Principal(slug))
	if h == nil {
		r.t.Fatalf("workspace %s has no main clone", slug)
	}
	return h
}

func (r *rig) head(slug string) gitx.Hash {
	r.t.Helper()
	h, err := r.principal(slug).ResolveRef(r.ctx, "HEAD")
	if err != nil {
		r.t.Fatal(err)
	}
	return h
}

func (r *rig) fetchCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.fetches
}

// attempts are the instants of the fetches asked of the mirror.
func (r *rig) attempts() []time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]time.Time(nil), r.fetchTimes...)
}

func (r *rig) setFetchErr(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.fetchErr = err
}

func (r *rig) setGM(s workspaces.GrandMasterState) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.gmState = s
}

func (r *rig) alerts() []workspaces.Alert {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]workspaces.Alert(nil), r.alertsSent...)
}

func (r *rig) items() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.gmItems...)
}

func (r *rig) mergeRefs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.merges...)
}

// runUntil does what the engine does between two instants: tick, and move the
// clock to the next thing that is due, until the clock reaches target.
func (r *rig) runUntil(svc *workspaces.Service, target time.Time) {
	r.t.Helper()
	stuck := 0
	for range 5000 {
		next, ok, err := svc.Tick(r.ctx)
		if err != nil {
			r.t.Fatal(err)
		}
		now := r.clk.Now()
		if !ok || next.After(target) {
			r.clk.Advance(target.Sub(now))
			if _, _, err := svc.Tick(r.ctx); err != nil {
				r.t.Fatal(err)
			}
			return
		}
		if !next.After(now) {
			// Due right now although Tick just did what was due: tolerate a
			// few, then call it a loop that does nothing.
			if stuck++; stuck > 5 {
				r.t.Fatalf("Tick keeps returning %v at %v", next, now)
			}
			continue
		}
		stuck = 0
		r.clk.Advance(next.Sub(now))
	}
	r.t.Fatal("runUntil did not reach the target")
}

// spyGit wraps the fake git so a test can make the clone and the fetch fail,
// see what the engine asks of the main clone and make it look moved.
type spyGit struct {
	*fakegit.Git
	r *rig
}

var _ workspaces.Git = (*spyGit)(nil)

func (g *spyGit) wrap(repo gitx.Repo, err error) (gitx.Repo, error) {
	if err != nil {
		return nil, err
	}
	return spyRepo{Repo: repo, r: g.r}, nil
}

func (g *spyGit) Clone(ctx context.Context, url, dir string, opts gitx.CloneOptions) (gitx.Repo, error) {
	g.r.mu.Lock()
	err := g.r.cloneErr
	g.r.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return g.wrap(g.Git.Clone(ctx, url, dir, opts))
}

func (g *spyGit) Open(ctx context.Context, dir string) (gitx.Repo, error) {
	return g.wrap(g.Git.Open(ctx, dir))
}

func (g *spyGit) OpenWorktree(ctx context.Context, rec gitx.WorktreeRecord) (gitx.Repo, error) {
	g.r.mu.Lock()
	moved := g.r.moved
	g.r.mu.Unlock()
	if moved {
		return nil, errors.Join(gitx.ErrWorktreeMoved, errors.New("the .git file points elsewhere"))
	}
	return g.wrap(g.Git.OpenWorktree(ctx, rec))
}

type spyRepo struct {
	gitx.Repo
	r *rig
}

func (s spyRepo) noteFetch() error {
	s.r.mu.Lock()
	s.r.fetches++
	s.r.fetchTimes = append(s.r.fetchTimes, s.r.clk.Now())
	err := s.r.fetchErr
	s.r.mu.Unlock()
	select {
	case s.r.fetched <- struct{}{}:
	default:
	}
	return err
}

func (s spyRepo) Fetch(ctx context.Context, remote string, refspecs ...string) error {
	if err := s.noteFetch(); err != nil {
		return err
	}
	return s.Repo.Fetch(ctx, remote, refspecs...)
}

func (s spyRepo) FetchWithAuth(ctx context.Context, auth gitx.Auth, remote string, refspecs ...string) error {
	if err := s.noteFetch(); err != nil {
		return err
	}
	return s.Repo.FetchWithAuth(ctx, auth, remote, refspecs...)
}

func (s spyRepo) MergeFFOnly(ctx context.Context, ref string) (gitx.Hash, error) {
	s.r.mu.Lock()
	s.r.merges = append(s.r.merges, ref)
	s.r.mu.Unlock()
	return s.Repo.MergeFFOnly(ctx, ref)
}

// ghFake answers for the repository lucas/Meu_App, whose clone URL is the
// folder of the fake remote.
type ghFake rig

func (g *ghFake) Repository(_ context.Context, repo workspaces.Repo) (workspaces.RepoInfo, error) {
	return workspaces.RepoInfo{FullName: repo.String(), CloneURL: g.remote.Dir(), DefaultBranch: "main"}, nil
}

type alertFake rig

func (a *alertFake) Operational(_ context.Context, al workspaces.Alert) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.alertErr != nil {
		return a.alertErr
	}
	a.alertsSent = append(a.alertsSent, al)
	return nil
}

type gmFake rig

func (g *gmFake) State(context.Context, string) (workspaces.GrandMasterState, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.gmState, nil
}

func (g *gmFake) NotifySystem(_ context.Context, _, text string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.gmItems = append(g.gmItems, text)
	return nil
}
