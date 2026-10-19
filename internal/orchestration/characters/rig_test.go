package characters_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/hook"
	"github.com/lucasndas/mestre/internal/machinesheet"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/connections"
	"github.com/lucasndas/mestre/internal/orchestration/inbox"
	"github.com/lucasndas/mestre/internal/orchestration/presets"
	"github.com/lucasndas/mestre/internal/orchestration/waits"
	"github.com/lucasndas/mestre/internal/providers"
	"github.com/lucasndas/mestre/internal/sessionzero"
	"github.com/lucasndas/mestre/internal/store"
	guilddb "github.com/lucasndas/mestre/internal/store/guild"
	machinedb "github.com/lucasndas/mestre/internal/store/machine"
	"github.com/lucasndas/mestre/internal/testkit/sheetfake"
	"github.com/lucasndas/mestre/pkg/clock"
)

var day0 = time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)

// The databases are migrated once, by TestMain; every test starts from a copy
// (docs/qualidade/testes.md: creating and migrating a file is slow on a
// Windows runner).
var guildTemplate, machineTemplate []byte

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "characters-template")
	if err != nil {
		fmt.Fprintln(os.Stderr, "characters:", err)
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
			fmt.Fprintln(os.Stderr, "characters: template:", err)
			os.Exit(1)
		}
	}
	code := m.Run()
	for _, f := range exitHooks {
		f()
	}
	os.RemoveAll(dir)
	os.Exit(code)
}

// exitHooks are run when the tests are over (the full suite removes what it
// built).
var exitHooks []func()

func openCopy(t *testing.T, name string, template []byte, open func(context.Context, string, store.Options) (*store.DB, error)) *store.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, template, 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := open(context.Background(), path, store.Options{Synchronous: "OFF"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// presetsDoc is the bases and sheets of the tests.
const presetsDoc = `kind: mestre/presets
version: 1
bases:
  - {name: claude-code, command: claude, platforms: [{os: linux}, {os: windows, command: claude.exe}], ready: hook}
  - {name: codex, command: codex, platforms: [{os: linux}, {os: windows, command: codex.exe}], ready: hook}
  - {name: bash, command: bash, platforms: [{os: linux}], ready: {pattern: '\$ $'}}
  - {name: slow, command: bash, platforms: [{os: linux}], ready: {delay: 5}}
sheets:
  - {name: Dev Claude, base: claude-code, agent: claude-code, class: Dev, max_copies: 200}
  - {name: Dev Persistente, base: claude-code, agent: claude-code, class: Dev, lifecycle: persistent, max_copies: 50}
  - {name: GPU Claude, base: claude-code, agent: claude-code, class: Dev, requires: {gpu: true}}
  - {name: Revisor Codex, base: codex, agent: codex, class: Revisor, lifecycle: persistent, max_idle: 120m}
  - {name: Dupla, base: claude-code, agent: claude-code, class: Dev, max_copies: 2}
`

// fakeMachineSheets are the sheets of the machines of the Guild.
type fakeMachineSheets struct {
	mu     sync.Mutex
	sheets map[string]machinesheet.Sheet
}

func (f *fakeMachineSheets) Sheets(context.Context) ([]machinesheet.Sheet, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]machinesheet.Sheet, 0, len(f.sheets))
	for _, s := range f.sheets {
		out = append(out, s)
	}
	slices.SortFunc(out, func(a, b machinesheet.Sheet) int { return strings.Compare(a.MachineID, b.MachineID) })
	return out, nil
}

func (f *fakeMachineSheets) set(s machinesheet.Sheet) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sheets[s.MachineID] = s
}

func (f *fakeMachineSheets) setOnline(id string, online bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.sheets[id]
	s.Online = online
	f.sheets[id] = s
}

// sheetOf builds the sheet of a fake machine as the Guild keeps it.
func sheetOf(t *testing.T, m *sheetfake.Machine, id, name string, online bool) machinesheet.Sheet {
	t.Helper()
	b, err := machinesheet.NewBuilder(m.Sources(), id, "")
	if err != nil {
		t.Fatal(err)
	}
	s, err := b.Build(context.Background(), machinesheet.Sheet{})
	if err != nil {
		t.Fatal(err)
	}
	s.MachineID, s.Name, s.Online = id, name, online
	return s
}

// machineSpec describes a machine of a rig.
type machineSpec struct {
	id   string
	fake *sheetfake.Machine
}

func linux(id string) machineSpec {
	m := sheetfake.Linux(id)
	m.SetResources(machinesheet.Resources{CPUCores: 8, MemBytes: 16 << 30, DiskFreeBytes: 100 << 30, GPUs: []machinesheet.GPU{}})
	return machineSpec{id, m}
}

func windowsGPU(id string) machineSpec {
	m := sheetfake.Windows(id)
	m.SetResources(machinesheet.Resources{CPUCores: 12, MemBytes: 32 << 30, DiskFreeBytes: 100 << 30, GPUs: []machinesheet.GPU{{Model: "NVIDIA RTX 4070", MemBytes: 12 << 30}}})
	m.SetProviders(
		machinesheet.Provider{Provider: machinesheet.ProviderClaudeCode, Profiles: []machinesheet.Profile{{Name: "claude-trabalho", Version: "2.1.0", LoggedIn: true, Conformant: true, Default: true}}},
		machinesheet.Provider{Provider: machinesheet.ProviderCodex, Profiles: []machinesheet.Profile{{Name: "codex-pessoal", Version: "0.9.0", LoggedIn: true, Conformant: true, Default: true}}},
	)
	return machineSpec{id, m}
}

// withCodex gives a Linux machine a Codex profile.
func withCodex(s machineSpec) machineSpec {
	s.fake.SetProviders(
		machinesheet.Provider{Provider: machinesheet.ProviderClaudeCode, Profiles: []machinesheet.Profile{{Name: "claude-trabalho", Version: "2.1.0", LoggedIn: true, Conformant: true, Default: true}}},
		machinesheet.Provider{Provider: machinesheet.ProviderCodex, Profiles: []machinesheet.Profile{{Name: "codex-pessoal", Version: "0.9.0", LoggedIn: true, Conformant: true, Default: true}}},
	)
	return s
}

// --- fakes of the pieces of other stages ---

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

// of lists the notices of a kind.
func (n *noticeLog) of(kind characters.NoticeKind) []characters.Notice {
	n.mu.Lock()
	defer n.mu.Unlock()
	var out []characters.Notice
	for _, x := range n.ns {
		if x.Kind == kind {
			out = append(out, x)
		}
	}
	return out
}

type fakeScrolls struct {
	mu   sync.Mutex
	sums []characters.Summary
}

func (f *fakeScrolls) Record(_ context.Context, s characters.Summary) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sums = append(f.sums, s)
	return nil
}

func (f *fakeScrolls) all() []characters.Summary {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.sums)
}

type fakeTables struct {
	root   string
	mu     sync.Mutex
	tables map[string]characters.TableInfo
	// failWorktree makes the creation of the worktree fail.
	failWorktree bool
}

func (f *fakeTables) Table(_ context.Context, ws, id string) (characters.TableInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.tables[id]
	if !ok {
		return characters.TableInfo{}, fmt.Errorf("no table %s in %s", id, ws)
	}
	return t, nil
}

func (f *fakeTables) Worktree(_ context.Context, _, table, machine string) (string, error) {
	f.mu.Lock()
	fail := f.failWorktree
	f.mu.Unlock()
	if fail {
		return "", fmt.Errorf("no worktree for %s", table)
	}
	if f.root != "" { // a real folder, for the tests that write in it
		return filepath.Join(f.root, machine, table), nil
	}
	return filepath.Join("/wt", machine, table), nil
}

func (f *fakeTables) setState(id string, st characters.TableState) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t := f.tables[id]
	t.State = st
	f.tables[id] = t
}

type fakeInbox struct {
	mu      sync.Mutex
	pending map[string]int
}

func (f *fakeInbox) Pending(_ context.Context, id string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pending[id], nil
}

func (f *fakeInbox) set(id string, n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pending[id] = n
}

type fakeWaits struct {
	mu sync.Mutex
	by map[string]string
}

func (f *fakeWaits) Active(_ context.Context, id string) (string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.by[id]
	return r, ok, nil
}

func (f *fakeWaits) set(id, reason string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if reason == "" {
		delete(f.by, id)
		return
	}
	f.by[id] = reason
}

// fakeBuilder renders the request of the session zero without the pieces of
// milestone 5.
type fakeBuilder struct {
	mu   sync.Mutex
	last map[string]characters.BuildInput
}

func (b *fakeBuilder) Build(_ context.Context, in characters.BuildInput) (sessionzero.CharacterRequest, error) {
	b.mu.Lock()
	b.last[in.Character.ID] = in
	b.mu.Unlock()
	c := in.Character
	return sessionzero.CharacterRequest{
		ID: c.ID, Name: c.Name, Role: c.Role, Workspace: c.Workspace, Machine: c.Machine, Dir: in.Dir,
		Table: sessionzero.TableContext{ID: c.Table}, Snapshot: in.Snapshot, ResumeSessionID: in.ResumeSessionID, NewSessionID: in.NewSessionID,
	}, nil
}

func (b *fakeBuilder) input(id string) characters.BuildInput {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.last[id]
}

// fakePrep is the session zero of a machine: it gives the plan and keeps what
// was asked.
type fakePrep struct {
	mu       sync.Mutex
	first    string
	verify   func() error
	prepared []sessionzero.CharacterRequest
	restored []sessionzero.CharacterRequest
	ended    []sessionzero.EndRequest
	err      error
	updates  string
}

func (p *fakePrep) Prepare(_ context.Context, req sessionzero.CharacterRequest) (sessionzero.CharacterPlan, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != nil {
		return sessionzero.CharacterPlan{}, p.err
	}
	p.prepared = append(p.prepared, req)
	prov := providers.Null
	switch req.Snapshot.Sheet.Agent {
	case presets.AgentClaudeCode:
		prov = providers.ClaudeCode
	case presets.AgentCodex:
		prov = providers.Codex
	}
	plan := sessionzero.CharacterPlan{ID: req.ID, Provider: prov, FirstMessage: p.first, Tmp: filepath.Join("/tmp", req.ID)}
	plan.Launch = providers.Launch{Path: req.Snapshot.Base.Command, Dir: req.Dir, SessionID: req.NewSessionID, Verify: p.verify}
	if req.ResumeSessionID != "" {
		plan.Launch.SessionID, plan.Launch.Resumed = req.ResumeSessionID, true
	}
	return plan, nil
}

// Restore keeps the requests the runtime gave back after a restart.
func (p *fakePrep) Restore(_ context.Context, req sessionzero.CharacterRequest) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.restored = append(p.restored, req)
	return nil
}

func (p *fakePrep) End(_ context.Context, r sessionzero.EndRequest) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ended = append(p.ended, r)
	return nil
}

func (p *fakePrep) Contexts() hook.Contexts { return fakeContexts{} }

func (p *fakePrep) TakeUpdate(string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	u := p.updates
	p.updates = ""
	return u
}

type fakeContexts struct{}

func (fakeContexts) Context(context.Context, providers.Event) (string, error) { return "", nil }

func (p *fakePrep) endedOf(id string) []sessionzero.EndRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []sessionzero.EndRequest
	for _, e := range p.ended {
		if e.ID == id {
			out = append(out, e)
		}
	}
	return out
}

// fakeHost is the terminals of a machine: processes the test drives.
type fakeHost struct {
	mu        sync.Mutex
	on        func(characters.HostEvent)
	procs     map[string]*fakeProc
	order     []*fakeProc
	report    characters.HostReport
	createErr error
	status    map[string]string
}

func newFakeHost() *fakeHost {
	return &fakeHost{procs: map[string]*fakeProc{}, status: map[string]string{}}
}

func (h *fakeHost) Create(_ context.Context, spec characters.HostSpec) (characters.Process, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.createErr != nil {
		return nil, h.createErr
	}
	p := &fakeProc{id: spec.ID, agent: spec.CharacterID, spec: spec, exited: make(chan struct{})}
	h.procs[spec.ID] = p
	h.order = append(h.order, p)
	return p, nil
}

func (h *fakeHost) Reconcile(context.Context) (characters.HostReport, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.report, nil
}

func (h *fakeHost) Events(f func(characters.HostEvent)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.on = f
}

func (h *fakeHost) SetStatus(_ context.Context, id, status string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.status[id] = status
	return nil
}

func (h *fakeHost) emit(e characters.HostEvent) {
	h.mu.Lock()
	on := h.on
	h.mu.Unlock()
	on(e)
}

// proc is the process of a character (the last one started for it).
func (h *fakeHost) proc(agent string) *fakeProc {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i := len(h.order) - 1; i >= 0; i-- {
		if h.order[i].agent == agent {
			return h.order[i]
		}
	}
	return nil
}

type fakeProc struct {
	id, agent string
	spec      characters.HostSpec

	mu       sync.Mutex
	typed    []string
	changed  chan struct{} // closed at the next Input (waitTyped)
	screen   characters.Screen
	stops    []stopCall
	exited   chan struct{}
	once     sync.Once
	detached bool
	inputErr error
}

type stopCall struct {
	graceful string
	wait     time.Duration
}

func (p *fakeProc) ID() string          { return p.id }
func (p *fakeProc) CharacterID() string { return p.agent }

func (p *fakeProc) Input(_ context.Context, data []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.inputErr != nil {
		return p.inputErr
	}
	p.typed = append(p.typed, string(data))
	if p.changed != nil {
		close(p.changed)
		p.changed = nil
	}
	return nil
}

// waitTyped waits until n inputs were typed (by another goroutine, maybe of
// another machine) and returns them; it fails the test when ctx ends first.
func (p *fakeProc) waitTyped(t testing.TB, ctx context.Context, n int) []string { //nolint:unused // used by the full suite
	t.Helper()
	for {
		p.mu.Lock()
		if len(p.typed) >= n {
			typed := slices.Clone(p.typed)
			p.mu.Unlock()
			return typed
		}
		if p.changed == nil {
			p.changed = make(chan struct{})
		}
		ch := p.changed
		p.mu.Unlock()
		select {
		case <-ch:
		case <-ctx.Done():
			t.Fatalf("waiting for %d input(s): %v; typed %q", n, ctx.Err(), p.typedAll())
		}
	}
}

func (p *fakeProc) Screen(context.Context) (characters.Screen, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.screen, nil
}

func (p *fakeProc) Stop(_ context.Context, graceful []byte, wait time.Duration) error {
	p.mu.Lock()
	p.stops = append(p.stops, stopCall{string(graceful), wait})
	p.mu.Unlock()
	p.once.Do(func() { close(p.exited) })
	return nil
}

func (p *fakeProc) Exited() <-chan struct{} { return p.exited }

func (p *fakeProc) PTY() characters.PTYInfo {
	return characters.PTYInfo{PID: 4000 + len(p.id), Started: "boot:" + p.id, Version: "1.0.0"}
}

func (p *fakeProc) Detach() {
	p.mu.Lock()
	p.detached = true
	p.mu.Unlock()
}

func (p *fakeProc) setScreen(text string, row int) {
	p.mu.Lock()
	p.screen = characters.Screen{Text: text, Row: row}
	p.mu.Unlock()
}

func (p *fakeProc) typedAll() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.typed)
}

func (p *fakeProc) stopCalls() []stopCall {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.stops)
}

// --- the rig ---

type rigOptions struct {
	machines []machineSpec
	limits   characters.Limits
	// first is the first message of the plan of every character.
	first string
	// tables checks the files of the table before each start.
	tables characters.TableChecker
	// connect gives the Service the real connections of the Guild database.
	connect bool
	// stage7 puts the real inbox and waits of the Citadel in the rig: the
	// deliveries are typed in the terminals by the Runtime, and the turns
	// answer the asks. The worktrees are real folders.
	stage7 bool
	// longText is the size above which a delivery goes to a file (the
	// default of the document when zero).
	longText int
	// classes checks the class of each request (nil: not checked).
	classes characters.ClassCheck
}

type machineRig struct {
	id   string
	rt   *characters.Runtime
	host *fakeHost
	prep *fakePrep
	db   *store.DB
}

// reportTap hands the reports of the machines to the Citadel and wakes whoever
// waits for the next one.
type reportTap struct {
	svc *characters.Service
	mu  sync.Mutex
	ch  chan struct{}
}

func (w *reportTap) Report(ctx context.Context, r characters.Report) error {
	err := w.svc.Report(ctx, r)
	w.mu.Lock()
	close(w.ch)
	w.ch = make(chan struct{})
	w.mu.Unlock()
	return err
}

// next is closed by the next report.
func (w *reportTap) next() <-chan struct{} {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.ch
}

// rig is a Citadel and the machines of a Guild, with the fakes of what the
// next stages bring.
type rig struct {
	t        *testing.T
	ctx      context.Context
	clk      *clock.Fake
	gdb      *store.DB
	reg      characters.Registry
	cat      *presets.Catalog
	sheets   *fakeMachineSheets
	svc      *characters.Service
	conns    *connections.Service
	notes    *noticeLog
	scrolls  *fakeScrolls
	tables   *fakeTables
	inbox    *fakeInbox
	waits    *fakeWaits
	builder  *fakeBuilder
	tap      *reportTap
	ib       *inbox.Service
	wt       *waits.Service
	mach     map[string]*machineRig
	machines characters.MachineSet
}

const workspace = "meu-app"

var engine = characters.Creator{Kind: characters.CreatedByEngine}
var owner = characters.Creator{Kind: characters.CreatedByUser, ID: "lucas", Level: characters.LevelOwner}

func newRig(t *testing.T, o rigOptions) *rig {
	t.Helper()
	if len(o.machines) == 0 {
		o.machines = []machineSpec{linux("linux")}
	}
	root := ""
	if o.stage7 {
		root = t.TempDir()
		o.connect = true
	}
	r := &rig{t: t, ctx: context.Background(), clk: clock.NewFake(day0), sheets: &fakeMachineSheets{sheets: map[string]machinesheet.Sheet{}},
		notes: &noticeLog{}, scrolls: &fakeScrolls{}, inbox: &fakeInbox{pending: map[string]int{}}, waits: &fakeWaits{by: map[string]string{}},
		builder: &fakeBuilder{last: map[string]characters.BuildInput{}}, mach: map[string]*machineRig{}, machines: characters.MachineSet{}}
	r.tables = &fakeTables{root: root, tables: map[string]characters.TableInfo{
		"3-login": {ID: "3-login", Workspace: workspace, State: characters.TableWorking, Worktrees: map[string]string{}},
		"4-api":   {ID: "4-api", Workspace: workspace, State: characters.TableWorking, Worktrees: map[string]string{}},
	}}
	r.gdb = openCopy(t, "mestre.db", guildTemplate, guilddb.Open)
	r.reg = characters.Registry{DB: r.gdb}
	if _, err := r.gdb.Writer().Exec(`INSERT INTO workspaces (slug, repo, repo_key, clone_url, default_branch, state, stage, created_at, updated_at)
		VALUES (?, 'lucas/meu-app', 'lucas/meu-app', 'https://example.test/x.git', 'main', 'active', '', 1, 1)`, workspace); err != nil {
		t.Fatal(err)
	}
	r.cat = &presets.Catalog{DB: r.gdb, Clock: r.clk, Machines: r.sheets, Users: r.reg}
	doc, err := presets.ParseDocument([]byte(presetsDoc))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := r.cat.PlanImport(r.ctx, doc)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.cat.ApplyImport(r.ctx, plan, nil); err != nil {
		t.Fatal(err)
	}
	for _, m := range o.machines {
		r.sheets.set(sheetOf(t, m.fake, m.id, m.id, true))
	}
	so := characters.ServiceOptions{
		Registry: r.reg, Clock: r.clk, Presets: characters.CatalogPresets{Catalog: r.cat}, Tables: r.tables, Machines: r.machines,
		Builder: r.builder, Notifier: r.notes, Scrolls: r.scrolls, Inbox: r.inbox, Limits: o.limits,
	}
	so.Classes = o.classes
	if o.connect {
		r.conns, err = connections.NewService(connections.Options{Registry: r.reg, Clock: r.clk})
		if err != nil {
			t.Fatal(err)
		}
		so.Connections = r.conns
	}
	var inboxProbe providers.Inbox
	var waitProbe characters.WaitProbe = r.waits
	if o.stage7 {
		r.ib, err = inbox.NewService(inbox.Options{DB: &inbox.Store{DB: r.gdb}, Clock: r.clk, Registry: r.reg, Deliverer: r.machines})
		if err != nil {
			t.Fatal(err)
		}
		r.wt, err = waits.NewService(waits.Options{Store: &waits.Store{DB: r.gdb}, Clock: r.clk, Registry: r.reg, Inbox: r.ib, Notifier: r.notes,
			Connections: r.conns, Terminals: r.machines})
		if err != nil {
			t.Fatal(err)
		}
		so.Observer, so.Inbox = characters.Observers{r.ib, r.wt}, r.ib
		inboxProbe, waitProbe = r.ib, r.wt
	}
	svc, err := characters.NewService(so)
	if err != nil {
		t.Fatal(err)
	}
	r.svc = svc
	r.tap = &reportTap{svc: svc, ch: make(chan struct{})}
	for _, m := range o.machines {
		mr := &machineRig{id: m.id, host: newFakeHost(), prep: &fakePrep{first: o.first}}
		mr.db = openCopy(t, "maquina.db", machineTemplate, machinedb.Open)
		mr.rt, err = characters.NewRuntime(characters.RuntimeOptions{
			Machine: m.id, Clock: r.clk, DB: mr.db, Host: mr.host, Prep: mr.prep, Providers: providers.NewRegistry(providers.Options{}),
			Reporter: r.tap, Notifier: r.notes, Waits: waitProbe, Tables: o.tables, Inbox: inboxProbe, Turns: r.turnObserver(o.stage7), LongText: o.longText,
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(mr.rt.Close)
		r.mach[m.id] = mr
		r.machines[m.id] = mr.rt
	}
	return r
}

// recruit is a recruit by the engine for a step of a quest, or by the creator
// given.
func (r *rig) create(req characters.CreateRequest) (characters.CreateResult, error) {
	r.t.Helper()
	if req.Workspace == "" {
		req.Workspace = workspace
	}
	if req.Creator.Kind == "" {
		req.Creator = engine
	}
	return r.svc.Create(r.ctx, req)
}

// make creates a character and fails the test if it does not run now.
func (r *rig) make(sheet, name, table string) characters.Character {
	r.t.Helper()
	res, err := r.create(characters.CreateRequest{Name: name, Sheet: sheet, Table: table})
	if err != nil {
		r.t.Fatalf("create %s: %v", name, err)
	}
	if res.Pending != "" {
		r.t.Fatalf("create %s: pending", name)
	}
	return res.Character
}

// get reads a character from the registry.
func (r *rig) get(id string) characters.Character {
	r.t.Helper()
	c, err := r.reg.Get(r.ctx, id)
	if err != nil {
		r.t.Fatal(err)
	}
	return c
}

// runtimeOf is the runtime of the machine a character runs on.
func (r *rig) machineOf(c characters.Character) *machineRig {
	r.t.Helper()
	m := r.mach[c.Machine]
	if m == nil {
		r.t.Fatalf("no machine %q", c.Machine)
	}
	return m
}

// event makes the normalized event of a hook of a character.
func event(c characters.Character, kind providers.EventKind) providers.Event {
	prov := providers.ClaudeCode
	if c.Sheet == "Revisor Codex" {
		prov = providers.Codex
	}
	return providers.Event{Version: providers.EventVersion, Provider: prov, Event: kind, AgentID: c.ID, TableID: c.Table, SessionID: "sess-" + c.ID, TurnID: "1"}
}

// hook delivers an event of the hook engine to the runtime of the character.
func (r *rig) hook(c characters.Character, kind providers.EventKind) {
	r.t.Helper()
	r.machineOf(c).rt.OnHook(event(c, kind), hook.Answer{})
}

// state is the state of the character in the registry.
func (r *rig) state(c characters.Character) characters.State { return r.get(c.ID).State }

// ready brings a character from starting to idle, as its hook does.
func (r *rig) ready(c characters.Character) {
	r.t.Helper()
	r.hook(c, providers.EventSessionStart)
	if got := r.state(c); got != characters.Idle {
		r.t.Fatalf("%s is %s after session_start, want idle", c.Name, got)
	}
}

// busy starts a turn and leaves the character busy.
func (r *rig) busy(c characters.Character) {
	r.t.Helper()
	r.hook(c, providers.EventTurnStart)
	if got := r.state(c); got != characters.Busy {
		r.t.Fatalf("%s is %s after turn_start, want busy", c.Name, got)
	}
}

// idle ends the turn and leaves the character idle.
func (r *rig) idle(c characters.Character) {
	r.t.Helper()
	r.hook(c, providers.EventTurnEnd)
	if got := r.state(c); got != characters.Idle {
		r.t.Fatalf("%s is %s after turn_end, want idle", c.Name, got)
	}
}

// advance moves the clock and runs the periodic work of the Citadel.
func (r *rig) advance(d time.Duration) {
	r.t.Helper()
	r.clk.Advance(d)
	if err := r.svc.Sweep(r.ctx); err != nil {
		r.t.Fatal(err)
	}
}

// states lists the states a character went through, in order, from the
// history.
func (r *rig) path(c characters.Character) []characters.State {
	r.t.Helper()
	log, err := r.reg.Log(r.ctx, c.ID)
	if err != nil {
		r.t.Fatal(err)
	}
	var out []characters.State
	for _, l := range log {
		out = append(out, l.To)
	}
	return out
}

// claudeReady is the screen of Claude Code at its prompt.
const claudeReady = "Claude Code\n\n──────────────────────────────\n❯ \n──────────────────────────────\n  ? for shortcuts"

type sheetEdit = presets.Sheet

// mustSheet reads a sheet of the catalog and changes it.
func mustSheet(t *testing.T, r *rig, name string, edit func(*sheetEdit)) presets.Sheet {
	t.Helper()
	s, ok, err := r.cat.Sheet(r.ctx, name)
	if err != nil || !ok {
		t.Fatalf("sheet %s: %v %v", name, ok, err)
	}
	edit(&s)
	return s
}

// turnObserver is the Citadel's end of the turns for the runtimes: the Service,
// once it exists (the runtimes are made after it).
func (r *rig) turnObserver(on bool) characters.TurnObserver {
	if !on {
		return nil
	}
	return r.svc
}
