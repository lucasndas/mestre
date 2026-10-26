package presets_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/machinesheet"
	"github.com/mestre-ia/mestre/internal/orchestration/presets"
	"github.com/mestre-ia/mestre/internal/store"
	"github.com/mestre-ia/mestre/internal/testkit/guildtpl"
	"github.com/mestre-ia/mestre/pkg/clock"
)

var day0 = time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)

// The Guild database is migrated once, by TestMain; every test starts from a
// copy (docs/qualidade/testes.md: creating and migrating a file is slow on a
// Windows runner).
var guildTemplate []byte

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "presets-template")
	if err != nil {
		fmt.Fprintln(os.Stderr, "presets:", err)
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
		fmt.Fprintln(os.Stderr, "presets: template:", err)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// fixture is a Catalog over a copy of the Guild database, with the fake
// clock, the classes the test registers and the machines it decides.
type fixture struct {
	t        *testing.T
	db       *store.DB
	clk      *clock.Fake
	cat      *presets.Catalog
	classes  *fakeClasses
	machines *fakeMachines
	users    *fakeUsers
}

func newFixture(t *testing.T) *fixture {
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
	f := &fixture{t: t, db: db, clk: clock.NewFake(day0), classes: &fakeClasses{m: map[string]*classes.Class{}},
		machines: &fakeMachines{}, users: &fakeUsers{}}
	f.cat = &presets.Catalog{DB: db, Clock: f.clk, Classes: f.classes, Users: f.users, Machines: f.machines}
	return f
}

type fakeClasses struct {
	mu sync.Mutex
	m  map[string]*classes.Class
}

func (f *fakeClasses) Class(_ context.Context, name string) (*classes.Class, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.m[name]
	return c, ok, nil
}

// add registers a class with the permissions.yaml it is given.
func (f *fakeClasses) add(t *testing.T, name, permissions string) *classes.Class {
	t.Helper()
	perms, err := classes.ParsePermissions([]byte(permissions))
	if err != nil {
		t.Fatal(err)
	}
	c := &classes.Class{Name: name, Permissions: perms}
	f.mu.Lock()
	f.m[name] = c
	f.mu.Unlock()
	return c
}

type fakeUsers struct {
	mu sync.Mutex
	by map[string][]string
}

func (f *fakeUsers) SheetUsers(_ context.Context, sheet string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.by[sheet], nil
}

func (f *fakeUsers) set(sheet string, characters ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.by == nil {
		f.by = map[string][]string{}
	}
	f.by[sheet] = characters
}

type fakeMachines struct {
	mu     sync.Mutex
	sheets []machinesheet.Sheet
}

func (f *fakeMachines) Sheets(context.Context) ([]machinesheet.Sheet, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]machinesheet.Sheet(nil), f.sheets...), nil
}

func (f *fakeMachines) set(s ...machinesheet.Sheet) {
	f.mu.Lock()
	f.sheets = s
	f.mu.Unlock()
}

// example is the file of docs/orquestracao/bases-e-fichas.md#exemplos.
const example = `kind: mestre/presets
version: 1
bases:
  - name: claude-code
    command: claude
    platforms: [{os: linux}, {os: windows, command: claude.exe}]
    ready: hook
  - name: codex
    command: codex
    platforms: [{os: linux}, {os: windows, command: codex.exe}]
    ready: hook
  - name: antigravity
    command: agy
    platforms: [{os: linux}, {os: windows, command: agy.exe}]
    ready: hook
  - name: bash
    command: bash
    platforms: [{os: linux}]
    ready: {pattern: '\$ $'}
  - name: powershell
    command: pwsh
    platforms: [{os: windows}]
    ready: {pattern: 'PS .*> $'}
sheets:
  - {name: Dev Claude, base: claude-code, agent: claude-code, class: Dev, lifecycle: persistent, max_idle: 240m}
  - {name: Revisor Codex, base: codex, agent: codex, class: Revisor}
  - {name: QA Antigravity, base: antigravity, agent: antigravity, class: QA, account: agy-pessoal}
`

// importAll imports a file with every conflict resolved the same way.
func (f *fixture) importFile(data string, choices map[presets.Item]presets.Choice) (presets.Applied, error) {
	f.t.Helper()
	doc, err := presets.ParseDocument([]byte(data))
	if err != nil {
		return presets.Applied{}, err
	}
	plan, err := f.cat.PlanImport(context.Background(), doc)
	if err != nil {
		return presets.Applied{}, err
	}
	return f.cat.ApplyImport(context.Background(), plan, choices)
}

func (f *fixture) mustImportExample() {
	f.t.Helper()
	if _, err := f.importFile(example, nil); err != nil {
		f.t.Fatal(err)
	}
}

// onlineSheet is the sheet of a fake machine as the Guild keeps it.
func onlineSheet(t *testing.T, src machinesheet.Sources, id, name string, online bool) machinesheet.Sheet {
	t.Helper()
	b, err := machinesheet.NewBuilder(src, id, "")
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
