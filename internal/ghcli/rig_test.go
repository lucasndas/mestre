package ghcli_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/cli"
	"github.com/lucasndas/mestre/internal/ghcli"
	"github.com/lucasndas/mestre/internal/github"
	"github.com/lucasndas/mestre/internal/gitx"
	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/internal/testkit/fakegit"
	"github.com/lucasndas/mestre/internal/testkit/fakegithub"
	"github.com/lucasndas/mestre/internal/testkit/fakenet"
	"github.com/lucasndas/mestre/internal/testkit/guildtpl"
	"github.com/lucasndas/mestre/pkg/clock"
)

const repoFull = "lucas/meu-app"

type chars map[string]characters.Character

func (c chars) Get(_ context.Context, id string) (characters.Character, error) {
	if x, ok := c[id]; ok {
		return x, nil
	}
	return characters.Character{}, characters.ErrNoCharacter
}

type tables map[string]github.TableRef

func (t tables) Epic(_ context.Context, table string) (github.TableRef, bool, error) {
	v, ok := t[table]
	return v, ok, nil
}

type repos struct{}

// branches is the mirror of the Citadel: the branch of the table is in it.
type branches struct {
	git    *fakegit.Git
	mirror string
	remote string
	// name is the branch the Citadel says is the table's (default 42-login).
	name *string
}

func (b branches) Prepare(ctx context.Context, _ string) (github.Branch, error) {
	repo, err := b.git.Open(ctx, b.mirror)
	if err != nil {
		return github.Branch{}, err
	}
	name := "42-login"
	if b.name != nil && *b.name != "" {
		name = *b.name
	}
	head, err := repo.ResolveRef(ctx, name)
	if err != nil {
		return github.Branch{}, err
	}
	return github.Branch{Repo: repo, Name: name, Base: "main", BaseBranch: "main", Head: string(head), Remote: b.remote}, nil
}

// pushAuth gives the credentials of the push and remembers whether the
// permission of the workflows was asked.
type pushAuth struct {
	mu        sync.Mutex
	workflows []bool
	// onAuth runs when the credentials are asked, after the change was read and
	// before the push (another sync of the table).
	onAuth func()
}

func (p *pushAuth) PushAuth(_ context.Context, _ string, workflows bool) (gitx.Auth, error) {
	if p.onAuth != nil {
		p.onAuth()
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.workflows = append(p.workflows, workflows)
	return gitx.BasicAuth("https://github.com/", "x-access-token", "ghs_pushTokenOfTheTestRig0123456789"), nil
}

// workflowsFake is the policy of the classes for the workflows and the approvals.
type workflowsFake struct {
	mu       sync.Mutex
	policy   map[string]classes.Workflows
	approved map[string]bool // by head
	asked    []string
}

func (w *workflowsFake) Policy(_ context.Context, class string) (classes.Workflows, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.policy[class], nil // never by default
}

func (w *workflowsFake) Approved(_ context.Context, _, _, head string) (bool, int64, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.asked = append(w.asked, head)
	return w.approved[head], 77, nil
}

// criteriaFake says which criteria have no proof.
type criteriaFake struct {
	mu   sync.Mutex
	open []string
}

func (c *criteriaFake) Unproven(context.Context, string) ([]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.open...), nil
}

// lease is the lease of the tests: valid, or expired with a queue.
type lease struct {
	mu     sync.Mutex
	down   bool
	queued []func(context.Context) error
}

func (l *lease) Allow() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.down {
		return errors.New("expired")
	}
	return nil
}

func (l *lease) Do(ctx context.Context, _ string, fn func(context.Context) error) (bool, error) {
	l.mu.Lock()
	if l.down {
		l.queued = append(l.queued, fn)
		l.mu.Unlock()
		return true, nil
	}
	l.mu.Unlock()
	return false, fn(ctx)
}

// expire makes the lease expire; renew gives it back and sends the queue, in order.
func (l *lease) expire() { l.mu.Lock(); l.down = true; l.mu.Unlock() }

func (l *lease) renew(ctx context.Context) int {
	l.mu.Lock()
	l.down = false
	q := l.queued
	l.queued = nil
	l.mu.Unlock()
	for _, f := range q {
		_ = f(ctx)
	}
	return len(q)
}

// prs gives the PR of each table.
type prs map[string]int

func (p prs) PRNumber(_ context.Context, key string) (int, bool, error) {
	n, ok := p[key]
	return n, ok, nil
}

func (repos) Repo(context.Context, string) (string, error) { return repoFull, nil }

// clients gives each character the REST of its App: the token tells the App to
// the double.
type clients struct {
	gh *fakegithub.Server
	// onFiles runs when the files of a PR are read (a push in the middle of a command).
	onFiles *func()
	quota   *github.Quota
}

// hooked is the REST with a hook on the reading of the files of a PR.
type hooked struct {
	*github.REST
	onFiles *func()
}

func (h hooked) PullFiles(ctx context.Context, repo string, n int) ([]github.PullFile, error) {
	files, err := h.REST.PullFiles(ctx, repo, n)
	if h.onFiles != nil && *h.onFiles != nil {
		(*h.onFiles)()
	}
	return files, err
}

// appOf is the App of a character (its slug) and the token the double knows it by.
func appOf(ch characters.Character) (app, token string) {
	switch {
	case ch.Role == characters.RoleGrandMaster:
		return "grand_master", "tok_gm"
	case ch.Class == "qa":
		return "qa", "tok_qa"
	}
	return "mestre", "tok_mestre"
}

func (c clients) For(_ context.Context, ch characters.Character, _ string) (github.PullAPI, error) {
	app, tok := appOf(ch)
	return hooked{&github.REST{BaseURL: "https://api.github.com", HTTP: &http.Client{Transport: fakenet.HandlerTransport(c.gh)},
		Token: func(context.Context, string) (string, error) { return tok, nil }, Quota: c.quota, App: app}, c.onFiles}, nil
}

// quotaOf says whether the App of a character is low.
type quotaOf struct{ q *github.Quota }

func (q quotaOf) Low(_ context.Context, ch characters.Character) (time.Time, bool) {
	app, _ := appOf(ch)
	return q.q.Low(app)
}

type rig struct {
	t     *testing.T
	ctx   context.Context
	gh    *fakegithub.Server
	objs  *github.Objects
	gate  *cli.Gate
	prs   prs
	lease *lease
	// locked makes the Mestre locked.
	locked atomic.Bool
	clk    *clock.Fake
	// the sending of the branch
	git      *fakegit.Git
	mirror   *fakegit.Repo
	remote   string
	auth     *pushAuth
	wf       *workflowsFake
	criteria *criteriaFake
	branch   *string
	// onFiles: see clients.
	onFiles *func()
	h       *ghcli.Handlers
	fwd     *localchan.LocalForwarder
	db      *store.DB
	chars   chars
	quota   *github.Quota
}

// newRig has a table 42-login (its epic is the issue #1) with its Mestre da mesa "tm", a QA
// "qa" of the class qa that may use issue commands, a Dev "dev" whose class
// does not, and the Grão-Mestre "gm".
func newRig(t *testing.T) *rig {
	t.Helper()
	ctx := t.Context()
	clk := clock.NewFake(time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC))
	db, err := guildtpl.Open(ctx, filepath.Join(t.TempDir(), "mestre.db"), store.Options{Synchronous: "OFF", Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	gh, err := fakegithub.New(clk, t)
	if err != nil {
		t.Fatal(err)
	}
	gh.AddRepo("lucas", "meu-app")
	gh.IssueToken("tok_gm", "grand_master")
	gh.IssueToken("tok_mestre", "mestre")
	gh.IssueToken("tok_qa", "qa")
	gh.AddIssue(repoFull, "épico 42", github.EpicLabel) // #1... the numbers of the double start at 1
	objs := &github.Objects{DB: db, Clock: clk}
	cs := chars{
		"tm":  {ID: "tm", Role: characters.RoleTableMaster, Workspace: "meu-app", Table: "42-login", Class: "mestre-da-mesa"},
		"qa":  {ID: "qa", Role: characters.RoleCharacter, Workspace: "meu-app", Table: "42-login", Class: "qa"},
		"rev": {ID: "rev", Role: characters.RoleCharacter, Workspace: "meu-app", Table: "42-login", Class: "revisor"},
		"dev": {ID: "dev", Role: characters.RoleCharacter, Workspace: "meu-app", Table: "42-login", Class: "dev"},
		"gm":  {ID: "gm", Role: characters.RoleGrandMaster, Workspace: "meu-app"},
	}
	pr := prs{}
	quota := &github.Quota{Clock: clk}
	onFiles := new(func())
	ls := &lease{}
	git := fakegit.NewGit(clk)
	root := t.TempDir()
	mirrorDir, remoteDir := filepath.Join(root, "espelho.git"), filepath.Join(root, "github.git")
	for _, d := range []string{mirrorDir, remoteDir} {
		if _, err := git.Init(ctx, d, gitx.InitOptions{Bare: true, InitialBranch: "main"}); err != nil {
			t.Fatal(err)
		}
	}
	mirror := git.Handle(mirrorDir).Data()
	mirror.Commit("main", "chore: initial", map[string]string{"README.md": "# Meu App\n", ".github/workflows/ci.yml": "name: ci\n"})
	if err := mirror.Branch("42-login", "main"); err != nil {
		t.Fatal(err)
	}
	mirror.Commit("42-login", "feat: login", map[string]string{"src/login.ts": "export const timeout = 30\n"})
	branch := new(string)
	auth, wf, crit := &pushAuth{}, &workflowsFake{policy: map[string]classes.Workflows{}, approved: map[string]bool{}}, &criteriaFake{}
	send := github.Sending{Branches: branches{git: git, mirror: mirrorDir, remote: remoteDir, name: branch}, Auth: auth, Workflows: wf, Criteria: crit}
	h := &ghcli.Handlers{PRs: pr, Lease: ls, Send: send, Opened: func(_ context.Context, key string, n int, _ string) error { pr[key] = n; return nil }, Characters: cs, Quota: quotaOf{quota}, Clients: clients{gh, onFiles, quota}, Repos: repos{}, Tables: tables{"meu-app/42-login": {Repo: repoFull, Epic: 1}}, Objects: objs, Clock: clk}
	fwd := localchan.NewLocalForwarder()
	h.Register(fwd)
	profiles := cli.ProfilesFunc(func(_ context.Context, id string) cli.Profile {
		switch id {
		case "tm":
			return cli.Profile{Role: cli.RoleTableMaster}
		case "gm":
			return cli.Profile{Role: cli.RoleGrandMaster}
		case "qa", "rev":
			return cli.Profile{Role: cli.RoleCharacter, AllowsCLI: func(c string) bool { return len(c) > 6 && c[:6] == "issue " || len(c) > 3 && c[:3] == "pr " }}
		}
		return cli.Profile{Role: cli.RoleCharacter}
	})
	rg := &rig{t: t, ctx: ctx, gh: gh, objs: objs, prs: pr, lease: ls, clk: clk, onFiles: onFiles, git: git, mirror: mirror, remote: remoteDir, auth: auth, wf: wf, criteria: crit, branch: branch, h: h, fwd: fwd, db: db, chars: cs, quota: quota, gate: cli.NewGate(cli.GateConfig{Profiles: profiles, Next: fwd})}
	h.Locked = func(context.Context) bool { return rg.locked.Load() }
	return rg
}

// result is the answer of a command.
type result struct {
	OK    bool
	Exit  int
	Code  string
	Data  map[string]any
	Error string
}

// run sends a command of a character through the gate, as the CLI would.
func (r *rig) run(who, cmd string, flags map[string]any, args ...string) result {
	r.t.Helper()
	id, err := localchan.NewID(clock.Real{})
	if err != nil {
		r.t.Fatal(err)
	}
	return r.runID(id, who, cmd, flags, args...)
}

// runID is run with the id of the request: the same one again is a resending.
func (r *rig) runID(id, who, cmd string, flags map[string]any, args ...string) result {
	r.t.Helper()
	if flags == nil {
		flags = map[string]any{}
	}
	resp := r.gate.Forward(r.ctx, &localchan.Call{
		Request: localchan.Request{V: 1, ID: id, AgentID: who, Cmd: cmd, Args: localchan.TextArgs(args...), Flags: flags},
		Caller:  localchan.Caller{Identified: true, AgentID: who, TerminalID: "t-" + who},
	})
	out := result{OK: resp.OK, Exit: resp.ExitCode()}
	if resp.Error != nil {
		out.Code, out.Error = resp.Error.Code, resp.Error.Message
	}
	if len(resp.Data) > 0 {
		var m map[string]any
		_ = json.Unmarshal(resp.Data, &m)
		out.Data = m
	}
	return out
}

func itoa(n int) string { return strconv.Itoa(n) }

// gitxErr is the refusal of a push, by what GitHub said.
func gitxErr(kind string) error {
	switch kind {
	case "workflow":
		return gitx.ErrPushWorkflow
	case "signature":
		return gitx.ErrPushSignature
	case "protected":
		return gitx.ErrPushProtected
	}
	return gitx.ErrNotFastForward
}

// claim makes a class the owner of a block of the PR.
func claim(r *rig, pr int, block, class string) error {
	_, err := r.objs.DB.Writer().ExecContext(r.ctx, `INSERT INTO pr_blocks (repo, pr, name, class) VALUES (?, ?, ?, ?) ON CONFLICT (repo, pr, name) DO UPDATE SET class = excluded.class`, repoFull, pr, block, class)
	return err
}
