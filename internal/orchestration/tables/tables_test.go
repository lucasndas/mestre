package tables_test

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/gitx"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/connections"
	"github.com/lucasndas/mestre/internal/orchestration/inbox"
	"github.com/lucasndas/mestre/internal/orchestration/tables"
	"github.com/lucasndas/mestre/internal/orchestration/workspaces"
	"github.com/lucasndas/mestre/internal/sessionzero"
)

// CA-MESAS-08: the slug of the title of an epic is lower case, without
// accents, with a hyphen for each run of other characters, at most 30
// characters; "mesa" when nothing is left.
func TestTheSlugOfTheTitleOfTheEpic(t *testing.T) {
	t.Parallel()
	for title, want := range map[string]string{
		"Login de usuários": "login-de-usuarios",
		"Ação: pagar!":      "acao-pagar",
		"!!!":               "mesa",
		"":                  "mesa",
		"  Ñandú -- ÀÉÎÕÜ ": "nandu-aeiou",
		"Um título bem comprido que passa dos trinta caracteres": "um-titulo-bem-comprido-que-pas",
		"a-----------------------------b-----------------------": "a-b",
	} {
		got := tables.Slug(title)
		if got != want {
			t.Errorf("Slug(%q) = %q; want %q", title, got, want)
		}
		if len(got) > 30 || strings.HasPrefix(got, "-") || strings.HasSuffix(got, "-") || strings.Contains(got, "--") {
			t.Errorf("Slug(%q) = %q breaks the rules", title, got)
		}
	}
	if got := tables.ID(42, "Login de usuários"); got != "42-login-de-usuarios" {
		t.Errorf("ID = %q", got)
	}
}

// CA-MESAS-02: only the transitions of the diagram are accepted, in every
// state, for every event.
func TestOnlyTheTransitionsOfTheDiagramAreAccepted(t *testing.T) {
	t.Parallel()
	diagram := map[tables.State]map[tables.Event]tables.State{
		tables.StateOpen:       {tables.EventReady: tables.StateWorking, tables.EventCancel: tables.StateCancelling},
		tables.StateWorking:    {tables.EventPROpened: tables.StateInReview, tables.EventCancel: tables.StateCancelling},
		tables.StateInReview:   {tables.EventMission: tables.StateWorking, tables.EventClose: tables.StateClosing, tables.EventCancel: tables.StateCancelling},
		tables.StateClosing:    {tables.EventClosed: tables.StateClosed},
		tables.StateCancelling: {tables.EventCancelled: tables.StateCancelled},
	}
	for _, from := range tables.States {
		for _, ev := range tables.Events {
			to, err := tables.Transition(from, ev)
			want, ok := diagram[from][ev]
			switch {
			case ok && (err != nil || to != want):
				t.Errorf("%s in %s = %q, %v; want %q", ev, from, to, err, want)
			case !ok && !errors.Is(err, tables.ErrBadTransition):
				t.Errorf("%s in %s = %q, %v; want an error", ev, from, to, err)
			}
		}
	}
	for _, s := range []tables.State{tables.StateClosed, tables.StateCancelled} {
		for _, ev := range tables.Events {
			if _, err := tables.Transition(s, ev); err == nil {
				t.Errorf("a table that is %s took %s", s, ev)
			}
		}
	}
}

// Part of CA-MESAS-01: the Grand Master opens the table of the epic 42 "Login
// de usuários": the branch epico/42-login-de-usuarios from the main branch of
// the origin, the worktree on the Citadel, the table master recruited by the
// Grand Master, connected to it, and its first message with the original text
// of the user byte for byte, followed by the observations in another block.
// (`gh epic create` is milestone 5: here the epic exists in the fake GitHub.)
func TestOpeningATableMakesTheBranchTheWorktreeAndTheTableMaster(t *testing.T) {
	t.Parallel()
	w := newWorld(t, worldOpts{})
	w.activate()
	w.gh.add(42, "Login de usuários", "epico")
	original := "Quero login com e-mail e senha.\n\n  Sem OAuth por enquanto — só e-mail.\r\nObrigado!\n"
	w.msgs.msgs["m81"] = tables.Message{ID: "m81", Text: original, Origin: inbox.Origin{Channel: inbox.ChannelTelegram, User: "u1", UserName: "Lucas", Level: characters.LevelOwner}}
	main, err := w.mirror().ResolveRef(w.ctx, "refs/remotes/origin/main")
	if err != nil {
		t.Fatal(err)
	}

	res, err := w.svc.Open(w.ctx, tables.OpenRequest{By: w.gm, Epic: 42, FromMessage: "m81", Notes: "Priorize o fluxo de recuperação de senha."})
	if err != nil {
		t.Fatal(err)
	}
	tbl := res.Table
	if tbl.ID != "42-login-de-usuarios" || tbl.Branch != "epico/42-login-de-usuarios" || tbl.State != tables.StateOpen && tbl.State != tables.StateWorking {
		t.Fatalf("table = %+v", tbl)
	}
	if got, err := w.mirror().ResolveRef(w.ctx, "refs/heads/"+tbl.Branch); err != nil || got != main {
		t.Fatalf("the branch is at %q (%v); want origin/main %q", got, err, main)
	}
	wantDir := filepath.Join(w.wss.Layout().Tables(w.slug()), "42-login-de-usuarios")
	if res.Worktree != wantDir {
		t.Errorf("worktree = %q; want %q", res.Worktree, wantDir)
	}
	if wts, _ := w.mirror().ListWorktrees(w.ctx); !slices.ContainsFunc(wts, func(x gitx.Worktree) bool { return x.Path == wantDir && x.Branch == tbl.Branch }) {
		t.Errorf("the worktree is not a worktree of the mirror on the branch: %+v", wts)
	}
	if got := w.files.list(&w.files.prepared); !slices.Contains(got, wantDir) {
		t.Errorf("the session zero of the table ran in %v; want %s", got, wantDir)
	}

	// The table master: recruited with the default sheet, in the table, created by the Grand Master.
	if len(w.chars.creates) != 1 {
		t.Fatalf("creates = %+v", w.chars.creates)
	}
	cr := w.chars.creates[0]
	if cr.Role != characters.RoleTableMaster || cr.Table != tbl.ID || cr.Workspace != w.slug() || cr.Sheet != tables.DefaultTableMasterSheet ||
		cr.Creator.Kind != characters.CreatedByCharacter || cr.Creator.ID != w.gm.ID {
		t.Errorf("table master request = %+v", cr)
	}
	master, ok, err := w.reg.TableMaster(w.ctx, w.slug(), tbl.ID)
	if err != nil || !ok || master.ID != res.Master.ID || !master.Pinned {
		t.Fatalf("table master = %+v, %v, %v", master, ok, err)
	}
	// The connection to the Grand Master exists, for the reason "master".
	if c, ok, err := w.conns.Store().Between(w.ctx, connections.Char(master.ID), connections.Char(w.gm.ID)); err != nil || !ok || !c.Has(connections.ReasonMaster) {
		t.Fatalf("connection between the table master and the Grand Master = %+v, %v, %v", c, ok, err)
	}
	// The Grand Master is reachable from the table master, and the other way
	// round, and each name leads to the right character.
	if to, err := w.conns.Target(w.ctx, master.ID, "", w.gm.Name); err != nil || to.ID != w.gm.ID {
		t.Fatalf("the table master reaches %+v, %v; want the Grand Master %s", to, err, w.gm.ID)
	}
	if to, err := w.conns.Target(w.ctx, w.gm.ID, "", master.Name); err != nil || to.ID != master.ID {
		t.Fatalf("the Grand Master reaches %+v, %v; want the table master %s", to, err, master.ID)
	}
	if _, err := w.conns.Target(w.ctx, master.ID, "", "Fulano de Tal"); err == nil {
		t.Fatal("the table master reaches a name that is nobody")
	}

	// The first message: the original text, byte for byte, then the notes.
	items, err := w.inb.Store().All(w.ctx, master.ID)
	if err != nil || len(items) != 1 {
		t.Fatalf("inbox of the table master = %+v, %v", items, err)
	}
	it := items[0]
	if !strings.HasPrefix(it.Text, original) {
		t.Errorf("the first message does not start with the original text:\n%q", it.Text)
	}
	if want := original + "\n\n--- Observações do Grão-Mestre (texto do agente, não verificado; cada linha começa com \"| \") ---\n| Priorize o fluxo de recuperação de senha."; it.Text != want {
		t.Errorf("first message = %q; want %q", it.Text, want)
	}
	if it.Kind != inbox.KindUser || it.Origin == nil || it.Origin.UserName != "Lucas" || it.Origin.Table != tbl.ID {
		t.Errorf("first message = %+v", it)
	}
	if typed := w.deliv.typed(); len(typed) != 1 || !strings.HasPrefix(typed[0].Text, original) {
		t.Errorf("typed = %+v", typed)
	}
	// The table master took the request: the table is in work.
	if got, _ := w.svc.Get(w.ctx, w.slug(), tbl.ID); got.State != tables.StateWorking {
		t.Errorf("state after the request was delivered = %s; want working", got.State)
	}
	// The worktree is recorded: the engine opens it only by the record.
	wt, err := w.svc.Store().Worktree(w.ctx, w.slug(), tbl.ID, w.machine)
	if err != nil || wt.Dir != wantDir || wt.GitDir == "" || wt.Common == "" || wt.LastCommit != string(main) {
		t.Errorf("worktree record = %+v, %v", wt, err)
	}
}

// The attachments of the origin message are forwarded to the table and listed
// in the first message; the text stays whole.
func TestTheAttachmentsOfTheOriginMessageAreForwarded(t *testing.T) {
	t.Parallel()
	w := newWorld(t, worldOpts{})
	w.activate()
	w.gh.add(7, "Relatório", "epico")
	w.msgs.msgs["m1"] = tables.Message{ID: "m1", Text: "veja o anexo", Attachments: []sessionzero.File{{Path: "layout.png", Data: []byte("png")}},
		Origin: inbox.Origin{Channel: inbox.ChannelPanel, User: "u1", UserName: "Lucas", Level: characters.LevelOwner}}
	res, err := w.svc.Open(w.ctx, tables.OpenRequest{By: w.gm, Epic: 7, FromMessage: "m1"})
	if err != nil {
		t.Fatal(err)
	}
	if got := w.atts.got[res.Table.ID]; len(got) != 1 || got[0].Path != "layout.png" {
		t.Errorf("forwarded = %+v", got)
	}
	items, _ := w.inb.Store().All(w.ctx, res.Master.ID)
	if len(items) != 1 || items[0].Text != "veja o anexo" || !slices.Equal(items[0].Attachments, []string{".mestre/anexos/layout.png"}) {
		t.Errorf("first message = %+v", items)
	}
	if got := w.files.list(&w.files.prepared); len(got) < 2 {
		t.Errorf("the session zero must run again so the attachments are copied: %v", got)
	}
}

// CA-MESAS-10: the epic 7 does not exist in the fake GitHub: the opening is
// refused, no epic is created and no branch exists.
func TestOpeningATableForAnEpicThatDoesNotExistIsRefused(t *testing.T) {
	t.Parallel()
	w := newWorld(t, worldOpts{})
	w.activate()
	_, err := w.svc.Open(w.ctx, tables.OpenRequest{By: w.gm, Epic: 7})
	if !errors.Is(err, tables.ErrEpicNotFound) {
		t.Fatalf("Open = %v; want epic_not_found", err)
	}
	if len(w.gh.epics) != 0 || w.gh.created != 0 {
		t.Errorf("the opening touched the epics: %+v", w.gh.epics)
	}
	refs, err := w.mirror().ListRefs(w.ctx, "refs/heads/epico/")
	if err != nil || len(refs) != 0 {
		t.Errorf("branches = %+v, %v; want none", refs, err)
	}
	if ts, _ := w.svc.List(w.ctx, w.slug()); len(ts) != 0 {
		t.Errorf("tables = %+v", ts)
	}
	if len(w.chars.creates) != 0 {
		t.Errorf("a table master was recruited: %+v", w.chars.creates)
	}
}

// The other refusals of the opening: no label, closed epic, an epic that
// already has a table, a workspace that is not active, a caller that is not
// the Grand Master. Nothing is left behind.
func TestOtherRefusalsOfTheOpening(t *testing.T) {
	t.Parallel()
	w := newWorld(t, worldOpts{})
	w.activate()
	w.gh.add(1, "Sem rótulo", "bug")
	w.gh.add(2, "Fechado", "epico")
	e := w.gh.epics[2]
	e.Open = false
	w.gh.epics[2] = e
	w.gh.add(3, "Bom", "epico")
	for epic, want := range map[int]error{1: tables.ErrEpicNotFound, 2: tables.ErrEpicClosed} {
		if _, err := w.svc.Open(w.ctx, tables.OpenRequest{By: w.gm, Epic: epic}); !errors.Is(err, want) {
			t.Errorf("Open(#%d) = %v; want %v", epic, err, want)
		}
	}
	w.open(3)
	if _, err := w.svc.Open(w.ctx, tables.OpenRequest{By: w.gm, Epic: 3}); !errors.Is(err, tables.ErrTableExists) {
		t.Errorf("a second table for the same epic = %v; want table_exists", err)
	}
	master, _, _ := w.reg.TableMaster(w.ctx, w.slug(), "3-bom")
	if _, err := w.svc.Open(w.ctx, tables.OpenRequest{By: master, Epic: 3}); !errors.Is(err, tables.ErrNotGrandMaster) {
		t.Errorf("a table master opening a table = %v; want level_denied", err)
	}
	if err := w.wss.Pause(w.ctx, w.slug()); err != nil {
		t.Fatal(err)
	}
	w.gh.add(4, "Outro", "epico")
	var na *workspaces.NotActiveError
	if _, err := w.svc.Open(w.ctx, tables.OpenRequest{By: w.gm, Epic: 4}); !errors.As(err, &na) || na.State != workspaces.StatePaused {
		t.Errorf("a paused workspace = %v; want it refused", err)
	}
	if ts, _ := w.svc.List(w.ctx, w.slug()); len(ts) != 1 {
		t.Errorf("tables = %d; want only the one that opened", len(ts))
	}
}

// If a step fails the ones before it are undone and the error goes back to the
// Grand Master: the table master that did not get its request is dismissed,
// the worktree and the branch go away, and the epic can be opened again.
func TestAFailedOpeningUndoesTheStepsBefore(t *testing.T) {
	t.Parallel()
	for name, fail := range map[string]func(w *world){
		"the session zero":   func(w *world) { w.files.err = errors.New("hooks.json is not valid") },
		"the table master":   func(w *world) { w.chars.createErr = errors.New("no machine") },
		"the origin message": func(w *world) {},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			w := newWorld(t, worldOpts{})
			w.activate()
			w.gh.add(5, "Falha", "epico")
			req := tables.OpenRequest{By: w.gm, Epic: 5}
			if name == "the origin message" {
				req.FromMessage = "m404"
			}
			fail(w)
			if _, err := w.svc.Open(w.ctx, req); err == nil {
				t.Fatal("Open succeeded")
			}
			refs, _ := w.mirror().ListRefs(w.ctx, "refs/heads/epico/")
			wts, _ := w.mirror().ListWorktrees(w.ctx)
			if len(refs) != 0 || len(wts) != 2 { // the mirror and the main clone
				t.Errorf("left behind: branches %+v, worktrees %+v", refs, wts)
			}
			if ts, _ := w.svc.List(w.ctx, w.slug()); len(ts) != 0 {
				t.Errorf("tables = %+v", ts)
			}
			if name == "the session zero" {
				w.files.err = nil
				if _, err := w.svc.Open(w.ctx, req); err != nil {
					t.Errorf("the epic did not open again: %v", err)
				}
			}
		})
	}
}

// A failure after the table master was recruited dismisses it too.
func TestAFailedOpeningDismissesTheTableMaster(t *testing.T) {
	t.Parallel()
	w := newWorld(t, worldOpts{})
	w.activate()
	w.gh.add(5, "Falha", "epico")
	w.enqueueErr = errors.New("the inbox is down")
	if _, err := w.svc.Open(w.ctx, tables.OpenRequest{By: w.gm, Epic: 5}); err == nil {
		t.Fatal("Open succeeded")
	}
	if len(w.chars.creates) != 1 || !slices.Equal(w.chars.dismissed(), []string{"c1"}) {
		t.Fatalf("creates %d, dismissed %v; want the table master dismissed", len(w.chars.creates), w.chars.dismissed())
	}
	if m, err := w.reg.Get(w.ctx, "c1"); err != nil || m.State != characters.Stopped || m.Pinned {
		t.Errorf("the table master = %+v, %v", m, err)
	}
	refs, _ := w.mirror().ListRefs(w.ctx, "refs/heads/epico/")
	if len(refs) != 0 {
		t.Errorf("branches = %+v", refs)
	}
}

// CA-MESAS-09: the merge is confirmed in a table in review: it stays in
// review, the Grand Master gets the item `github`, and only `table close` takes
// it to closing; a close without the merge confirmed is refused.
func TestTheConfirmedMergeOnlyTellsTheGrandMaster(t *testing.T) {
	t.Parallel()
	w := newWorld(t, worldOpts{})
	w.activate()
	w.gh.add(42, "Login", "epico")
	tbl := w.open(42).Table
	repo := w.repoOf(tbl)
	head := commit(t, w.be, w.git, repo, "login.go", "package login\n", "feat: login")

	if _, err := w.svc.Close(w.ctx, tables.CloseRequest{By: w.gm, Table: tbl.ID}); !errors.Is(err, tables.ErrNotMerged) {
		t.Fatalf("close of a table in work = %v; want not_merged", err)
	}
	if _, err := w.svc.PROpened(w.ctx, w.slug(), tbl.ID, 7, string(head)); err != nil {
		t.Fatal(err)
	}
	if _, err := w.svc.Close(w.ctx, tables.CloseRequest{By: w.gm, Table: tbl.ID}); !errors.Is(err, tables.ErrNotMerged) {
		t.Fatalf("close of a table in review without the merge = %v; want not_merged", err)
	}
	if got, _ := w.svc.Get(w.ctx, w.slug(), tbl.ID); got.State != tables.StateInReview {
		t.Fatalf("state = %s after the refusal", got.State)
	}

	got, err := w.svc.MergeConfirmed(w.ctx, w.slug(), tbl.ID, string(head))
	if err != nil {
		t.Fatal(err)
	}
	if got.State != tables.StateInReview || !got.Merged {
		t.Fatalf("after the merge: state %s merged %v; want in_review, merged", got.State, got.Merged)
	}
	items, _ := w.inb.Store().All(w.ctx, w.gm.ID)
	if len(items) != 1 || items[0].Kind != inbox.KindGitHub || !strings.Contains(items[0].Text, "PR da mesa "+tbl.ID+" mesclado") {
		t.Fatalf("inbox of the Grand Master = %+v", items)
	}
	// Telling it again changes nothing and does not repeat the item.
	if _, err := w.svc.MergeConfirmed(w.ctx, w.slug(), tbl.ID, string(head)); err != nil {
		t.Fatal(err)
	}
	if items, _ = w.inb.Store().All(w.ctx, w.gm.ID); len(items) != 1 {
		t.Errorf("the item was repeated: %+v", items)
	}
	res, err := w.svc.Close(w.ctx, tables.CloseRequest{By: w.gm, Table: tbl.ID})
	if err != nil {
		t.Fatal(err)
	}
	if res.Table.State != tables.StateClosed {
		t.Errorf("state after close = %s; want closed", res.Table.State)
	}
}

// The mission of adjustment takes a table in review back to work; a mission
// does nothing to a table that is closing.
func TestAMissionTakesAReviewedTableBackToWork(t *testing.T) {
	t.Parallel()
	w := newWorld(t, worldOpts{})
	w.activate()
	w.gh.add(42, "Login", "epico")
	tbl := w.open(42).Table
	if got, err := w.svc.MissionStarted(w.ctx, w.slug(), tbl.ID); err != nil || got.State != tables.StateWorking {
		t.Fatalf("a mission in work = %v, %v", got.State, err)
	}
	if _, err := w.svc.PROpened(w.ctx, w.slug(), tbl.ID, 3, "abc"); err != nil {
		t.Fatal(err)
	}
	if got, err := w.svc.PROpened(w.ctx, w.slug(), tbl.ID, 3, "def"); err != nil || got.State != tables.StateInReview || got.PRHead != "def" {
		t.Fatalf("pr update in review = %+v, %v", got, err)
	}
	if got, err := w.svc.MissionStarted(w.ctx, w.slug(), tbl.ID); err != nil || got.State != tables.StateWorking {
		t.Fatalf("a mission in review = %v, %v", got.State, err)
	}
}

// Part of CA-SUPCAD-06 (fecha no M5; falta: o aviso de 3 h sem entrega, o botão
// Pausar mesa e a auditoria de quem pausou).
// The pause is a mark over the state: the inbox does not deliver to the
// characters of the table, the other tables go on, a paused workspace pauses
// every table, and resume delivers what waited in the order of the queue.
func TestPausingATableHoldsTheInboxUntilResume(t *testing.T) {
	t.Parallel()
	w := newWorld(t, worldOpts{})
	w.activate()
	w.gh.add(1, "Um", "epico")
	w.gh.add(2, "Dois", "epico")
	t1, t2 := w.open(1).Table, w.open(2).Table
	m1, _, _ := w.reg.TableMaster(w.ctx, w.slug(), t1.ID)
	m2, _, _ := w.reg.TableMaster(w.ctx, w.slug(), t2.ID)
	// Both masters are idle again after the request.
	for _, m := range []characters.Character{m1, m2} {
		if _, _, err := w.reg.Transition(w.ctx, m.ID, characters.Idle, characters.Change{At: w.clk.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.svc.Pause(w.ctx, w.slug(), t1.ID, tables.PauseRequest{Reason: "limite de uso", By: "usage_limit"}); err != nil {
		t.Fatal(err)
	}
	if p, _ := w.svc.Paused(w.ctx, w.slug(), t1.ID); !p {
		t.Fatal("the table is not paused")
	}
	if p, _ := w.svc.Paused(w.ctx, w.slug(), t2.ID); p {
		t.Fatal("the other table is paused")
	}
	if p, _ := w.svc.Paused(w.ctx, w.slug(), ""); p {
		t.Fatal("the workspace is paused")
	}
	before := len(w.deliv.typed())
	for _, id := range []string{m1.ID, m2.ID} {
		if _, err := w.inb.Enqueue(w.ctx, inbox.NewItem{Workspace: w.slug(), Target: id, Kind: inbox.KindAgent, Sender: "x", Text: "oi " + id}); err != nil {
			t.Fatal(err)
		}
	}
	typed := w.deliv.typed()[before:]
	if len(typed) != 1 || typed[0].Text != "oi "+m2.ID {
		t.Fatalf("typed while t1 is paused = %+v; want only the other table's", typed)
	}
	if err := w.svc.Resume(w.ctx, w.slug(), t1.ID); err != nil {
		t.Fatal(err)
	}
	if typed = w.deliv.typed()[before:]; len(typed) != 2 || typed[1].Text != "oi "+m1.ID {
		t.Fatalf("typed after resume = %+v; want the queued item", typed)
	}
	// The workspace paused: every table is.
	if err := w.svc.PauseWorkspace(w.ctx, w.slug()); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{t1.ID, t2.ID, ""} {
		if p, _ := w.svc.Paused(w.ctx, w.slug(), id); !p {
			t.Errorf("table %q is not paused with the workspace", id)
		}
	}
	if err := w.svc.ResumeWorkspace(w.ctx, w.slug()); err != nil {
		t.Fatal(err)
	}
	if p, _ := w.svc.Paused(w.ctx, w.slug(), t1.ID); p {
		t.Error("the table is still paused after the workspace resumed")
	}
}

// What the characters ask of the tables: the table with its state and its
// worktrees, and the folder of a machine (the main clone for the Grand Master).
func TestTheTablesAnswerTheCharacters(t *testing.T) {
	t.Parallel()
	w := newWorld(t, worldOpts{})
	w.activate()
	w.gh.add(42, "Login", "epico")
	tbl := w.open(42).Table
	info, err := w.svc.Table(w.ctx, w.slug(), tbl.ID)
	if err != nil || info.State != characters.TableWorking || info.Epic != 42 || info.Branch != "epico/42-login" || info.Worktrees[w.machine] == "" {
		t.Fatalf("Table = %+v, %v", info, err)
	}
	if _, err := w.svc.Table(w.ctx, w.slug(), "9-nada"); !errors.Is(err, tables.ErrNoTable) {
		t.Errorf("an unknown table = %v", err)
	}
	if dir, err := w.svc.Worktree(w.ctx, w.slug(), tbl.ID, w.machine); err != nil || dir != info.Worktrees[w.machine] {
		t.Errorf("Worktree = %q, %v", dir, err)
	}
	if dir, err := w.svc.Worktree(w.ctx, w.slug(), "", w.machine); err != nil || dir != w.wss.Layout().Principal(w.slug()) {
		t.Errorf("the main clone = %q, %v", dir, err)
	}
	if _, err := w.svc.Worktree(w.ctx, w.slug(), tbl.ID, "torre-9"); !errors.Is(err, tables.ErrMachineUnreachable) {
		t.Errorf("a Tower that is not connected = %v", err)
	}
	if found, err := w.svc.Find(w.ctx, w.slug(), "42"); err != nil || found.ID != tbl.ID {
		t.Errorf("Find by the number of the epic = %+v, %v", found, err)
	}
}
