package tables_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/gitx"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/connections"
	"github.com/lucasndas/mestre/internal/orchestration/inbox"
	"github.com/lucasndas/mestre/internal/orchestration/tables"
	"github.com/lucasndas/mestre/internal/orchestration/workspaces"
	"github.com/lucasndas/mestre/internal/protocol"
	"github.com/lucasndas/mestre/internal/replication"
	"github.com/lucasndas/mestre/internal/sessionzero"
	"github.com/lucasndas/mestre/internal/store"
	guilddb "github.com/lucasndas/mestre/internal/store/guild"
	machinedb "github.com/lucasndas/mestre/internal/store/machine"
	"github.com/lucasndas/mestre/internal/testkit/fakegit"
	"github.com/lucasndas/mestre/pkg/clock"
)

var day0 = time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)

// The databases are migrated once, by TestMain; every test starts from a copy
// (docs/qualidade/testes.md).
var guildTemplate, machineTemplate []byte

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "tables-template")
	if err != nil {
		fmt.Fprintln(os.Stderr, "tables:", err)
		os.Exit(1)
	}
	for _, t := range []struct {
		name string
		open func(context.Context, string, store.Options) (*store.DB, error)
		into *[]byte
	}{{"mestre.db", guilddb.Open, &guildTemplate}, {"maquina.db", machinedb.Open, &machineTemplate}} {
		path := filepath.Join(dir, t.name)
		db, err := t.open(context.Background(), path, store.Options{Synchronous: "OFF"})
		if err == nil {
			err = db.Close()
		}
		if err == nil {
			*t.into, err = os.ReadFile(path)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "tables: template:", err)
			os.Exit(1)
		}
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func openCopy(t *testing.T, name string, template []byte, open func(context.Context, string, store.Options) (*store.DB, error), clk clock.Clock) *store.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, template, 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := open(context.Background(), path, store.Options{Synchronous: "OFF", Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// backend is the git of a machine of the tests: the fake one in the fast
// suite, the real one in the complete suite.
type backend interface {
	// newGit is the git of a machine (each machine has its own).
	newGit(t *testing.T, clk clock.Clock) tables.Git
	// initRepo makes a repository with one commit on main.
	initRepo(t *testing.T, g tables.Git, dir string, files map[string]string) gitx.Repo
	// write puts a file in the folder of a worktree (or of a repository).
	write(t *testing.T, g tables.Git, dir, rel, content string)
}

type fakeBackend struct{}

func (fakeBackend) newGit(_ *testing.T, clk clock.Clock) tables.Git { return fakegit.NewGit(clk) }

func (fakeBackend) initRepo(t *testing.T, g tables.Git, dir string, files map[string]string) gitx.Repo {
	t.Helper()
	repo, err := g.Init(t.Context(), dir, gitx.InitOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if u, ok := g.(*spyGit); ok {
		g = u.Git
	}
	g.(*fakegit.Git).Handle(repo.Dir()).Data().Commit("main", "chore: initial commit", files)
	return repo
}

func (fakeBackend) write(t *testing.T, g tables.Git, dir, rel, content string) {
	t.Helper()
	if u, ok := g.(*spyGit); ok {
		g = u.Git
	}
	h := g.(*fakegit.Git).Handle(dir)
	if h == nil {
		t.Fatalf("no repository at %s", dir)
	}
	h.SetFile(rel, content)
}

// spyGit wraps the git of the Citadel to see what the engine asks of the
// repositories: the worktrees it removes and the branches it deletes.
type spyGit struct {
	tables.Git
	mu      sync.Mutex
	removed []string
	deleted []string
	// busy makes the removal of a worktree fail as it does on Windows with an
	// open file.
	busy bool
	// failOpen is the number of the call of OpenWorktree that fails (1 is the
	// first); zero fails none.
	failOpen, opens int
}

func (g *spyGit) wrap(r gitx.Repo, err error) (gitx.Repo, error) {
	if err != nil {
		return nil, err
	}
	return spyRepo{Repo: r, g: g}, nil
}

func (g *spyGit) Open(ctx context.Context, dir string) (gitx.Repo, error) {
	return g.wrap(g.Git.Open(ctx, dir))
}

func (g *spyGit) Clone(ctx context.Context, url, dir string, opts gitx.CloneOptions) (gitx.Repo, error) {
	return g.wrap(g.Git.Clone(ctx, url, dir, opts))
}

func (g *spyGit) OpenWorktree(ctx context.Context, rec gitx.WorktreeRecord) (gitx.Repo, error) {
	g.mu.Lock()
	g.opens++
	fail := g.failOpen != 0 && g.opens == g.failOpen
	g.mu.Unlock()
	if fail {
		return nil, fmt.Errorf("the worktree cannot be opened")
	}
	return g.wrap(g.Git.OpenWorktree(ctx, rec))
}

func (g *spyGit) asked() (removed, deleted []string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Clone(g.removed), slices.Clone(g.deleted)
}

type spyRepo struct {
	gitx.Repo
	g *spyGit
}

func (r spyRepo) RemoveWorktree(ctx context.Context, path string, force bool) error {
	r.g.mu.Lock()
	r.g.removed = append(r.g.removed, path)
	busy := r.g.busy
	r.g.mu.Unlock()
	if busy {
		return fmt.Errorf("remove worktree %s: %w", path, gitx.ErrWorktreeBusy)
	}
	return r.Repo.RemoveWorktree(ctx, path, force)
}

func (r spyRepo) DeleteBranch(ctx context.Context, name string) error {
	r.g.mu.Lock()
	r.g.deleted = append(r.g.deleted, name)
	r.g.mu.Unlock()
	return r.Repo.DeleteBranch(ctx, name)
}

// --- fakes of the pieces of other stages ---

type fakeGitHub struct {
	mu       sync.Mutex
	epics    map[int]tables.Epic
	created  int
	closedPR []int
	closedEp []int
	err      error
}

func (g *fakeGitHub) Epic(_ context.Context, _ string, n int) (tables.Epic, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.err != nil {
		return tables.Epic{}, g.err
	}
	e, ok := g.epics[n]
	if !ok {
		return tables.Epic{}, fmt.Errorf("%w: #%d", tables.ErrEpicNotFound, n)
	}
	return e, nil
}

func (g *fakeGitHub) CloseEpic(_ context.Context, _ string, n int) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.closedEp = append(g.closedEp, n)
	return nil
}

func (g *fakeGitHub) ClosePR(_ context.Context, _ string, n int) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.closedPR = append(g.closedPR, n)
	return nil
}

func (g *fakeGitHub) add(n int, title string, labels ...string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.epics[n] = tables.Epic{Number: n, Title: title, Labels: labels, Open: true}
}

func (g *fakeGitHub) setBody(n int, body string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	e := g.epics[n]
	e.Body = body
	g.epics[n] = e
}

type fakeMessages struct {
	mu   sync.Mutex
	msgs map[string]tables.Message
}

func (f *fakeMessages) Message(_ context.Context, _, id string) (tables.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, ok := f.msgs[id]
	if !ok {
		return tables.Message{}, fmt.Errorf("no message %s", id)
	}
	return m, nil
}

type fakeAttachments struct {
	mu  sync.Mutex
	got map[string][]sessionzero.File
}

func (f *fakeAttachments) Forward(_ context.Context, _, table string, files []sessionzero.File) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.got[table] = files
	return nil
}

// fakeFiles is the session zero of the tables: it keeps what was asked, and
// runs the sync function it is given.
type fakeFiles struct {
	mu       sync.Mutex
	prepared []string
	released []string
	synced   []string
	err      error
}

func (f *fakeFiles) Prepare(_ context.Context, r sessionzero.TableRequest) (sessionzero.TableReport, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return sessionzero.TableReport{}, f.err
	}
	f.prepared = append(f.prepared, r.Dir)
	return sessionzero.TableReport{}, nil
}

func (f *fakeFiles) Release(r sessionzero.TableRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.released = append(f.released, r.Dir)
	return nil
}

func (f *fakeFiles) Sync(ctx context.Context, r sessionzero.TableRequest, fn func(context.Context) error) error {
	f.mu.Lock()
	f.synced = append(f.synced, r.Dir)
	f.mu.Unlock()
	return fn(ctx)
}

func (f *fakeFiles) list(of *[]string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(*of)
}

type noticeLog struct {
	mu sync.Mutex
	ns []characters.Notice
}

func (n *noticeLog) Notify(_ context.Context, x characters.Notice) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.ns = append(n.ns, x)
	return nil
}

func (n *noticeLog) texts(to characters.RecipientKind) []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	var out []string
	for _, x := range n.ns {
		if x.To.Kind == to {
			out = append(out, x.Text)
		}
	}
	return out
}

type alertLog struct {
	mu sync.Mutex
	as []workspaces.Alert
}

func (a *alertLog) Operational(_ context.Context, al workspaces.Alert) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.as = append(a.as, al)
	return nil
}

func (a *alertLog) all() []workspaces.Alert {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.as)
}

type fakeDestructive struct {
	mu      sync.Mutex
	allowed bool
	asked   []tables.DestructiveAction
}

func (f *fakeDestructive) Allowed(_ context.Context, a tables.DestructiveAction) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, a)
	return f.allowed, nil
}

type fakeRituals struct {
	mu          sync.Mutex
	deactivated []string
}

func (f *fakeRituals) Deactivate(_ context.Context, _, table string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deactivated = append(f.deactivated, table)
	return nil
}

type fakeRequests struct {
	mu        sync.Mutex
	cancelled []string
}

func (f *fakeRequests) CancelTable(_ context.Context, _, table string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancelled = append(f.cancelled, table)
	return nil
}

// workspace fakes (GitHub of the workspace, alerts, Grand Master).

type wsGitHub struct{ url string }

func (g wsGitHub) Repository(_ context.Context, repo workspaces.Repo) (workspaces.RepoInfo, error) {
	return workspaces.RepoInfo{FullName: repo.String(), CloneURL: g.url, DefaultBranch: "main"}, nil
}

type wsGM struct{}

func (wsGM) State(context.Context, string) (workspaces.GrandMasterState, error) {
	return workspaces.GMIdle, nil
}
func (wsGM) NotifySystem(context.Context, string, string) error { return nil }

// fakeChars is the Service of the characters: it writes the rows of the
// registry like the real one, without the machines.
type fakeChars struct {
	w *world

	mu        sync.Mutex
	n         int
	creates   []characters.CreateRequest
	dismisses []characters.DismissRequest
	// schedule makes Dismiss answer "scheduled" for a character: it ends only
	// when the test calls finish.
	schedule  map[string]bool
	createErr error
}

func (f *fakeChars) Create(ctx context.Context, req characters.CreateRequest) (characters.CreateResult, error) {
	f.mu.Lock()
	f.n++
	n := f.n
	err := f.createErr
	f.creates = append(f.creates, req)
	f.mu.Unlock()
	if err != nil {
		return characters.CreateResult{}, err
	}
	name := req.Name
	if name == "" {
		name = characters.NameTableMaster
		if req.Role == characters.RoleCharacter {
			name = fmt.Sprintf("Dev %d", n)
		}
	}
	c := characters.Character{ID: fmt.Sprintf("c%d", n), Name: name, Kind: characters.KindCharacter, Role: req.Role, Workspace: req.Workspace, Table: req.Table,
		Machine: f.w.machine, Sheet: req.Sheet, Base: "claude-code", State: characters.Idle, TerminalID: fmt.Sprintf("t%d", n), Lifecycle: characters.Persistent,
		CreatedBy: req.Creator, CreatedAt: f.w.clk.Now(), StartedAt: f.w.clk.Now(), LastSignalAt: f.w.clk.Now()}
	if err := f.w.reg.Insert(ctx, c); err != nil {
		return characters.CreateResult{}, err
	}
	if err := f.w.conns.Born(ctx, c); err != nil {
		return characters.CreateResult{}, err
	}
	return characters.CreateResult{Character: c}, nil
}

func (f *fakeChars) Dismiss(ctx context.Context, req characters.DismissRequest) (characters.DismissResult, error) {
	f.mu.Lock()
	f.dismisses = append(f.dismisses, req)
	sched := f.schedule[req.ID]
	f.mu.Unlock()
	c, err := f.w.reg.Get(ctx, req.ID)
	if err != nil {
		return characters.DismissResult{}, err
	}
	if c.Pinned {
		return characters.DismissResult{}, fmt.Errorf("%w: %s", characters.ErrPinned, c.Name)
	}
	if sched {
		return characters.DismissResult{Status: characters.Scheduled, Character: c}, nil
	}
	if err := f.finish(ctx, c.ID); err != nil {
		return characters.DismissResult{}, err
	}
	return characters.DismissResult{Status: characters.Dismissed, Character: c}, nil
}

// finish ends a character, as its machine reporting it stopped.
func (f *fakeChars) finish(ctx context.Context, id string) error {
	_, _, err := f.w.reg.Transition(ctx, id, characters.Stopped, characters.Change{Reason: "dismiss", At: f.w.clk.Now()})
	return err
}

func (f *fakeChars) dismissed() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, d := range f.dismisses {
		out = append(out, d.ID)
	}
	return out
}

// fakeDeliverer is the machines for the inbox: it records what is typed and,
// like the runtime, puts the character to work.
type fakeDeliverer struct {
	w   *world
	mu  sync.Mutex
	got []protocol.InboxDeliver
}

func (f *fakeDeliverer) Deliver(ctx context.Context, _ string, d protocol.InboxDeliver) (protocol.InboxResult, error) {
	f.mu.Lock()
	f.got = append(f.got, d)
	f.mu.Unlock()
	for _, c := range f.w.aliveAll() {
		if c.TerminalID == d.TerminalID {
			from, changed, err := f.w.reg.Transition(ctx, c.ID, characters.Busy, characters.Change{Reason: "turn", At: f.w.clk.Now()})
			if err == nil && changed {
				c.State = characters.Busy
				_ = f.w.svc.StateChanged(ctx, c, from)
			}
		}
	}
	_ = f.w.inb.Confirmed(ctx, d.ItemID) // the turn began
	return protocol.InboxResult{ItemID: d.ItemID, Status: protocol.InboxDelivered}, nil
}

func (f *fakeDeliverer) typed() []protocol.InboxDeliver {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.got)
}

// lazyPauses breaks the loop between the inbox, which asks if a table is
// paused, and the tables, which put items in the inbox.
type lazyPauses struct{ w *world }

func (l lazyPauses) Paused(ctx context.Context, ws, table string) (bool, error) {
	return l.w.svc.Paused(ctx, ws, table)
}

// world is a Citadel (and, on demand, Towers) with a workspace.
type world struct {
	t   *testing.T
	ctx context.Context
	clk *clock.Fake
	be  backend
	// root holds everything of the test.
	root    string
	machine string

	db     *store.DB
	git    tables.Git
	spy    *spyGit
	remote gitx.Repo
	wss    *workspaces.Service
	svc    *tables.Service
	reg    characters.Registry
	inb    *inbox.Service
	conns  *connections.Service

	chars    *fakeChars
	deliv    *fakeDeliverer
	gh       *fakeGitHub
	msgs     *fakeMessages
	atts     *fakeAttachments
	files    *fakeFiles
	notices  *noticeLog
	alerts   *alertLog
	dest     *fakeDestructive
	rituals  *fakeRituals
	requests *fakeRequests
	peers    *loopPeers

	gm characters.Character
	// enqueueErr makes the inbox refuse what the tables put in it.
	enqueueErr error
}

// flakyInbox is the inbox, which a test can make refuse.
type flakyInbox struct {
	*inbox.Service
	w *world
}

func (f flakyInbox) Enqueue(ctx context.Context, n inbox.NewItem) (inbox.Item, error) {
	if f.w.enqueueErr != nil {
		return inbox.Item{}, f.w.enqueueErr
	}
	return f.Service.Enqueue(ctx, n)
}

type worldOpts struct {
	be backend
	// noPeers leaves the Service without a way to the Towers.
	noPeers bool
	// settings replaces the defaults of the workspace.
	settings tables.Settings
	// deps replaces the absence of dependencies between the epics.
	deps tables.Dependencies
}

func newWorld(t *testing.T, o worldOpts) *world {
	t.Helper()
	if o.be == nil {
		o.be = fakeBackend{}
	}
	w := &world{t: t, ctx: t.Context(), clk: clock.NewFake(day0), be: o.be, root: t.TempDir(), machine: "cidadela"}
	w.spy = &spyGit{Git: w.be.newGit(t, w.clk)}
	w.git = w.spy
	w.db = openCopy(t, "mestre.db", guildTemplate, guilddb.Open, w.clk)
	w.reg = characters.Registry{DB: w.db}

	w.remote = w.be.initRepo(t, w.git, filepath.Join(w.root, "remote", "meu-app"), map[string]string{"README.md": "# Meu App\n"})
	var err error
	w.wss, err = workspaces.New(workspaces.Options{DB: w.db, Git: w.git, Layout: workspaces.Layout{Root: filepath.Join(w.root, "workspaces")}, Clock: w.clk,
		GitHub: wsGitHub{url: w.remote.Dir()}, Alerts: &alertLog{}, GrandMaster: wsGM{}})
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range workspaces.Stages {
		if st != workspaces.StageMirror {
			if err := w.wss.Register(st, func(context.Context, workspaces.Workspace) error { return nil }); err != nil {
				t.Fatal(err)
			}
		}
	}

	w.conns, err = connections.NewService(connections.Options{Registry: w.reg, Clock: w.clk})
	if err != nil {
		t.Fatal(err)
	}
	w.deliv = &fakeDeliverer{w: w}
	w.inb, err = inbox.NewService(inbox.Options{DB: &inbox.Store{DB: w.db}, Clock: w.clk, Registry: w.reg, Deliverer: w.deliv, Pauses: lazyPauses{w}})
	if err != nil {
		t.Fatal(err)
	}
	w.chars = &fakeChars{w: w, schedule: map[string]bool{}}
	w.gh = &fakeGitHub{epics: map[int]tables.Epic{}}
	w.msgs = &fakeMessages{msgs: map[string]tables.Message{}}
	w.atts = &fakeAttachments{got: map[string][]sessionzero.File{}}
	w.files = &fakeFiles{}
	w.notices = &noticeLog{}
	w.alerts = &alertLog{}
	w.dest = &fakeDestructive{}
	w.rituals = &fakeRituals{}
	w.requests = &fakeRequests{}
	w.peers = &loopPeers{w: w, towers: map[string]*tower{}}

	opts := tables.Options{DB: w.db, Git: w.git, Workspaces: w.wss, Clock: w.clk, Machine: w.machine, GitHub: w.gh, Characters: w.chars, Registry: w.reg,
		Inbox: flakyInbox{w.inb, w}, Notifier: w.notices, Alerts: w.alerts, Files: w.files, Messages: w.msgs, Attachments: w.atts, Destructive: w.dest, Rituals: w.rituals, Requests: w.requests,
		TempDir: filepath.Join(w.root, "tmp")}
	if !o.noPeers {
		opts.Peers = w.peers
	}
	opts.Settings = o.settings
	opts.Dependencies = o.deps
	w.svc, err = tables.New(opts)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

// activate creates the workspace lucas/Meu_App and walks it to active, with
// its Grand Master.
func (w *world) activate() workspaces.Workspace {
	w.t.Helper()
	ws, err := w.wss.Create(w.ctx, workspaces.CreateRequest{Repo: "lucas/Meu_App"})
	if err != nil {
		w.t.Fatal(err)
	}
	if err := w.wss.Prepare(w.ctx, ws.Slug); err != nil {
		w.t.Fatal(err)
	}
	w.insertGM(ws.Slug)
	return ws
}

// insertGM puts the Grand Master of a workspace in the registry.
func (w *world) insertGM(slug string) {
	w.t.Helper()
	gm := characters.Character{ID: "gm", Name: characters.NameGrandMaster, Kind: characters.KindCharacter, Role: characters.RoleGrandMaster, Workspace: slug,
		Machine: w.machine, Sheet: "Grão-Mestre", Base: "claude-code", State: characters.Idle, TerminalID: "tgm", Lifecycle: characters.Persistent,
		CreatedBy: characters.Creator{Kind: characters.CreatedByEngine}, CreatedAt: w.clk.Now()}
	if err := w.reg.Insert(w.ctx, gm); err != nil {
		w.t.Fatal(err)
	}
	w.gm = gm
}

func (w *world) slug() string { return "meu-app" }

func (w *world) aliveAll() []characters.Character {
	cs, err := w.reg.Alive(w.ctx)
	if err != nil {
		w.t.Fatal(err)
	}
	return cs
}

// open opens the table of an epic.
func (w *world) open(epic int) tables.OpenResult {
	w.t.Helper()
	res, err := w.svc.Open(w.ctx, tables.OpenRequest{By: w.gm, Epic: epic})
	if err != nil {
		w.t.Fatal(err)
	}
	return res
}

// repoOf opens the worktree of a table on the Citadel by its record.
func (w *world) repoOf(tbl tables.Table) gitx.Repo {
	w.t.Helper()
	wt, err := w.svc.Store().Worktree(w.ctx, tbl.Workspace, tbl.ID, w.machine)
	if err != nil {
		w.t.Fatal(err)
	}
	r, err := w.git.OpenWorktree(w.ctx, gitx.WorktreeRecord{Dir: wt.Dir, GitDir: wt.GitDir, Common: wt.Common})
	if err != nil {
		w.t.Fatal(err)
	}
	return r
}

// mirror opens the mirror of the workspace.
func (w *world) mirror() gitx.Repo {
	w.t.Helper()
	r, err := w.wss.OpenMirror(w.ctx, w.slug())
	if err != nil {
		w.t.Fatal(err)
	}
	return r
}

// commit writes a file in the worktree and commits it.
func commit(t *testing.T, be backend, g tables.Git, repo gitx.Repo, rel, content, msg string) gitx.Hash {
	t.Helper()
	be.write(t, g, repo.Dir(), rel, content)
	if err := repo.Stage(t.Context()); err != nil {
		t.Fatal(err)
	}
	h, err := repo.Commit(t.Context(), gitx.CommitOptions{Message: msg, Author: gitx.Signature{Name: "Dev", Email: "dev@example.invalid", When: day0}})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// pr marks the table as in review with a merged pull request: the state of a
// table whose PR was opened and merged.
func (w *world) merged(tbl tables.Table, head gitx.Hash) tables.Table {
	w.t.Helper()
	if _, err := w.svc.PROpened(w.ctx, tbl.Workspace, tbl.ID, 7, string(head)); err != nil {
		w.t.Fatal(err)
	}
	got, err := w.svc.MergeConfirmed(w.ctx, tbl.Workspace, tbl.ID, string(head))
	if err != nil {
		w.t.Fatal(err)
	}
	return got
}

// --- the Towers ---

// tower is one machine of the tests: its own git, its database, the replica
// of the mirror and the half of the tables.
type tower struct {
	w       *world
	id      string
	git     tables.Git
	db      *store.DB
	root    string
	half    *tables.Tower
	files   *fakeFiles
	mirrors replication.MirrorStore
	told    []string
	sendErr error
	mu      sync.Mutex
}

// loopPeers is the connection of the protocol between the Citadel and the
// Towers: the bundles go through the same receivers the connection would
// call, with the hash and the files of a transfer.
type loopPeers struct {
	w      *world
	mu     sync.Mutex
	towers map[string]*tower
	down   map[string]bool
	// sentTo counts the bundles sent to each Tower, and incremental how many
	// of them were incremental.
	sent        map[string]int
	incremental map[string]int
}

func (p *loopPeers) tower(machine string) (*tower, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	tw, ok := p.towers[machine]
	if !ok || p.down[machine] {
		return nil, fmt.Errorf("%w: %s", tables.ErrMachineUnreachable, machine)
	}
	return tw, nil
}

func (p *loopPeers) Connected(machine string) bool {
	_, err := p.tower(machine)
	return err == nil
}

func (p *loopPeers) setDown(machine string, down bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.down == nil {
		p.down = map[string]bool{}
	}
	p.down[machine] = down
}

func (p *loopPeers) SendBundle(ctx context.Context, machine string, meta protocol.Object, src protocol.BundleSource) error {
	tw, err := p.tower(machine)
	if err != nil {
		return err
	}
	p.mu.Lock()
	if p.sent == nil {
		p.sent, p.incremental = map[string]int{}, map[string]int{}
	}
	p.sent[machine]++
	p.mu.Unlock()
	return deliver(ctx, tw.w.t, tw.half, meta, src, func() {
		p.mu.Lock()
		p.incremental[machine]++
		p.mu.Unlock()
	})
}

func (p *loopPeers) WorktreeDir(_ context.Context, machine, workspace, table string) (string, error) {
	tw, err := p.tower(machine)
	if err != nil {
		return "", err
	}
	return tw.dir(workspace, table), nil
}

func (p *loopPeers) Inspect(ctx context.Context, machine, workspace, table string) (tables.Inspection, error) {
	tw, err := p.tower(machine)
	if err != nil {
		return tables.Inspection{}, err
	}
	return tw.half.Inspect(ctx, workspace, table)
}

func (p *loopPeers) RemoveWorktree(ctx context.Context, machine, workspace, table string) error {
	tw, err := p.tower(machine)
	if err != nil {
		return err
	}
	return tw.half.RemoveWorktree(ctx, workspace, table)
}

func (tw *tower) dir(workspace, table string) string {
	return filepath.Join(tw.root, "workspaces", workspace, "mesas", table)
}

// deliver is protocol.Link.SendBundle over a function call: the incremental
// bundle first, the whole branch when the receiver lacks the prerequisite.
func deliver(ctx context.Context, t *testing.T, fin protocol.Finalizer, meta protocol.Object, src protocol.BundleSource, onIncremental func()) error {
	t.Helper()
	send := func(open protocol.OpenSource) error {
		s, release, err := open(ctx)
		if err != nil {
			return err
		}
		defer release()
		return finalize(ctx, t, fin, meta, s)
	}
	if src.Incremental != nil {
		err := send(src.Incremental)
		if onIncremental != nil && err == nil {
			onIncremental()
		}
		var pe *protocol.Error
		if err == nil || !asProto(err, &pe) || pe.Code != protocol.CodeMissingPrerequisite {
			return err
		}
	}
	return send(src.Full)
}

func asProto(err error, pe **protocol.Error) bool {
	for err != nil {
		if e, ok := err.(*protocol.Error); ok {
			*pe = e
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

// finalize does what the end of a transfer does on the receiver: the hash of
// the content, the Skipper, and the Finalizer on the file.
func finalize(ctx context.Context, t *testing.T, fin protocol.Finalizer, meta protocol.Object, s protocol.Source) error {
	t.Helper()
	data, err := io.ReadAll(io.NewSectionReader(s, 0, s.Size))
	if err != nil {
		return err
	}
	sum := sha256hex(data)
	begin := protocol.XferBegin{XferID: "x" + sum[:8], Kind: protocol.KindGitBundle, Size: int64(len(data)), SHA256: sum, Meta: meta}
	if sk, ok := fin.(protocol.Skipper); ok && sk.Have(ctx, begin) {
		return nil
	}
	file := filepath.Join(t.TempDir(), "recv.bundle")
	if err := os.WriteFile(file, data, 0o600); err != nil {
		return err
	}
	return fin.Finalize(ctx, begin, file)
}

// addTower puts a Tower in the Guild: its git, its database, the replica of
// the mirror (a bare repository the bundles fill) and the half of the tables.
func (w *world) addTower(id string) *tower {
	w.t.Helper()
	tw := &tower{w: w, id: id, root: filepath.Join(w.root, "towers", id)}
	tw.git = w.be.newGit(w.t, w.clk)
	tw.db = openCopy(w.t, "maquina.db", machineTemplate, machinedb.Open, w.clk)
	// The replica is where the replication puts it: <dados>/workspaces/<slug>/espelho.git.
	tw.mirrors = replication.MirrorStore{Root: filepath.Join(tw.root, "workspaces"), Git: tw.git}
	if _, err := tw.mirrors.Open(w.ctx, w.slug()); err != nil {
		w.t.Fatal(err)
	}
	tw.files = &fakeFiles{}
	var err error
	tw.half, err = tables.NewTower(tables.TowerOptions{
		DB: tw.db, Git: tw.git, Clock: w.clk,
		Mirror:      func(ctx context.Context, ws string) (gitx.Repo, error) { return tw.mirrors.Open(ctx, ws) },
		WorktreeDir: tw.dir,
		Files:       tw.files,
		Send: func(ctx context.Context, meta protocol.Object, src protocol.BundleSource) error {
			tw.mu.Lock()
			err := tw.sendErr
			tw.mu.Unlock()
			if err != nil {
				return err
			}
			return deliver(ctx, w.t, w.svc.ReceiverFor(id), meta, src, nil)
		},
		Tell: func(_ context.Context, _, _, text string) {
			tw.mu.Lock()
			defer tw.mu.Unlock()
			tw.told = append(tw.told, text)
		},
		TempDir: filepath.Join(tw.root, "tmp"),
	})
	if err != nil {
		w.t.Fatal(err)
	}
	w.peers.mu.Lock()
	w.peers.towers[id] = tw
	w.peers.mu.Unlock()
	return tw
}

func (tw *tower) replica() gitx.Repo {
	tw.w.t.Helper()
	r, err := tw.mirrors.Open(tw.w.ctx, tw.w.slug())
	if err != nil {
		tw.w.t.Fatal(err)
	}
	return r
}

// repo opens the worktree of a table on the Tower.
func (tw *tower) repo(table string) gitx.Repo {
	tw.w.t.Helper()
	wts, err := tw.replica().ListWorktrees(tw.w.ctx)
	if err != nil {
		tw.w.t.Fatal(err)
	}
	for _, wt := range wts {
		if filepath.Base(wt.Path) == table {
			r, err := tw.git.Open(tw.w.ctx, wt.Path)
			if err != nil {
				tw.w.t.Fatal(err)
			}
			return r
		}
	}
	tw.w.t.Fatalf("the Tower %s has no worktree of %s", tw.id, table)
	return nil
}

func (tw *tower) notes() []string {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	return slices.Clone(tw.told)
}

func sha256hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// dev recruits a character in the table, as its table master.
func (w *world) dev(tbl tables.Table, name string) (res characters.CreateResult, err error) {
	w.t.Helper()
	master, _, _ := w.reg.TableMaster(w.ctx, w.slug(), tbl.ID)
	return w.chars.Create(w.ctx, characters.CreateRequest{Name: name, Sheet: "Dev", Workspace: w.slug(), Table: tbl.ID, Role: characters.RoleCharacter,
		Creator: characters.Creator{Kind: characters.CreatedByCharacter, ID: master.ID}})
}
