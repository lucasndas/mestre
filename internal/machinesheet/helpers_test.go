package machinesheet_test

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/guild"
	"github.com/mestre-ia/mestre/internal/machinesheet"
	"github.com/mestre-ia/mestre/internal/protocol"
	"github.com/mestre-ia/mestre/internal/store"
	"github.com/mestre-ia/mestre/internal/testkit/guildtpl"
	"github.com/mestre-ia/mestre/pkg/clock"
)

var day0 = time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)

// The Guild database is migrated once, by TestMain; every test starts from a
// copy (creating and migrating a file costs tens of milliseconds on a Windows
// runner). The schema itself is tested in internal/store.
var guildTemplate []byte

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "machinesheet-template")
	if err != nil {
		fmt.Fprintln(os.Stderr, "machinesheet:", err)
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
		fmt.Fprintln(os.Stderr, "machinesheet: template:", err)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// newGuildDB opens a copy of the migrated Guild database.
func newGuildDB(t *testing.T) *store.DB {
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
	return db
}

// addMachine writes a row of the registry, as the entry in the Guild does.
func addMachine(t *testing.T, db *store.DB, id, name, instance string) {
	t.Helper()
	_, err := db.Writer().Exec(
		`INSERT INTO machines (machine_id, name, instance, priority, excluded, state, names, joined_at) VALUES (?, ?, ?, 0, 0, 'active', '{"hostname":"x"}', ?)`,
		id, name, instance, day0.UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
}

// presence is the connection state the tests decide.
type presence struct {
	mu sync.Mutex
	on map[string]bool
}

func newPresence(online ...string) *presence {
	p := &presence{on: map[string]bool{}}
	for _, id := range online {
		p.on[id] = true
	}
	return p
}

func (p *presence) Connected(id string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.on[id]
}

func (p *presence) set(id string, v bool) {
	p.mu.Lock()
	p.on[id] = v
	p.mu.Unlock()
}

// names is a machinesheet.NameBook that records the updates.
type names struct {
	mu      sync.Mutex
	machine guild.Machine
	updates []string
}

func (n *names) Machine(context.Context, string) (guild.Machine, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.machine, nil
}

func (n *names) UpdateNames(_ context.Context, id, host string, ips []net.IP) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.updates = append(n.updates, fmt.Sprintf("%s %s %v", id, host, ips))
	return nil
}

func (n *names) got() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]string(nil), n.updates...)
}

// recorder is a Sender that keeps what the agent sends.
type recorder struct {
	mu   sync.Mutex
	sent []protocol.Object
	err  error
	ch   chan protocol.Object
}

func (r *recorder) Send(_ context.Context, o protocol.Outgoing) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return "", r.err
	}
	if o.Type != protocol.TypeMachineSheet {
		return "", fmt.Errorf("sent %s", o.Type)
	}
	patch := o.Payload.(protocol.Object)
	r.sent = append(r.sent, patch)
	if r.ch != nil {
		r.ch <- patch
	}
	return "id", nil
}

func (r *recorder) patches() []protocol.Object {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]protocol.Object(nil), r.sent...)
}

func (r *recorder) fail(err error) {
	r.mu.Lock()
	r.err = err
	r.mu.Unlock()
}

// keys lists the fields of a patch without the revision.
func keys(o protocol.Object) []string {
	var out []string
	for k := range o {
		if k != "rev" {
			out = append(out, k)
		}
	}
	slices.Sort(out)
	return out
}

// next waits for the next patch.
func (r *recorder) next(t *testing.T) protocol.Object {
	t.Helper()
	return <-r.ch
}

func newAgent(t *testing.T, clk clock.Clock, sources machinesheet.Sources, opts ...func(*machinesheet.AgentConfig)) *machinesheet.Agent {
	t.Helper()
	b, err := machinesheet.NewBuilder(sources, "tower1", "")
	if err != nil {
		t.Fatal(err)
	}
	cfg := machinesheet.AgentConfig{Builder: b, Clock: clk}
	for _, o := range opts {
		o(&cfg)
	}
	a, err := machinesheet.NewAgent(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return a
}
