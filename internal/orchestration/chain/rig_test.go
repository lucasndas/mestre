package chain_test

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/app"
	"github.com/lucasndas/mestre/internal/approvals"
	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/gitx"
	"github.com/lucasndas/mestre/internal/hook"
	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/orchestration/chain"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/inbox"
	"github.com/lucasndas/mestre/internal/orchestration/tables"
	"github.com/lucasndas/mestre/internal/orchestration/workspaces"
	"github.com/lucasndas/mestre/internal/providers"
	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/internal/testkit/fakechannel"
	"github.com/lucasndas/mestre/internal/testkit/fakegit"
	"github.com/lucasndas/mestre/internal/testkit/guildtpl"
	"github.com/lucasndas/mestre/internal/testkit/orcfakes"
	"github.com/lucasndas/mestre/internal/users"
	"github.com/lucasndas/mestre/pkg/clock"
)

// devYAML is the class of the characters that ask: commands that are not in
// the list ask, and the only domain is the registry.
const devYAML = `version: 1
files:
  read: ["**"]
  write: ["**"]
commands:
  allow: ["npm test"]
  ask: ["npm install *"]
  default: ask
network:
  domains: ["registry.npmjs.org"]
tools:
  default: ask
`

// fakeClasses gives the rules, the environment and the domains of the
// characters; a test replaces what it needs.
type fakeClasses struct {
	mu      sync.Mutex
	rules   map[string]*classes.Rules
	decrees map[string][]classes.Decree
	yaml    map[string]string
	domains map[string][]string
	used    map[string]bool
	nextID  int64
	now     time.Time
	// gate is the gate of the temporary table rules the environment carries.
	gate classes.TableRuleGate
}

func newFakeClasses(now time.Time) *fakeClasses {
	return &fakeClasses{rules: map[string]*classes.Rules{}, decrees: map[string][]classes.Decree{}, yaml: map[string]string{}, domains: map[string][]string{},
		used: map[string]bool{}, now: now}
}

func (f *fakeClasses) build(id string) *classes.Rules {
	var p *classes.Permissions
	var err error
	if y, ok := f.yaml[id]; ok {
		p, err = classes.ParsePermissions([]byte(y))
	} else if f.rules[id] != nil {
		return f.rules[id]
	} else {
		p, err = classes.ParsePermissions([]byte(devYAML))
	}
	if err != nil {
		panic(err)
	}
	r, err := classes.Build(classes.Layers{AgentID: id, Class: "dev", Permissions: p, Decrees: f.decrees[id], Now: f.now})
	if err != nil {
		panic(err)
	}
	r.Version = len(f.decrees[id]) + 1
	return r
}

func (f *fakeClasses) Rules(_ context.Context, id string) (*classes.Rules, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.build(id), nil
}

func (f *fakeClasses) Env(_ context.Context, id string) (classes.Env, error) {
	return classes.Env{AgentID: id, Worktree: "/work/42-login", OS: "linux", Exceptions: ledger{f}, TableRules: f.gate}, nil
}

func (f *fakeClasses) Domains(_ context.Context, id string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if d, ok := f.domains[id]; ok {
		return d, nil
	}
	return []string{"registry.npmjs.org", "pypi.org"}[:1], nil
}

// ledger records the exceptions used, once each.
type ledger struct{ f *fakeClasses }

func (l ledger) Use(_ context.Context, agent string, id int64) (bool, error) {
	l.f.mu.Lock()
	defer l.f.mu.Unlock()
	k := fmt.Sprintf("%s/%d", agent, id)
	if l.f.used[k] {
		return false, nil
	}
	l.f.used[k] = true
	return true, nil
}

// fakeGrants makes the exceptions as decrees of the effective rules.
type fakeGrants struct {
	f       *fakeClasses
	mu      sync.Mutex
	decrees []chain.DecreeGrant
	// hook runs once, in the middle of the next exception (a timer firing, a
	// crash); fail makes the next exception fail.
	hook func()
	fail error
}

func (g *fakeGrants) Exception(_ context.Context, e chain.ExceptionGrant) error {
	if g.hook != nil {
		h := g.hook
		g.hook = nil
		h()
	}
	if g.fail != nil {
		err := g.fail
		g.fail = nil
		return err
	}
	g.f.mu.Lock()
	defer g.f.mu.Unlock()
	g.f.nextID++
	a := e.Action
	g.f.decrees[e.Character] = append(g.f.decrees[e.Character], classes.Decree{ID: g.f.nextID, Kind: classes.DecreeException, Level: classes.LevelTableMaster,
		Effect: classes.Effect{Kind: classes.EffectPermission}, Action: &a})
	return nil
}

func (g *fakeGrants) Decree(_ context.Context, d chain.DecreeGrant) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.decrees = append(g.decrees, d)
	return nil
}

type fakeTemp struct {
	mu    sync.Mutex
	rules []chain.TempRule
}

func (f *fakeTemp) Create(_ context.Context, r chain.TempRule) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rules = append(f.rules, r)
	return nil
}

type fakeUsers struct {
	mu        sync.Mutex
	asked     []chain.PeopleQuestion
	withdrawn []int64
}

func (u *fakeUsers) Ask(_ context.Context, q chain.PeopleQuestion) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.asked = append(u.asked, q)
	return nil
}

func (u *fakeUsers) Withdraw(_ context.Context, id int64, _ string) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.withdrawn = append(u.withdrawn, id)
	return nil
}

func (u *fakeUsers) askedFor(id int64) []chain.PeopleQuestion {
	u.mu.Lock()
	defer u.mu.Unlock()
	var out []chain.PeopleQuestion
	for _, q := range u.asked {
		if q.Request.ID == id {
			out = append(out, q)
		}
	}
	return out
}

type fakeAlerts struct {
	mu   sync.Mutex
	told []string
}

func (a *fakeAlerts) Notify(_ context.Context, _, text string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.told = append(a.told, text)
	return nil
}

// fakeDelegations says whether a delegation of the Owner covers an alto.
type fakeDelegations struct {
	mu     sync.Mutex
	covers bool
}

func (d *fakeDelegations) Covers(context.Context, chain.Request) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.covers, nil
}

func (d *fakeDelegations) set(v bool) {
	d.mu.Lock()
	d.covers = v
	d.mu.Unlock()
}

type fakeSupervision struct {
	mu     sync.Mutex
	paused bool
	seen   []chain.AuditEntry
}

func (s *fakeSupervision) AutonomyPaused(context.Context, string, string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.paused, nil
}

func (s *fakeSupervision) Decided(_ context.Context, e chain.AuditEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seen = append(s.seen, e)
	return nil
}

// world is the Citadel of the tests with a workspace, a table, its master and
// the chain of approval.
type world struct {
	t        *testing.T
	ctx      context.Context
	clk      *clock.Fake
	orc      *app.Orchestration
	ch       *chain.Service
	machines *orcfakes.Machines
	cls      *fakeClasses
	grants   *fakeGrants
	temp     *fakeTemp
	users    *fakeUsers
	alerts   *fakeAlerts
	sup      *fakeSupervision
	deleg    *fakeDelegations
	prs      *fakePRs
	// supervisor is the real supervision, with worldOpts.realSup.
	supervisor *chain.Supervisor
	policy     *policy
	slug       string
	table      tables.Table
	gm         characters.Character
	master     characters.Character
	fwd        *localchan.LocalForwarder
	sheets     int
	// lib is the library of the real classes (worldOpts.realClasses).
	lib *classes.Library
	// The real flow of approval over the people (worldOpts.flow).
	usvc  *users.Service
	flow  *approvals.Flow
	chn   *fakechannel.Channel
	owner users.User
}

// policy is the policy file of the tests: the settings change under it.
type policy struct {
	mu    sync.Mutex
	s     chain.Settings
	err   error
	rules chain.PolicyRules
}

// Rules are the rules of the policy file of the tests (the default content
// until a test sets others).
func (p *policy) Rules(string) (chain.PolicyRules, string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.rules, "p1"
}

func (p *policy) setRules(r chain.PolicyRules) {
	p.mu.Lock()
	p.rules = r
	p.mu.Unlock()
}

// fakePRs gives the facts of the PRs the conditions read, by number.
type fakePRs struct {
	mu    sync.Mutex
	facts map[int]chain.PRFacts
	err   error
	read  int
}

func (f *fakePRs) PullRequest(_ context.Context, _ string, ref chain.PRRef) (chain.PRFacts, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.read++
	if f.err != nil {
		return chain.PRFacts{}, f.err
	}
	return f.facts[ref.Number], nil
}

func (f *fakePRs) set(n int, facts chain.PRFacts) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.facts == nil {
		f.facts = map[int]chain.PRFacts{}
	}
	f.facts[n] = facts
}

func (p *policy) Chain(context.Context, string) (chain.Settings, string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != nil {
		return chain.Settings{}, "", p.err
	}
	return p.s, "p1", nil
}

func (p *policy) fail(err error) {
	p.mu.Lock()
	p.err = err
	p.mu.Unlock()
}

func (p *policy) set(s chain.Settings) {
	p.mu.Lock()
	p.s = s
	p.mu.Unlock()
}

type worldOpts struct {
	noGrants, noTemp, realTemp, realSup bool
	// realClasses builds the rules of the approvers (and the domains) with
	// the production builder of the Citadel (app.CitadelClasses) over a real
	// library; restrictions are the restrictions of sheets, by name.
	realClasses  bool
	restrictions map[string]map[string]any
	// classYAML replaces devYAML as the permissions of the approved class
	// dev (realClasses).
	classYAML string
	// flow puts the real flow of approval in the place of the fake people.
	flow bool
}

func newWorld(t *testing.T, o ...worldOpts) *world {
	t.Helper()
	var opt worldOpts
	if len(o) > 0 {
		opt = o[0]
	}
	ctx := t.Context()
	clk := clock.NewFake(time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC))
	root := t.TempDir()
	db, err := guildtpl.Open(ctx, filepath.Join(root, "mestre.db"), store.Options{Synchronous: "OFF", Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	git := fakegit.NewGit(clk)
	remote, err := git.Init(ctx, filepath.Join(root, "remote", "meu-app"), gitx.InitOptions{})
	if err != nil {
		t.Fatal(err)
	}
	git.Handle(remote.Dir()).Data().Commit("main", "chore: initial commit", map[string]string{"README.md": "# Meu App\n"})

	w := &world{t: t, ctx: ctx, clk: clk, cls: newFakeClasses(clk.Now()), temp: &fakeTemp{}, users: &fakeUsers{}, alerts: &fakeAlerts{}, sup: &fakeSupervision{}, deleg: &fakeDelegations{}, prs: &fakePRs{},
		policy: &policy{s: chain.DefaultSettings(), rules: chain.DefaultRules()}}
	w.grants = &fakeGrants{f: w.cls}
	w.machines = orcfakes.NewMachines(clk)
	w.machines.Chars = func() *characters.Service { return w.orc.Characters }
	oo := app.OrchestrationOptions{Guild: db, Machine: "cidadela", Clock: clk, WorkspacesDir: filepath.Join(root, "workspaces"), TempDir: filepath.Join(root, "tmp"),
		Git: git, Machines: w.machines, Presets: orcfakes.Presets{Persistent: map[string]bool{"Dev Claude": true}, Restrictions: opt.restrictions}, Builder: orcfakes.Builder{}, GitHub: orcfakes.Hub{URL: remote.Dir()},
		Epics: orcfakes.Epics{Also: []int{43}}, Alerts: orcfakes.Alerts{}, Files: orcfakes.Files{}, Messages: orcfakes.Messages{}, Peers: &orcfakes.Peers{Root: filepath.Join(root, "towers")},
		Users: &orcfakes.Users{}, Destructive: orcfakes.Destructive{}, Limits: characters.Limits{PerMachine: 40, PerTable: 20, PerWorkspace: 40},
		ChainClasses: w.cls, ChainPolicy: w.policy, ChainUsers: w.users, ChainAlerts: w.alerts, ChainSupervision: w.sup, ChainDelegations: w.deleg, ChainPRs: w.prs}
	if opt.realSup {
		oo.ChainSupervision = nil
	}
	if opt.flow {
		w.chn = fakechannel.New()
		w.usvc, err = users.New(users.Options{DB: db, Clock: clk, Approvals: lateApprovals{w}, PIN: users.PINCost{MemoryKiB: 8, Time: 1, Threads: 1}})
		if err != nil {
			t.Fatal(err)
		}
		if w.owner, err = w.usvc.CreateOwner(ctx, "Lucas"); err != nil {
			t.Fatal(err)
		}
		w.flow, err = approvals.New(approvals.Options{DB: db, Clock: clk, Chain: lateChain{w}, Users: w.usvc, PINs: w.usvc, Messenger: w.chn, Locked: func(context.Context) bool { return false }})
		if err != nil {
			t.Fatal(err)
		}
		oo.ChainUsers = w.flow
	}
	if opt.realClasses {
		w.lib = &classes.Library{Roots: classes.DefaultRoots(filepath.Join(root, "classes-globais"), filepath.Join(root, "clone")), Records: classes.ClassDB{DB: db, Clock: clk},
			Clock: clk, OS: "linux", Skill: func(string) bool { return false }}
		cc, err := app.NewCitadelClasses(app.CitadelClassesOptions{Registry: characters.Registry{DB: db}, Libraries: func(string) *classes.Library { return w.lib },
			Presets: oo.Presets, Decrees: fakeDecrees{w.cls}})
		if err != nil {
			t.Fatal(err)
		}
		oo.ChainClasses = cc
	}
	if !opt.noGrants {
		oo.ChainGrants = w.grants
	}
	if !opt.noTemp && !opt.realTemp {
		oo.ChainTempRules = w.temp
	}
	w.orc, err = app.NewOrchestration(oo)
	if err != nil {
		t.Fatal(err)
	}
	w.ch = w.orc.Chain
	w.supervisor = w.orc.Supervisor
	if opt.realTemp {
		w.cls.gate = w.orc.TableRules
	}
	w.machines.Confirm = w.orc.Inbox.Confirmed
	w.fwd = localchan.NewLocalForwarder()
	w.orc.Register(w.fwd, nil)

	ws, err := w.orc.Workspaces.Create(ctx, workspaces.CreateRequest{Repo: "lucas/Meu_App"})
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range workspaces.Stages {
		if st != workspaces.StageMirror {
			if err := w.orc.Workspaces.Register(st, func(context.Context, workspaces.Workspace) error { return nil }); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := w.orc.Workspaces.Prepare(ctx, ws.Slug); err != nil {
		t.Fatal(err)
	}
	w.slug = ws.Slug
	if opt.realClasses {
		w.lib.Workspace = ws.Slug
		perms := devYAML
		if opt.classYAML != "" {
			perms = opt.classYAML
		}
		w.approveClass("dev", perms)
	}
	res, err := w.orc.Characters.Create(ctx, characters.CreateRequest{Sheet: "Grão-Mestre", Workspace: ws.Slug, Role: characters.RoleGrandMaster, Creator: characters.Creator{Kind: characters.CreatedByEngine}})
	if err != nil {
		t.Fatal(err)
	}
	w.gm = res.Character
	opened, err := w.orc.Tables.Open(ctx, tables.OpenRequest{By: w.gm, Epic: 42})
	if err != nil {
		t.Fatal(err)
	}
	w.table, w.master = opened.Table, opened.Master
	// Both finish their first turn: they are free to be told things.
	w.machines.Report(ctx, w.master.ID, characters.Idle)
	w.machines.Report(ctx, w.gm.ID, characters.Idle)
	return w
}

// nextSheet rotates the sheets, since a sheet has a few copies only.
func (w *world) nextSheet() string {
	sheets := []string{"Dev Claude", "Revisor Codex", "QA Antigravity", "Arquiteto Claude"}
	w.sheets++
	return sheets[w.sheets%len(sheets)]
}

// recruit makes a character of the table as the table master would.
func (w *world) recruit(name string) characters.Character {
	w.t.Helper()
	res, err := w.orc.Characters.Create(w.ctx, characters.CreateRequest{Name: name, Sheet: w.nextSheet(), Workspace: w.slug, Table: w.table.ID, Role: characters.RoleCharacter,
		Creator: characters.Creator{Kind: characters.CreatedByCharacter, ID: w.master.ID}})
	if err != nil {
		w.t.Fatal(err)
	}
	if res.Pending != "" {
		w.t.Fatalf("recruit %s is pending: %s", name, res.Pending)
	}
	return res.Character
}

func (w *world) state(c characters.Character, st characters.State) {
	w.t.Helper()
	w.machines.Report(w.ctx, c.ID, st)
}

func (w *world) get(id string) characters.Character {
	w.t.Helper()
	c, err := w.orc.Characters.Get(w.ctx, id)
	if err != nil {
		w.t.Fatal(err)
	}
	return c
}

func (w *world) request(id int64) chain.Request {
	w.t.Helper()
	r, err := w.ch.Get(w.ctx, id)
	if err != nil {
		w.t.Fatal(err)
	}
	return r
}

// shell is an ask of a character for a command: the event of the hook, the
// risk the engine gives it and the open request.
func (w *world) shell(c characters.Character, command string) int64 {
	w.t.Helper()
	id, err := w.ch.Open(w.ctx, w.hookRequest(c, command))
	if err != nil {
		w.t.Fatalf("open %q: %v", command, err)
	}
	return id
}

func (w *world) hookRequest(c characters.Character, command string) hook.ApprovalRequest {
	w.t.Helper()
	tool := &providers.Tool{NativeName: "Bash", Kind: providers.KindShell, Command: command}
	ev := providers.Event{Version: providers.EventVersion, Event: providers.EventPreTool, AgentID: c.ID, TableID: c.Table, Cwd: "/work/42-login", Tool: tool}
	env, _ := w.cls.Env(w.ctx, c.ID)
	rules, _ := w.cls.Rules(w.ctx, c.ID)
	risk := classes.ClassifyRisk(env, rules, classes.RiskRequest{Tool: tool, Cwd: ev.Cwd})
	act := classes.Action{Kind: tool.Kind, Command: command}
	return hook.ApprovalRequest{AgentID: c.ID, TableID: c.Table, Action: act, About: command, Risk: risk, Event: ev}
}

// level is where a request is, as a short name.
func (w *world) level(id int64) chain.LevelKind { return w.request(id).Level.Kind }

// approvalItems lists the items `approval` of a character, delivered or not.
func (w *world) approvalItems(c characters.Character) []inbox.Item {
	w.t.Helper()
	items, err := w.orc.Inbox.Store().All(w.ctx, c.ID)
	if err != nil {
		w.t.Fatal(err)
	}
	var out []inbox.Item
	for _, it := range items {
		if it.Kind == inbox.KindApproval && it.State != inbox.Cancelled {
			out = append(out, it)
		}
	}
	return out
}

func (w *world) itemsOf(c characters.Character, k inbox.Kind) []inbox.Item {
	w.t.Helper()
	items, err := w.orc.Inbox.Store().All(w.ctx, c.ID)
	if err != nil {
		w.t.Fatal(err)
	}
	var out []inbox.Item
	for _, it := range items {
		if it.Kind == k {
			out = append(out, it)
		}
	}
	return out
}

// decide is the decision of an agent that must be accepted.
func (w *world) decide(by characters.Character, id int64, ans chain.Answer, reason string) chain.Outcome {
	w.t.Helper()
	out, err := w.ch.Decide(w.ctx, chain.Decision{Request: id, By: by.ID, Answer: ans, Reason: reason})
	if err != nil {
		w.t.Fatalf("decide #%d %s: %v", id, ans, err)
	}
	return out
}

// refusal is the decision of an agent that must be refused with the code.
func (w *world) refusal(d chain.Decision) *chain.Refusal {
	w.t.Helper()
	_, err := w.ch.Decide(w.ctx, d)
	ref, ok := err.(*chain.Refusal)
	if !ok {
		w.t.Fatalf("decide %+v = %v; want a refusal", d, err)
	}
	return ref
}

func contains(s string, parts ...string) bool {
	for _, p := range parts {
		if !strings.Contains(s, p) {
			return false
		}
	}
	return true
}

func itoa(n int64) string { return fmt.Sprint(n) }

// activeWait is the reason of the active wait of a character.
func (w *world) activeWait(c characters.Character) string {
	w.t.Helper()
	reason, _, err := w.orc.Waits.Active(w.ctx, c.ID)
	if err != nil {
		w.t.Fatal(err)
	}
	return reason
}

// setWaiting ends the turn of a character that waits for an approval: it goes
// to waiting with the reason, and the Observers are told.
func (w *world) setWaiting(c characters.Character, reason string) {
	w.t.Helper()
	from, _, err := w.orc.Registry.Transition(w.ctx, c.ID, characters.Waiting, characters.Change{Reason: reason, At: w.clk.Now()})
	if err != nil {
		w.t.Fatal(err)
	}
	if err := w.orc.Observers.StateChanged(w.ctx, w.get(c.ID), from); err != nil {
		w.t.Fatal(err)
	}
}

// lateChain and lateApprovals tie the flow to the chain and to the users, which
// are made after it.
type lateChain struct{ w *world }

func (c lateChain) Get(ctx context.Context, id int64) (chain.Request, error) {
	return c.w.ch.Get(ctx, id)
}

func (c lateChain) DecideByPerson(ctx context.Context, d chain.PersonDecision) (chain.Outcome, error) {
	return c.w.ch.DecideByPerson(ctx, d)
}

func (c lateChain) Invalidate(ctx context.Context, id int64, why string) error {
	return c.w.ch.Invalidate(ctx, id, why)
}

type lateApprovals struct{ w *world }

func (a lateApprovals) InvalidateBy(ctx context.Context, id string) error {
	return a.w.flow.InvalidateBy(ctx, id)
}
