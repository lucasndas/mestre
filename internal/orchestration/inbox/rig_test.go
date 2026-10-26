package inbox_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/orchestration/inbox"
	"github.com/mestre-ia/mestre/internal/protocol"
	"github.com/mestre-ia/mestre/internal/store"
	"github.com/mestre-ia/mestre/internal/testkit/guildtpl"
	"github.com/mestre-ia/mestre/pkg/clock"
)

var day0 = time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)

// The Guild database is migrated once, by TestMain; every test starts from a
// copy (docs/qualidade/testes.md).
var guildTemplate []byte

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "inbox-template")
	if err != nil {
		fmt.Fprintln(os.Stderr, "inbox:", err)
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
		fmt.Fprintln(os.Stderr, "inbox: template:", err)
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
	mu   sync.Mutex
	w    *world
	got  []protocol.InboxDeliver
	busy map[string]bool
	err  error
	// cut is called while an item is being typed, as a cut of the connection the call came in
	// would: it cancels the context of the caller, and the delivery ends with its error.
	cut func()
}

func (f *fakeDeliverer) Deliver(ctx context.Context, _ string, d protocol.InboxDeliver) (protocol.InboxResult, error) {
	f.mu.Lock()
	if f.err != nil {
		err := f.err
		f.mu.Unlock()
		return protocol.InboxResult{}, err
	}
	if f.cut != nil {
		cut := f.cut
		f.mu.Unlock()
		cut()
		return protocol.InboxResult{}, ctx.Err()
	}
	if f.busy[d.TerminalID] {
		f.mu.Unlock()
		return protocol.InboxResult{ItemID: d.ItemID, Status: protocol.InboxBusy}, nil
	}
	f.got = append(f.got, d)
	f.mu.Unlock()
	if c, ok := f.w.byTerminal(d.TerminalID); ok {
		f.w.set(c, characters.Busy) // the machine reports the character at work
	}
	return protocol.InboxResult{ItemID: d.ItemID, Status: protocol.InboxDelivered}, nil
}

// ids are the items delivered so far, in order.
func (f *fakeDeliverer) ids() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.got))
	for i, d := range f.got {
		out[i] = d.ItemID
	}
	return out
}

type fakeChannels struct {
	mu   sync.Mutex
	down bool
	sent []inbox.Outbound
}

func (f *fakeChannels) Send(_ context.Context, o inbox.Outbound) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down {
		return inbox.ErrChannelDown
	}
	f.sent = append(f.sent, o)
	return nil
}

func (f *fakeChannels) setDown(v bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.down = v
}

func (f *fakeChannels) all() []inbox.Outbound {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]inbox.Outbound(nil), f.sent...)
}

type fakeUsers struct {
	mu    sync.Mutex
	owner []string
	user  []string
}

func (f *fakeUsers) NotifyOwner(_ context.Context, _, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.owner = append(f.owner, text)
	return nil
}

func (f *fakeUsers) NotifyUser(_ context.Context, _, user, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.user = append(f.user, user+": "+text)
	return nil
}

type fakePauses struct {
	mu     sync.Mutex
	paused map[string]bool
}

func (f *fakePauses) Paused(_ context.Context, _, table string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.paused[table], nil
}

func (f *fakePauses) set(table string, v bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paused[table] = v
}

// world is a Guild with a registry of characters and the inbox over it.
type world struct {
	t      *testing.T
	ctx    context.Context
	clk    *clock.Fake
	db     *store.DB
	reg    characters.Registry
	svc    *inbox.Service
	deliv  *fakeDeliverer
	chans  *fakeChannels
	users  *fakeUsers
	pauses *fakePauses
	n      int
}

func newWorld(t *testing.T) *world { return newWorldMax(t, 0) }

// newWorldMax is a world whose inboxes warn above max items (the default of the
// document when zero).
func newWorldMax(t *testing.T, max int) *world {
	t.Helper()
	w := &world{t: t, ctx: context.Background(), clk: clock.NewFake(day0), chans: &fakeChannels{}, users: &fakeUsers{}, pauses: &fakePauses{paused: map[string]bool{}}}
	path := filepath.Join(t.TempDir(), "mestre.db")
	if err := os.WriteFile(path, guildTemplate, 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := guildtpl.Open(w.ctx, path, store.Options{Synchronous: "OFF"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	w.db = db
	if _, err := db.Writer().Exec(`INSERT INTO workspaces (slug, repo, repo_key, clone_url, default_branch, state, stage, created_at, updated_at)
		VALUES (?, 'lucas/meu-app', 'lucas/meu-app', 'https://example.test/x.git', 'main', 'active', '', 1, 1)`, workspace); err != nil {
		t.Fatal(err)
	}
	w.reg = characters.Registry{DB: db}
	w.deliv = &fakeDeliverer{w: w, busy: map[string]bool{}}
	w.svc, err = inbox.NewService(inbox.Options{DB: &inbox.Store{DB: db}, Clock: w.clk, Registry: w.reg, Deliverer: w.deliv, Pauses: w.pauses, Channels: w.chans, Users: w.users, MaxQueue: max})
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

func (w *world) dev(table, name string) characters.Character {
	return w.add(characters.RoleCharacter, table, name)
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

// set moves a character to a state the way a report of its machine does: the
// registry first, then the Observer.
func (w *world) set(c characters.Character, to characters.State) {
	w.t.Helper()
	from, changed, err := w.reg.Transition(w.ctx, c.ID, to, characters.Change{Reason: "test", At: w.clk.Now()})
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
	if err := w.svc.StateChanged(w.ctx, fresh, from); err != nil {
		w.t.Error(err)
	}
}

func (w *world) enqueue(n inbox.NewItem) inbox.Item {
	w.t.Helper()
	if n.Workspace == "" {
		n.Workspace = workspace
	}
	it, err := w.svc.Enqueue(w.ctx, n)
	if err != nil {
		w.t.Fatal(err)
	}
	return it
}

// user is a message of a user to the Grand Master.
func (w *world) user(gm characters.Character, level characters.UserLevel, name, text string) inbox.Item {
	return w.enqueue(inbox.NewItem{Target: gm.ID, Kind: inbox.KindUser, Sender: name, Text: text,
		Origin: &inbox.Origin{Channel: inbox.ChannelTelegram, User: name, UserName: name, Level: level, Conversation: "chat-" + name, Message: "tg-" + text}})
}
