package usage_test

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/i18n"
	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/orchestration/usage"
	"github.com/mestre-ia/mestre/internal/providers"
	"github.com/mestre-ia/mestre/internal/store"
	"github.com/mestre-ia/mestre/internal/testkit/guildtpl"
	"github.com/mestre-ia/mestre/pkg/clock"
)

var day0 = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)

const ceilingYAML = `version: 1
permissions:
  git:
    request_push: false
    workflows: never
cost:
  usd:
    per_table: 12
    per_day: 30
`

// fakeDirectory is the registry and the sheets.
type fakeDirectory struct {
	mu    sync.Mutex
	by    map[string]usage.Subject
	alts  map[string][]string
	steps int
}

func (d *fakeDirectory) Character(_ context.Context, id string) (usage.Subject, bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	s, ok := d.by[id]
	return s, ok, nil
}

func (d *fakeDirectory) Alternatives(_ context.Context, s usage.Subject) ([]string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.alts[s.ID], nil
}

func (d *fakeDirectory) PendingSteps(context.Context, string, string) (int, error) {
	return d.steps, nil
}

// fakePauser records what is paused.
type fakePauser struct {
	mu     sync.Mutex
	events []string
	spaces []string
}

func (p *fakePauser) add(s string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, s)
}

func (p *fakePauser) PauseQuest(_ context.Context, q, _ string) error {
	p.add("pause quest " + q)
	return nil
}
func (p *fakePauser) ResumeQuest(_ context.Context, q string) error {
	p.add("resume quest " + q)
	return nil
}
func (p *fakePauser) PauseTable(_ context.Context, ws, t, _ string) error {
	p.add("pause table " + ws + "/" + t)
	return nil
}
func (p *fakePauser) ResumeTable(_ context.Context, ws, t string) error {
	p.add("resume table " + ws + "/" + t)
	return nil
}
func (p *fakePauser) PauseWorkspace(_ context.Context, ws string) (bool, error) {
	p.add("pause workspace " + ws)
	return true, nil
}
func (p *fakePauser) ResumeWorkspace(_ context.Context, ws string) error {
	p.add("resume workspace " + ws)
	return nil
}
func (p *fakePauser) Workspaces(context.Context) ([]string, error) { return p.spaces, nil }

func (p *fakePauser) got() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.events...)
}

// fakeChars records the blocks.
type fakeChars struct {
	mu     sync.Mutex
	blocks []string
	frees  []string
}

func (c *fakeChars) Block(_ context.Context, id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.blocks = append(c.blocks, id)
	return nil
}

func (c *fakeChars) Unblock(_ context.Context, id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.frees = append(c.frees, id)
	return nil
}

type fakePump struct {
	mu sync.Mutex
	n  int
}

func (p *fakePump) PumpAll(context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.n++
	return nil
}

func (p *fakePump) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.n
}

// tellings collects what the people, the Grand Master and the masters hear.
type tellings struct {
	mu     sync.Mutex
	alerts []string
	msgs   []i18n.Msg // the same alerts, as messages
	notes  []characters.Notice
}

func (t *tellings) Notify(_ context.Context, workspace string, msg i18n.Msg) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.alerts = append(t.alerts, workspace+": "+i18n.Render(i18n.PtBR, msg))
	t.msgs = append(t.msgs, msg)
	return nil
}

func (t *tellings) notify(_ context.Context, n characters.Notice) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.notes = append(t.notes, n)
	return nil
}

type notifier struct{ t *tellings }

func (n notifier) Notify(ctx context.Context, x characters.Notice) error { return n.t.notify(ctx, x) }

func (t *tellings) alertsWith(sub string) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	n := 0
	for _, a := range t.alerts {
		if strings.Contains(a, sub) {
			n++
		}
	}
	return n
}

func (t *tellings) noteTo(kind characters.RecipientKind, sub string) []characters.Notice {
	t.mu.Lock()
	defer t.mu.Unlock()
	var out []characters.Notice
	for _, n := range t.notes {
		if n.To.Kind == kind && strings.Contains(n.Text, sub) {
			out = append(out, n)
		}
	}
	return out
}

// fakeExtras is the chain of approval: it keeps the requests it was asked for.
type fakeExtras struct {
	mu   sync.Mutex
	reqs []usage.ExtraRequest
}

func (e *fakeExtras) OpenExtra(_ context.Context, r usage.ExtraRequest) (int64, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.reqs = append(e.reqs, r)
	return int64(len(e.reqs)), nil
}

func (e *fakeExtras) all() []usage.ExtraRequest {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]usage.ExtraRequest(nil), e.reqs...)
}

type fakeChecklist struct {
	mu    sync.Mutex
	codes []string
}

func (c *fakeChecklist) Raise(_ context.Context, code, subject, _ string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.codes = append(c.codes, code+" "+subject)
	return nil
}

type fixedCeiling struct{ c *classes.Ceiling }

func (f fixedCeiling) Ceiling(context.Context, string) (*classes.Ceiling, error) { return f.c, nil }

// world is a Service over a real Guild database and fakes for the rest.
type world struct {
	t     *testing.T
	ctx   context.Context
	clk   *clock.Fake
	svc   *usage.Service
	dir   *fakeDirectory
	pause *fakePauser
	chars *fakeChars
	pump  *fakePump
	said  *tellings
	extra *fakeExtras
	check *fakeChecklist
}

func newWorld(t *testing.T, opts ...func(*usage.Options)) *world {
	t.Helper()
	ctx := context.Background()
	db, err := guildtpl.Open(ctx, filepath.Join(t.TempDir(), "mestre.db"), store.Options{Synchronous: "OFF"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ceiling, err := classes.ParseCeiling([]byte(ceilingYAML))
	if err != nil {
		t.Fatal(err)
	}
	w := &world{t: t, ctx: ctx, clk: clock.NewFake(day0), dir: &fakeDirectory{by: map[string]usage.Subject{}, alts: map[string][]string{}},
		pause: &fakePauser{}, chars: &fakeChars{}, pump: &fakePump{}, said: &tellings{}, extra: &fakeExtras{}, check: &fakeChecklist{}}
	o := usage.Options{Store: &usage.Store{DB: db}, Clock: w.clk, Directory: w.dir, Pauser: w.pause, Ceilings: fixedCeiling{ceiling},
		Characters: w.chars, Pump: w.pump, Notifier: notifier{w.said}, Alerts: w.said, Extras: w.extra, Checklist: w.check}
	for _, f := range opts {
		f(&o)
	}
	w.svc, err = usage.NewService(o)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

// character registers a character of a table "login" of the workspace "loja".
func (w *world) character(id, name string, role characters.Role, p providers.ID) usage.Subject {
	s := usage.Subject{ID: id, Name: name, Role: role, Workspace: "loja", Table: "t1", TableName: "Login", Quest: "q1", QuestName: "Entrar", Step: "q1/dev",
		Provider: p, Profile: "pessoal", Class: "dev", Sheet: name}
	w.dir.by[id] = s
	return s
}

func usd(v float64) *float64 { return &v }

// stepRecord is a record of the work of a step in the table.
func stepRecord(w *world, cost float64) usage.Record {
	return usage.Record{Character: "c1", Step: "q1/dev", Quest: "q1", Workspace: "loja", Table: "t1", QuestName: "Entrar", TableName: "Login", Profile: "pessoal",
		Provider: providers.ClaudeCode, At: w.clk.Now(), Source: "statusline", Origin: usage.OriginStep, CostUSD: usd(cost),
		Tokens: usage.Tokens{Input: 1000, Output: 200, Known: true}}
}
