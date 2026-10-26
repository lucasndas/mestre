package classes_test

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/store"
	"github.com/mestre-ia/mestre/internal/testkit/guildtpl"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// fixture is a Library over a real folder of classes and the real Guild
// database (copied from the template TestMain migrated), with the fake clock.
type fixture struct {
	t       *testing.T
	lib     *classes.Library
	roots   classes.Roots
	records classes.ClassDB
	alerts  *classAlerts
	ceiling *fixedCeiling
	apps    *fakeApps
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "mestre.db")
	if err := os.WriteFile(path, guildTemplate, 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := guildtpl.Open(context.Background(), path, store.Options{Synchronous: "OFF"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	f := &fixture{t: t, alerts: &classAlerts{}, ceiling: &fixedCeiling{c: classes.DefaultCeiling()}, apps: &fakeApps{}}
	f.roots = classes.DefaultRoots(filepath.Join(dir, "classes-globais"), filepath.Join(dir, "loja"))
	clk := clock.NewFake(now)
	f.records = classes.ClassDB{DB: db, Clock: clk}
	f.lib = &classes.Library{
		Workspace: "loja", Roots: f.roots, Records: f.records, Ceiling: f.ceiling, Alerts: f.alerts, Apps: f.apps, Clock: clk,
		OS: "linux", Skill: func(string) bool { return false },
	}
	return f
}

type fixedCeiling struct {
	mu sync.Mutex
	c  *classes.Ceiling
}

func (f *fixedCeiling) Current(context.Context) (*classes.Ceiling, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.c, nil
}

func (f *fixedCeiling) set(c *classes.Ceiling) { f.mu.Lock(); f.c = c; f.mu.Unlock() }

type classAlerts struct {
	mu  sync.Mutex
	got []classes.ClassAlert
}

func (a *classAlerts) ClassChanged(_ context.Context, al classes.ClassAlert) error {
	a.mu.Lock()
	a.got = append(a.got, al)
	a.mu.Unlock()
	return nil
}

func (a *classAlerts) all() []classes.ClassAlert {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]classes.ClassAlert(nil), a.got...)
}

// fakeApps is the directory of the GitHub Apps (M5) in memory.
type fakeApps struct {
	mu        sync.Mutex
	apps      map[string]classes.AppState
	requested map[string]classes.AppState
}

func (f *fakeApps) App(_ context.Context, slug string) (classes.AppState, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.apps[slug]
	return s, ok, nil
}

func (f *fakeApps) Requested(_ context.Context, class string) (classes.AppState, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.requested[class]
	return s, ok, nil
}

func (f *fakeApps) add(slug string, s classes.AppState) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.apps == nil {
		f.apps = map[string]classes.AppState{}
	}
	f.apps[slug] = s
}

// The authors of the changes.
var (
	owner       = classes.Author{Level: classes.LevelOwner, ID: "dono"}
	admin       = classes.Author{Level: classes.LevelAdmin, ID: "ana"}
	grandMaster = classes.Author{Level: classes.LevelGrandMaster, ID: "gm"}
	tableMaster = classes.Author{Level: classes.LevelTableMaster, ID: "mestre-42", Table: tableID}
)

const tableID = "42-login"

const defaultPerms = "version: 1\nfiles: {write: [\"tests/**\"]}\ncommands: {allow: [\"npm test\"], default: ask}\n"

// pkg builds a class package: CLASS.md with the given app (empty: no `app`
// line), permissions.yaml and the extra files.
func pkg(t testing.TB, name, app, perms string, extra map[string]string) classes.Files {
	t.Helper()
	front := "---\nname: " + name + "\ndescription: Testa a mesa\n"
	if app != "" {
		front += "app: " + app + "\n"
	}
	front += "---\nTeste a mesa e relate as falhas.\n"
	files := []classes.File{{Path: "CLASS.md", Data: []byte(front)}}
	if perms != "" {
		files = append(files, classes.File{Path: "permissions.yaml", Data: []byte(perms)})
	}
	for p, c := range extra {
		files = append(files, classes.File{Path: p, Data: []byte(c)})
	}
	out, err := classes.NewFiles(files)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// put writes a package as the folder of a class, as a hand edit or a git
// pull would.
func (f *fixture) put(k classes.ClassKey, files classes.Files) {
	f.t.Helper()
	dir, err := f.roots.Dir(k)
	if err != nil {
		f.t.Fatal(err)
	}
	if err := classes.WriteDir(dir, files); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) save(req classes.SaveRequest) classes.SaveResult {
	f.t.Helper()
	res, err := f.lib.Save(context.Background(), req)
	if err != nil {
		f.t.Fatalf("save %s: %v", req.Name, err)
	}
	return res
}

func (f *fixture) exists(k classes.ClassKey) bool {
	dir, err := f.roots.Dir(k)
	if err != nil {
		f.t.Fatal(err)
	}
	_, err = os.Stat(dir)
	return err == nil
}
