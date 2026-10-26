package app

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/github"
	"github.com/mestre-ia/mestre/internal/orchestration/workspaces"
	"github.com/mestre-ia/mestre/internal/panel"
	"github.com/mestre-ia/mestre/internal/store"
	"github.com/mestre-ia/mestre/internal/testkit/guildtpl"
	"github.com/mestre-ia/mestre/internal/users"
	"github.com/mestre-ia/mestre/internal/vault"
	"github.com/mestre-ia/mestre/pkg/clock"
)

const panelPkgPerms = "version: 1\nfiles:\n  read: [\"**\"]\n"

func panelPkg(t *testing.T, name, body string) classes.Files {
	t.Helper()
	f, err := classes.NewFiles([]classes.File{
		{Path: "CLASS.md", Data: []byte("---\nname: " + name + "\ndescription: Classe de teste\napp: novo\n---\n" + body + "\n")},
		{Path: "permissions.yaml", Data: []byte(panelPkgPerms)},
	})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// classRig is the classes screen over a real library of one workspace.
type classRig struct {
	cp  classPanel
	lib *classes.Library
	dir string
	ctx context.Context
}

func newClassRig(t *testing.T) *classRig {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	db, err := guildtpl.Open(ctx, filepath.Join(root, "mestre.db"), store.Options{Synchronous: "OFF"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Writer().ExecContext(ctx, `INSERT INTO workspaces (slug, repo, repo_key, clone_url, default_branch, state, stage, created_at, updated_at) VALUES ('loja', 'o/loja', 'o/loja', 'u', 'main', 'active', 'done', 0, 0)`); err != nil {
		t.Fatal(err)
	}
	layout := workspaces.Layout{Root: filepath.Join(root, "workspaces")}
	books := newClassBooks(db, clock.Real{}, filepath.Join(root, "globais"), layout)
	lib := books.For("loja")
	dir, err := lib.Roots.Dir(classes.ClassKey{Scope: classes.ClassWorkspace, Name: "qa"})
	if err != nil {
		t.Fatal(err)
	}
	return &classRig{cp: classPanel{orc: &Orchestration{Libraries: books.For, log: slog.New(slog.DiscardHandler)}, db: db}, lib: lib, dir: dir, ctx: ctx}
}

func (r *classRig) listed(t *testing.T, name string) (panel.ClassRow, bool) {
	t.Helper()
	rows, _, err := r.cp.Classes(r.ctx, []string{"loja"}, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range rows {
		if c.Name == name {
			return c, true
		}
	}
	return panel.ClassRow{}, false
}

func (r *classRig) proposals(t *testing.T) []panel.ProposalRow {
	t.Helper()
	_, props, err := r.cp.Classes(r.ctx, []string{"loja"}, false)
	if err != nil {
		t.Fatal(err)
	}
	return props
}

var (
	admin  = users.User{ID: "ana", Level: users.Administrator}
	viewer = users.User{ID: "obs", Level: users.Observer}
)

// Part of CA-CLASSES-06: the class the Grand Master proposes waits in the panel, and only
// the "Aprovar" of an Administrator or above puts it in use; an Approver or an Observer
// cannot, and the hash the approver saw must still be the proposal's.
func TestTheProposalOfTheGrandMasterWaitsForAnAdministratorInThePanel(t *testing.T) {
	t.Parallel()
	r := newClassRig(t)
	res, err := r.lib.Save(r.ctx, classes.SaveRequest{Name: "qa", Scope: classes.ClassWorkspace, App: "novo", Files: panelPkg(t, "qa", "Teste."), By: classes.Author{Level: classes.LevelGrandMaster, ID: "gm-1"}})
	if err != nil || res.Applied || res.Proposal == nil {
		t.Fatalf("save by the Grand Master = %+v, %v; want a proposal", res, err)
	}
	props := r.proposals(t)
	if len(props) != 1 || props[0].Origin != "request" || props[0].Name != "qa" || props[0].Workspace != "loja" || props[0].Tested {
		t.Fatalf("proposals = %+v; want the one of the Grand Master, not tested", props)
	}
	if c, ok := r.listed(t, "qa"); ok && c.State == "approved" {
		t.Fatal("the class is in use before the approval")
	}
	for _, by := range []users.User{viewer, {ID: "ap", Level: users.Approver}} {
		if err := r.cp.Approve(r.ctx, "loja", props[0].ID, props[0].Hash, by); err == nil {
			t.Fatalf("%s approved a class of the workspace", by.Level)
		}
	}
	if err := r.cp.Approve(r.ctx, "loja", props[0].ID, "outro-hash", admin); !errors.Is(err, classes.ErrClassChanged) {
		t.Fatalf("approve with another hash = %v, want ErrClassChanged", err)
	}
	if err := r.cp.Approve(r.ctx, "loja", props[0].ID, props[0].Hash, admin); err != nil {
		t.Fatalf("approve by the Administrator: %v", err)
	}
	if c, ok := r.listed(t, "qa"); !ok || c.State != "approved" {
		t.Fatalf("class after the approval = %+v, %v; want approved", c, ok)
	}
	if len(r.proposals(t)) != 0 {
		t.Error("the proposal still waits after it was approved")
	}
}

// Part of CA-CLI-CLR-03 and CA-CLASSES-06: a class changed outside the Mestre (a manual
// edit, a git pull) shows up in the panel as a proposal from disk, and the Administrator
// approves what is on disk as it is.
func TestAClassChangedOutsideShowsUpInThePanelForApproval(t *testing.T) {
	t.Parallel()
	r := newClassRig(t)
	files := panelPkg(t, "qa", "Versão um.")
	if err := classes.WriteDir(r.dir, files); err != nil {
		t.Fatal(err)
	}
	rec := classes.ClassRecord{Key: classes.ClassKey{Scope: classes.ClassWorkspace, Name: "qa"}, Hash: files.Hash(), Files: files, ApprovedAt: clock.Real{}.Now(), By: classes.Author{Level: classes.LevelAdmin, ID: "ana"}}
	if err := r.lib.Records.Approve(r.ctx, "loja", rec, 0); err != nil {
		t.Fatal(err)
	}
	if c, ok := r.listed(t, "qa"); !ok || c.State != "approved" || len(r.proposals(t)) != 0 {
		t.Fatalf("before the edit: %+v %v, proposals %v", c, ok, r.proposals(t))
	}
	// somebody edits the file on disk
	if err := os.WriteFile(filepath.Join(r.dir, "CLASS.md"), []byte("---\nname: qa\ndescription: Classe de teste\napp: novo\n---\nVersão dois.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	props := r.proposals(t)
	if len(props) != 1 || props[0].Origin != "disk" || props[0].By != "" {
		t.Fatalf("proposals after the edit = %+v; want one from disk", props)
	}
	if c, _ := r.listed(t, "qa"); c.State == "approved" {
		t.Errorf("the class edited outside is still shown as approved: %+v", c)
	}
	if err := r.cp.Approve(r.ctx, "loja", props[0].ID, props[0].Hash, viewer); err == nil {
		t.Fatal("an Observer approved a change from disk")
	}
	if err := r.cp.Approve(r.ctx, "loja", props[0].ID, props[0].Hash, admin); err != nil {
		t.Fatalf("approve by the Administrator: %v", err)
	}
	if c, _ := r.listed(t, "qa"); c.State != "approved" {
		t.Fatalf("after the approval: %+v", c)
	}
}

// stubVault is the vault of the creation: this test never reaches the exchange of the code.
type stubVault struct{}

func (stubVault) Use(context.Context, string, string, func([]byte) error) error {
	return errors.New("no key")
}
func (stubVault) Put(context.Context, string, vault.Type, []byte, *vault.SiteMeta) error {
	return nil
}
func (stubVault) Delete(context.Context, string) error { return nil }

// `mestre app create` makes a working link: the Owner gets it by the Telegram with the
// address of the panel, and the Grand Master gets only the request (the link never returns
// to it). Without the creation (no vault or no address of the panel) the command says why.
func TestTheAppCreatorMakesTheLinkAndTellsOnlyTheOwner(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := guildtpl.Open(ctx, filepath.Join(t.TempDir(), "mestre.db"), store.Options{Synchronous: "OFF"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	clk := clock.Real{}
	reg := &github.Registry{DB: db, Clock: clk}
	if _, err := reg.Request(ctx, github.App{Name: "mestre-qa", Role: github.RoleClass, Class: "qa"}); err != nil {
		t.Fatal(err)
	}
	c, err := github.NewCreation(github.CreationOptions{Apps: reg, Tokens: &github.Tokens{Apps: reg, Keys: stubVault{}, Clock: clk}, Vault: stubVault{},
		Panel: func() string { return "https://mestre.local:7443" }, OwnerLogin: func(context.Context) (string, error) { return "lucas", nil }})
	if err != nil {
		t.Fatal(err)
	}
	var told []string
	cr := appCreator{c: c, tell: func(_ context.Context, text string) { told = append(told, text) }}
	if why := cr.Available(ctx); why != "" {
		t.Fatalf("available = %q", why)
	}
	link, err := cr.Begin(ctx, "mestre-qa", "gm-1")
	if err != nil || link.ID != "mestre-qa" || link.ExpiresAt.IsZero() {
		t.Fatalf("begin = %+v, %v", link, err)
	}
	if len(told) != 1 || !strings.Contains(told[0], "https://mestre.local:7443/github/apps/novo/") || !strings.Contains(told[0], "mestre-qa") {
		t.Fatalf("the Owner was told %q", told)
	}
	if open, _ := reg.OpenRequest(ctx, "mestre-qa"); !open {
		t.Error("the request of the App is not open")
	}
	if why := (appCreator{}).Available(ctx); why == "" {
		t.Error("a creator without the creation says it is available")
	}
}
