package waits_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/connections"
	"github.com/lucasndas/mestre/internal/orchestration/inbox"
	"github.com/lucasndas/mestre/internal/orchestration/waits"
	"github.com/lucasndas/mestre/internal/protocol"
	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/internal/testkit/guildtpl"
	"github.com/lucasndas/mestre/pkg/clock"
)

var day0 = time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)

// The Guild database is migrated once, by TestMain; every test starts from a
// copy (docs/qualidade/testes.md).
var guildTemplate []byte

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "waits-template")
	if err != nil {
		fmt.Fprintln(os.Stderr, "waits:", err)
		os.Exit(1)
	}
	path := filepath.Join(dir, "mestre.db")
	db, err := guildtpl.Open(context.Background(), path, store.Options{Synchronous: "OFF"})
	if err == nil {
		err = db.Close()
	}
	if err == nil {
		guildTemplate, err = os.ReadFile(path)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "waits: template:", err)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

const workspace = "meu-app"

// fakeDeliverer is the machines: it records what is typed and, like the
// runtime, puts the character to work.
type fakeDeliverer struct {
	mu  sync.Mutex
	w   *world
	got []protocol.InboxDeliver
}

func (f *fakeDeliverer) Deliver(_ context.Context, _ string, d protocol.InboxDeliver) (protocol.InboxResult, error) {
	f.mu.Lock()
	f.got = append(f.got, d)
	f.mu.Unlock()
	if c, ok := f.w.byTerminal(d.TerminalID); ok {
		f.w.set(c, characters.Busy)
	}
	return protocol.InboxResult{ItemID: d.ItemID, Status: protocol.InboxDelivered}, nil
}

// typedTo lists the texts delivered to a character.
func (f *fakeDeliverer) typedTo(c characters.Character) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, d := range f.got {
		if d.TerminalID == c.TerminalID {
			out = append(out, d.Text)
		}
	}
	return out
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

// fakeQuests is the quests of stage 9.
type fakeQuests struct {
	mu    sync.Mutex
	by    map[string]waits.QuestInfo
	owner map[string]string
}

func (f *fakeQuests) Quest(_ context.Context, _, _, name string) (waits.QuestInfo, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	q, ok := f.by[name]
	return q, ok, nil
}

func (f *fakeQuests) Responsible(_ context.Context, step string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.owner[step], nil
}

type fakeTerminals struct {
	mu     sync.Mutex
	typed  map[string][]byte
	err    map[string]error
	screen map[string]characters.Screen
}

func (f *fakeTerminals) Input(_ context.Context, _, character string, data []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.err[character]; err != nil {
		return err
	}
	f.typed[character] = append(f.typed[character], data...)
	return nil
}

func (f *fakeTerminals) Screen(_ context.Context, _, character string) (characters.Screen, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.screen[character], nil
}

func (f *fakeTerminals) typedIn(character string) []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]byte(nil), f.typed[character]...)
}

type fakeWaker struct {
	mu   sync.Mutex
	woke []string
}

func (f *fakeWaker) WaitResolved(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.woke = append(f.woke, id)
	return nil
}

// world is a Guild with characters, an inbox, the connections and the waits.
type world struct {
	t      *testing.T
	ctx    context.Context
	clk    *clock.Fake
	db     *store.DB
	reg    characters.Registry
	in     *inbox.Service
	conns  *connections.Service
	svc    *waits.Service
	deliv  *fakeDeliverer
	notes  *noticeLog
	quests *fakeQuests
	terms  *fakeTerminals
	waker  *fakeWaker
	n      int
}

func newWorld(t *testing.T) *world {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mestre.db")
	if err := os.WriteFile(path, guildTemplate, 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := guildtpl.Open(context.Background(), path, store.Options{Synchronous: "OFF"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Writer().Exec(`INSERT INTO workspaces (slug, repo, repo_key, clone_url, default_branch, state, stage, created_at, updated_at)
		VALUES (?, 'lucas/meu-app', 'lucas/meu-app', 'https://example.test/x.git', 'main', 'active', '', 1, 1)`, workspace); err != nil {
		t.Fatal(err)
	}
	return newWorldOver(t, db, clock.NewFake(day0))
}

// newWorldOver builds the services over a database: a restart of the engine
// builds them again over the same one.
func newWorldOver(t *testing.T, db *store.DB, clk *clock.Fake) *world {
	t.Helper()
	w := &world{t: t, ctx: context.Background(), clk: clk, db: db, reg: characters.Registry{DB: db}, notes: &noticeLog{},
		quests: &fakeQuests{by: map[string]waits.QuestInfo{}, owner: map[string]string{}},
		terms:  &fakeTerminals{typed: map[string][]byte{}, err: map[string]error{}, screen: map[string]characters.Screen{}}, waker: &fakeWaker{}}
	w.deliv = &fakeDeliverer{w: w}
	var err error
	w.in, err = inbox.NewService(inbox.Options{DB: &inbox.Store{DB: db}, Clock: clk, Registry: w.reg, Deliverer: w.deliv})
	if err != nil {
		t.Fatal(err)
	}
	w.conns, err = connections.NewService(connections.Options{Registry: w.reg, Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	w.svc, err = waits.NewService(waits.Options{Store: &waits.Store{DB: db}, Clock: clk, Registry: w.reg, Inbox: w.in, Notifier: w.notes, Connections: w.conns,
		Quests: w.quests, Waker: w.waker, Terminals: w.terms})
	if err != nil {
		t.Fatal(err)
	}
	return w
}

// add registers an idle character, with a terminal on the machine "m1".
func (w *world) add(role characters.Role, table, name string) characters.Character {
	w.t.Helper()
	w.n++
	w.clk.Advance(time.Millisecond)
	c := characters.Character{
		ID: fmt.Sprintf("c%02d", w.n), Name: name, Kind: characters.KindCharacter, Role: role, Workspace: workspace, Table: table,
		Machine: "m1", TerminalID: fmt.Sprintf("t%02d", w.n), Sheet: "Dev Claude", Base: "claude-code", Class: "Dev", State: characters.Idle, Lifecycle: characters.Persistent,
		CreatedBy: characters.Creator{Kind: characters.CreatedByEngine}, CreatedAt: w.clk.Now(),
	}
	if err := w.reg.Insert(w.ctx, c); err != nil {
		w.t.Fatal(err)
	}
	return c
}

func (w *world) gm() characters.Character {
	return w.add(characters.RoleGrandMaster, "", characters.NameGrandMaster)
}

// dev is a character of the table, connected to the other ones given.
func (w *world) dev(table, name string, with ...characters.Character) characters.Character {
	c := w.add(characters.RoleCharacter, table, name)
	for _, o := range with {
		if err := w.conns.Link(w.ctx, workspace, table, connections.Char(c.ID), connections.Char(o.ID), ""); err != nil {
			w.t.Fatal(err)
		}
	}
	return c
}

func (w *world) byTerminal(term string) (characters.Character, bool) {
	all, err := w.reg.Alive(w.ctx)
	if err != nil {
		w.t.Error(err)
		return characters.Character{}, false
	}
	for _, c := range all {
		if c.TerminalID == term {
			return c, true
		}
	}
	return characters.Character{}, false
}

// set moves a character to a state the way a report of its machine does, and
// tells the Observers (the inbox, then the waits).
func (w *world) set(c characters.Character, to characters.State) {
	w.t.Helper()
	w.setWith(c, to, "test")
}

func (w *world) setWith(c characters.Character, to characters.State, reason string) {
	w.t.Helper()
	from, changed, err := w.reg.Transition(w.ctx, c.ID, to, characters.Change{Reason: reason, At: w.clk.Now()})
	if err != nil {
		w.t.Error(err)
		return
	}
	if !changed {
		return
	}
	fresh, err := w.reg.Get(w.ctx, c.ID)
	if err != nil {
		w.t.Error(err)
		return
	}
	if err := (characters.Observers{w.in, w.svc}).StateChanged(w.ctx, fresh, from); err != nil {
		w.t.Error(err)
	}
}

// done is the done of a character, as the Service of the characters reports it.
func (w *world) done(c characters.Character, text string) {
	w.t.Helper()
	if err := (characters.Observers{w.in, w.svc}).Done(w.ctx, c, text); err != nil {
		w.t.Fatal(err)
	}
}

// turnEnds is the end of a turn: the Runtime tells the Citadel first, then
// the state changes.
func (w *world) turnEnds(c characters.Character, last string) {
	w.t.Helper()
	if err := (characters.Observers{w.in, w.svc}).TurnEnded(w.ctx, c, last); err != nil {
		w.t.Fatal(err)
	}
	w.set(c, characters.Idle)
}

// inboxOf lists the items that wait for a character.
func (w *world) inboxOf(c characters.Character) []inbox.Item {
	w.t.Helper()
	items, err := w.in.List(w.ctx, c.ID)
	if err != nil {
		w.t.Fatal(err)
	}
	return items
}

// enqueue puts an item in an inbox.
func (w *world) enqueue(n inbox.NewItem) inbox.Item {
	w.t.Helper()
	if n.Workspace == "" {
		n.Workspace = workspace
	}
	it, err := w.in.Enqueue(w.ctx, n)
	if err != nil {
		w.t.Fatal(err)
	}
	return it
}

// all lists every item a character ever got.
func (w *world) all(c characters.Character) []inbox.Item {
	w.t.Helper()
	items, err := w.in.Store().All(w.ctx, c.ID)
	if err != nil {
		w.t.Fatal(err)
	}
	return items
}
