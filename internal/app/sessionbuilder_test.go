package app_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mestre-ia/mestre/internal/app"
	"github.com/mestre-ia/mestre/internal/github"
	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/orchestration/tables"
	"github.com/mestre-ia/mestre/internal/orchestration/workspaces"
	"github.com/mestre-ia/mestre/internal/sessionzero"
)

// The session zero of a character, Citadel side: the request is built from the
// effective rules, the decrees, the identity, the class and the extra
// instructions of the users (docs/sessao-zero/personagem.md).

type fakeNotes struct {
	notes []sessionzero.DecreeNote
	err   error
}

func (f *fakeNotes) Notes(context.Context, characters.Character) ([]sessionzero.DecreeNote, error) {
	return f.notes, f.err
}

// fakeIdentities gives each level the bot of its App; a role in refuse has none.
type fakeIdentities struct{ refuse map[string]bool }

func (f fakeIdentities) For(_ context.Context, w github.Who) (github.Identity, error) {
	if f.refuse[w.Role] {
		return github.Identity{}, errors.New("the App is not ready")
	}
	name := "mestre-" + w.Role + "[bot]"
	return github.Identity{Name: name, Email: "1+" + name + "@users.noreply.github.com", App: w.Role}, nil
}

// sessionWorld is the Citadel world with the real builder.
type sessionWorld struct {
	*commWorld
	sb     *app.SessionBuilder
	notes  *fakeNotes
	ids    fakeIdentities
	layout workspaces.Layout
}

func newSessionWorld(t *testing.T) *sessionWorld {
	t.Helper()
	sw := &sessionWorld{notes: &fakeNotes{notes: []sessionzero.DecreeNote{{Number: 7, Author: "Ana", Level: "Administrador", Text: "use pnpm"}}}, ids: fakeIdentities{refuse: map[string]bool{}}}
	withBuilder := func(w *commWorld, oo *app.OrchestrationOptions) {
		sw.layout = workspaces.Layout{Root: oo.WorkspacesDir}
		var err error
		sw.sb, err = app.NewSessionBuilder(app.SessionBuilderOptions{Rules: w.cc.Rules, Notes: sw.notes, Identities: sw.ids,
			Libraries: oo.ClassLibraries, Layout: sw.layout, Machine: oo.Machine, Tables: tables.Store{DB: oo.Guild}})
		if err != nil {
			t.Fatal(err)
		}
		oo.Builder = sw.sb
	}
	sw.commWorld = newCommWorld(t, withCitadelClasses, withBuilder)
	sw.approveClass("dev", devPerms)
	return sw
}

func (w *sessionWorld) instructions(file, text string) string {
	w.t.Helper()
	dir := filepath.Join(w.layout.Principal(w.slug), ".mestre", "regras", "instrucoes")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		w.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, file), []byte(text), 0o644); err != nil {
		w.t.Fatal(err)
	}
	return filepath.Join(dir, file)
}

func (w *sessionWorld) build(c characters.Character) (sessionzero.CharacterRequest, error) {
	snap, err := commPresets{}.Snapshot(w.ctx, c.Sheet)
	if err != nil {
		w.t.Fatal(err)
	}
	in := characters.BuildInput{Character: c, Snapshot: snap, Dir: c.Dir}
	if c.Table != "" {
		if in.Table, err = w.orc.Tables.Table(w.ctx, c.Workspace, c.Table); err != nil {
			w.t.Fatal(err)
		}
	}
	return w.sb.Build(w.ctx, in)
}

func TestTheBuilderRendersTheRequestOfTheGrandMasterFromTheRulesTheDecreesTheIdentityAndTheInstructions(t *testing.T) {
	t.Parallel()
	w := newSessionWorld(t)
	w.instructions("grao-mestre.md", "<!-- do arquivo semente -->\nPrefira Codex para revisão.\n")
	req, err := w.build(w.get(w.gm))
	if err != nil {
		t.Fatal(err)
	}
	if req.Role != sessionzero.RoleGrandMaster || req.Name != "Grão-Mestre" || req.Table.ID != "" || req.Class != nil || req.GitDir != "" || req.Mirror != "" {
		t.Errorf("the request of the Grand Master: %+v", req)
	}
	if req.Rules == nil || req.Rules.AgentID != w.gm.ID || req.Rules.Class != "orquestrador" {
		t.Errorf("the rules are not the ones of the class orquestrador: %+v", req.Rules)
	}
	if req.Identity.Name != "mestre-grand_master[bot]" || !strings.Contains(req.Identity.Email, "users.noreply.github.com") {
		t.Errorf("identity = %+v", req.Identity)
	}
	if len(req.Decrees) != 1 || req.Decrees[0].Number != 7 || req.Instructions != "Prefira Codex para revisão." {
		t.Errorf("decrees %+v, instructions %q; want the note of the decree and the text of the file without its comment", req.Decrees, req.Instructions)
	}
	if want := w.layout.Principal(w.slug); req.Vars.Workspace != want || req.Vars.Mesa != req.Dir {
		t.Errorf("vars = %+v; want the main clone %s", req.Vars, want)
	}
}

func TestTheBuilderRendersTheRequestOfTheTableMasterAndOfACharacterWithTheRecordOfTheWorktree(t *testing.T) {
	t.Parallel()
	w := newSessionWorld(t)
	w.instructions("mestre-da-mesa.md", "Commits pequenos.\n")
	req, err := w.build(w.get(w.master))
	if err != nil {
		t.Fatal(err)
	}
	if req.Role != sessionzero.RoleTableMaster || req.Table.ID != w.table.ID || req.Table.Branch != w.table.Branch || req.Table.Epic != 42 ||
		req.Instructions != "Commits pequenos." || req.Identity.Name != "mestre-table_master[bot]" {
		t.Errorf("the request of the table master: %+v", req)
	}
	rec, err := tables.Store{DB: w.orc.Registry.DB}.Worktree(w.ctx, w.slug, w.table.ID, "cidadela")
	if err != nil || rec.GitDir == "" || req.GitDir != rec.GitDir || req.Mirror != rec.Common {
		t.Errorf("git dir %q, mirror %q; want the record taken when the worktree was made: %+v, %v", req.GitDir, req.Mirror, rec, err)
	}

	dev := w.recruit("Rita", "Dev Claude")
	req, err = w.build(w.get(dev))
	if err != nil {
		t.Fatal(err)
	}
	if req.Role != sessionzero.RoleCharacter || req.Class == nil || req.Class.Name != "dev" || req.Instructions != "" || len(req.Decrees) != 1 ||
		req.Identity.Name != "mestre-character[bot]" || req.Rules == nil || req.Rules.AgentID != dev.ID {
		t.Errorf("the request of the character: %+v", req)
	}
}

func TestTheBuilderRefusesWhenAnyPieceIsMissingAndNeverGivesARequestWithoutIt(t *testing.T) {
	t.Parallel()
	w := newSessionWorld(t)
	gm := w.get(w.gm)
	refused := func(name, want string) {
		t.Helper()
		req, err := w.build(gm)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v; want %q", name, err, want)
		}
		if req.Rules != nil || req.ID != "" {
			t.Errorf("%s: a request came with the error: %+v", name, req)
		}
	}

	w.ids.refuse["grand_master"] = true
	refused("identity", "the App is not ready")
	delete(w.ids.refuse, "grand_master")

	w.notes.err = errors.New("db down")
	refused("decrees", "the decrees of Grão-Mestre")
	w.notes.err = nil

	w.instructions("grao-mestre.md", strings.Repeat("x", 4097))
	refused("too large", "over 4 KiB")

	p := w.instructions("grao-mestre.md", "")
	target := filepath.Join(t.TempDir(), "other.md")
	if err := os.WriteFile(target, []byte("segredo"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, p); err != nil {
		t.Fatal(err)
	}
	refused("link", "extra instructions")
	_ = os.Remove(p)

	// A character the registry does not know has no rules: it does not start.
	ghost := gm
	ghost.ID = "ghost"
	if _, err := w.sb.Build(w.ctx, characters.BuildInput{Character: ghost, Dir: gm.Dir}); err == nil || !strings.Contains(err.Error(), "effective rules") {
		t.Errorf("a character with no rules: err = %v", err)
	}

	// Without the files the layer is built with none: a missing file and a file
	// of only the comment it is born with say nothing.
	for _, text := range []string{"", "<!-- Instruções extras -->\n"} {
		w.instructions("grao-mestre.md", text)
		if req, err := w.build(gm); err != nil || req.Instructions != "" {
			t.Errorf("instructions %q: %q, %v", text, req.Instructions, err)
		}
	}
	if err := os.Remove(filepath.Join(w.layout.Principal(w.slug), ".mestre", "regras", "instrucoes", "grao-mestre.md")); err != nil {
		t.Fatal(err)
	}
	if req, err := w.build(gm); err != nil || req.Instructions != "" {
		t.Errorf("no file: %q, %v", req.Instructions, err)
	}
}
