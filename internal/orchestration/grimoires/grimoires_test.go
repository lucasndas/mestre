package grimoires_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/connections"
	"github.com/lucasndas/mestre/internal/orchestration/grimoires"
	"github.com/lucasndas/mestre/internal/orchestration/inbox"
	"github.com/lucasndas/mestre/internal/orchestration/scrolls"
	"github.com/lucasndas/mestre/internal/testkit/orcfakes"
)

// team makes the team of the examples in workspace A: Dev and QA, the scroll
// Plano with a manual connection to Dev, and the quest Fluxo of the two.
func (w *world) team(class string) (dev, qa characters.Character, plan, tome string, quest string) {
	w.t.Helper()
	dev = w.recruit(w.a, "Dev Claude", "Dev", class)
	qa = w.recruit(w.a, "Revisor Codex", "QA", "")
	n := w.scroll(w.a, "Plano", "# Plano\nFazer o login.\n", "")
	w.link(w.a, connections.Char(dev.ID), connections.Scroll(n.Key))
	v := w.quest(w.a, questDef)
	return dev, qa, n.Key, "", v.ID
}

func (w *world) restoreReq(s side, name string, ch map[string]grimoires.Choice) grimoires.RestoreRequest {
	return grimoires.RestoreRequest{Grimoire: name, Workspace: s.slug, Table: s.table.ID, Choices: ch, By: admin}
}

// CA-GRIM-01: with a selection of two characters, a scroll, a manual connection
// and a quest, when it is saved the document has these elements and no data of
// a session, of a queue or of a wait.
func TestSavingTheSelectionKeepsTheTeamAndNothingOfItsState(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	dev, qa, scroll, _, quest := w.team("")
	// State that must stay out: a session of the provider, a queued item and the
	// automatic connections (Dev-Master, QA-Master, the quest's Dev-QA).
	if err := w.orc.Registry.SetSession(w.ctx, dev.ID, "sessao-SECRETA-123"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.orc.Inbox.Enqueue(w.ctx, inbox.NewItem{Workspace: w.a.slug, Target: dev.ID, Kind: inbox.KindSystem, Text: "texto-da-fila-XYZ"}); err != nil {
		t.Fatal(err)
	}
	res := w.save(w.a, "Time de login", grimoires.Selection{
		Characters: append(picks(dev.ID, qa.ID, w.a.master.ID), grimoires.Pick{}), Scrolls: picks(scroll), Quests: picks(quest),
	}, func(r *grimoires.SaveRequest) { r.Select.Characters = r.Select.Characters[:3] })

	d, err := w.orc.Grimoires.Get(w.ctx, admin, "Time de login")
	if err != nil {
		t.Fatal(err)
	}
	if d.Version != 1 || d.Name != "Time de login" || d.Author != "Ana" || d.Workspace != w.a.slug || d.Table != w.a.table.ID || d.SavedAt.IsZero() {
		t.Errorf("header = %+v", d)
	}
	if len(d.Characters) != 2 || d.Characters[0].Name != "Dev" || d.Characters[0].Sheet != "Dev Claude" || d.Characters[1].Name != "QA" {
		t.Errorf("characters = %+v; want Dev and QA (the Master never enters)", d.Characters)
	}
	if !slices.Equal(res.Left, []string{"Mestre"}) {
		t.Errorf("left out = %v; want the Master", res.Left)
	}
	if len(d.Scrolls) != 1 || d.Scrolls[0].Name != "Plano" || !strings.Contains(d.Scrolls[0].Content, "Fazer o login") {
		t.Errorf("scrolls = %+v", d.Scrolls)
	}
	if len(d.Connections) != 1 || d.Connections[0].A.Name+d.Connections[0].B.Name != "DevPlano" && d.Connections[0].A.Name+d.Connections[0].B.Name != "PlanoDev" {
		t.Errorf("connections = %+v; want only the manual Dev-Plano", d.Connections)
	}
	if len(d.Quests) != 1 || d.Quests[0].Definition.Name != "Fluxo" || len(d.Quests[0].Definition.Steps) != 2 {
		t.Errorf("quests = %+v", d.Quests)
	}

	raw, _ := json.Marshal(d)
	for _, leak := range []string{"sessao-SECRETA-123", "texto-da-fila-XYZ", dev.ID, qa.ID} {
		if strings.Contains(string(raw), leak) {
			t.Errorf("the document carries %q", leak)
		}
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		t.Fatal(err)
	}
	allowed := []string{"version", "name", "author", "saved_at", "workspace", "table", "characters", "tomes", "scrolls", "connections", "quests", "classes", "structure_only"}
	for k := range top {
		if !slices.Contains(allowed, k) {
			t.Errorf("the document has the field %q, which is not part of the recipe", k)
		}
	}
	for _, c := range []string{"session", "inbox", "wait", "queue", "usage", "state", "round", "issue", "revision"} {
		if strings.Contains(strings.ToLower(string(raw)), `"`+c) {
			t.Errorf("the document has a field of %s", c)
		}
	}
}

// CA-GRIM-02: with a class of workspace A that B does not have, when it is
// restored in a table of B, the user gets the warning with the three options and,
// choosing to copy as a class of the table, the class appears in
// .mestre/mesas/<mesa>/classes/.
func TestAClassOfTheOriginWorkspaceMissingInTheDestinationAsksAndTheCopyLandsInTheTable(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.workspaceClass(w.a, "qa-ws", modestPerms)
	dev := w.recruit(w.a, "Dev Claude", "Dev", "qa-ws")
	w.save(w.a, "Time", grimoires.Selection{Characters: picks(dev.ID)})

	plan, err := w.orc.Grimoires.Plan(w.ctx, w.restoreReq(w.b, "Time", nil))
	if err != nil {
		t.Fatal(err)
	}
	pend := plan.Pending()
	if len(pend) != 1 || pend[0].Name != "qa-ws" || pend[0].Resolution != grimoires.ResolvedChoose ||
		!slices.Equal(pend[0].Options, []grimoires.ClassChoice{grimoires.ChoiceCopy, grimoires.ChoiceMap, grimoires.ChoiceCancel}) {
		t.Fatalf("pending = %+v; want the class with the three options", pend)
	}
	_, err = w.orc.Grimoires.Restore(w.ctx, w.restoreReq(w.b, "Time", nil))
	var need *grimoires.ChoiceRequiredError
	if !errors.As(err, &need) || len(need.Classes) != 1 || need.Classes[0].Name != "qa-ws" {
		t.Fatalf("restore without a choice: %v; want the choice to be required", err)
	}
	if w.has(w.b, "Dev") {
		t.Fatal("something was created before the user chose")
	}

	rep, err := w.orc.Grimoires.Restore(w.ctx, w.restoreReq(w.b, "Time", map[string]grimoires.Choice{"qa-ws": {Action: grimoires.ChoiceCopy}}))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(rep.Copied, []string{"qa-ws"}) || len(rep.Failed) != 0 {
		t.Fatalf("report = %+v", rep)
	}
	dir := filepath.Join(w.orc.Workspaces.Layout().Principal(w.b.slug), ".mestre", "mesas", w.b.table.ID, "classes", "qa-ws")
	data, err := os.ReadFile(filepath.Join(dir, "CLASS.md"))
	if err != nil || !strings.Contains(string(data), "name: qa-ws") {
		t.Fatalf("the class is not in %s: %v", dir, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "permissions.yaml")); err != nil {
		t.Errorf("the permissions of the class were not copied: %v", err)
	}
	c, err := w.b.lib.Approved(w.ctx, "qa-ws", w.b.table.ID)
	if err != nil || c.Scope != classes.ClassTable || c.Table != w.b.table.ID {
		t.Errorf("the copy in B: %+v, %v; want an approved class of the table", c, err)
	}
	if got := w.char(w.b, "Dev"); got.Class != "qa-ws" {
		t.Errorf("Dev in B has the class %q; want qa-ws", got.Class)
	}
	// A is untouched.
	if _, err := w.a.lib.Approved(w.ctx, "qa-ws", w.a.table.ID); err != nil {
		t.Errorf("the class of A: %v", err)
	}
}

func TestTheOtherTwoOptionsMapToAnotherClassOrCancel(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.workspaceClass(w.a, "qa-ws", modestPerms)
	w.workspaceClass(w.b, "qa-b", modestPerms)
	dev := w.recruit(w.a, "Dev Claude", "Dev", "qa-ws")
	w.save(w.a, "Time", grimoires.Selection{Characters: picks(dev.ID)})

	_, err := w.orc.Grimoires.Restore(w.ctx, w.restoreReq(w.b, "Time", map[string]grimoires.Choice{"qa-ws": {Action: grimoires.ChoiceCancel}}))
	if !errors.Is(err, grimoires.ErrCancelled) || w.has(w.b, "Dev") {
		t.Fatalf("cancel: %v, Dev created: %v; want cancelled and nothing made", err, w.has(w.b, "Dev"))
	}
	_, err = w.orc.Grimoires.Restore(w.ctx, w.restoreReq(w.b, "Time", map[string]grimoires.Choice{"qa-ws": {Action: grimoires.ChoiceMap, MapTo: "nao-existe"}}))
	var miss *grimoires.MissingClassesError
	if !errors.As(err, &miss) || w.has(w.b, "Dev") {
		t.Fatalf("map to a class that is not there: %v; want the missing class listed and nothing made", err)
	}
	rep, err := w.orc.Grimoires.Restore(w.ctx, w.restoreReq(w.b, "Time", map[string]grimoires.Choice{"qa-ws": {Action: grimoires.ChoiceMap, MapTo: "qa-b"}}))
	if err != nil || len(rep.Copied) != 0 {
		t.Fatalf("map: %v, %+v", err, rep)
	}
	if got := w.char(w.b, "Dev"); got.Class != "qa-b" {
		t.Errorf("class = %q; want the mapped qa-b", got.Class)
	}
	if _, err := os.Stat(filepath.Join(w.orc.Workspaces.Layout().Principal(w.b.slug), ".mestre", "mesas", w.b.table.ID, "classes", "qa-ws")); err == nil {
		t.Error("a class was copied although the user mapped it")
	}
}

// CA-GRIM-03: with a copied class that exceeds the ceiling of B, when the
// restoration is tried it is refused listing the item in excess and nothing is
// created.
func TestACopiedClassAboveTheCeilingOfTheDestinationRefusesAndCreatesNothing(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.workspaceClass(w.a, "ops", "version: 1\ncommands: {allow: [\"docker *\"], default: ask}\n")
	dev := w.recruit(w.a, "Dev Claude", "Dev", "ops")
	n := w.scroll(w.a, "Plano", "texto", "")
	w.save(w.a, "Ops", grimoires.Selection{Characters: picks(dev.ID), Scrolls: picks(n.Key)})
	w.b.ceiling.set("version: 1\npermissions:\n  commands:\n    deny: [\"docker *\"]\n", t)

	_, err := w.orc.Grimoires.Restore(w.ctx, w.restoreReq(w.b, "Ops", map[string]grimoires.Choice{"ops": {Action: grimoires.ChoiceCopy}}))
	var over *grimoires.CeilingError
	if !errors.As(err, &over) || len(over.Items) != 1 || !strings.Contains(over.Items[0], "ops") || !strings.Contains(over.Items[0], "docker *") {
		t.Fatalf("err = %v; want the ceiling error listing ops and docker *", err)
	}
	if w.has(w.b, "Dev") {
		t.Error("a character was created")
	}
	if notes, _ := w.orc.Scrolls.Store().OfTable(w.ctx, w.b.slug, w.b.table.ID); len(notes) != 0 {
		t.Errorf("%d scrolls were created", len(notes))
	}
	if _, err := os.Stat(filepath.Join(w.orc.Workspaces.Layout().Principal(w.b.slug), ".mestre", "mesas", w.b.table.ID, "classes", "ops")); err == nil {
		t.Error("the class was written")
	}
	// The same grimoire passes in a destination whose ceiling allows it.
	w.b.ceiling.set("version: 1\n", t)
	if _, err := w.orc.Grimoires.Restore(w.ctx, w.restoreReq(w.b, "Ops", map[string]grimoires.Choice{"ops": {Action: grimoires.ChoiceCopy}})); err != nil {
		t.Errorf("with the ceiling lifted: %v", err)
	}
}

// CA-GRIM-04: with a character "Dev" already in the destination, when a
// grimoire with "Dev" is restored, the new one is called "Dev (2)" and the
// summary of the confirmation says so.
func TestANameThatExistsInTheDestinationGetsASuffixShownInTheSummary(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	dev, qa, scroll, _, _ := w.team("")
	w.save(w.a, "Time", grimoires.Selection{Characters: picks(dev.ID, qa.ID), Scrolls: picks(scroll)})

	plan, err := w.orc.Grimoires.Plan(w.ctx, w.restoreReq(w.a, "Time", nil))
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Characters) != 2 || plan.Characters[0] != (grimoires.Rename{From: "Dev", To: "Dev (2)"}) || plan.Characters[1] != (grimoires.Rename{From: "QA", To: "QA (2)"}) {
		t.Fatalf("characters = %+v", plan.Characters)
	}
	if len(plan.Scrolls) != 1 || plan.Scrolls[0].To != "Plano (2)" {
		t.Errorf("scrolls = %+v; want Plano (2)", plan.Scrolls)
	}
	if s := plan.Summary(); !strings.Contains(s, "Dev -> Dev (2)") || !strings.Contains(s, "Plano -> Plano (2)") {
		t.Errorf("summary = %q; want the new names", s)
	}
	rep, err := w.orc.Grimoires.Restore(w.ctx, w.restoreReq(w.a, "Time", nil))
	if err != nil {
		t.Fatal(err)
	}
	if !w.has(w.a, "Dev (2)") || !w.has(w.a, "QA (2)") || !w.has(w.a, "Dev") {
		t.Errorf("the characters are not Dev, QA, Dev (2) and QA (2): %+v", rep)
	}
	// A third time: the limit of 6 holds exactly, and the names go on.
	if _, err := w.orc.Grimoires.Restore(w.ctx, w.restoreReq(w.a, "Time", nil)); err != nil {
		t.Fatal(err)
	}
	if !w.has(w.a, "Dev (3)") || !w.has(w.a, "QA (3)") {
		t.Error("the third restoration did not make Dev (3) and QA (3)")
	}
}

func TestOnlyAnAdministratorSavesRestoresAndDeletesAndTheNameIsUnique(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	dev := w.recruit(w.a, "Dev Claude", "Dev", "")
	observer := grimoires.Actor{ID: "bia", Level: characters.LevelObserver}
	approver := grimoires.Actor{ID: "caio", Level: characters.LevelApprover}
	for _, by := range []grimoires.Actor{observer, approver} {
		if _, err := w.orc.Grimoires.Save(w.ctx, grimoires.SaveRequest{Workspace: w.a.slug, Table: w.a.table.ID, Name: "x", Select: grimoires.Selection{Characters: picks(dev.ID)}, By: by}); !errors.Is(err, grimoires.ErrLevelDenied) {
			t.Errorf("%s saving: %v; want level denied", by.Level, err)
		}
	}
	w.save(w.a, "Time", grimoires.Selection{Characters: picks(dev.ID)})
	if _, err := w.orc.Grimoires.Save(w.ctx, grimoires.SaveRequest{Workspace: w.a.slug, Table: w.a.table.ID, Name: "time", Select: grimoires.Selection{Characters: picks(dev.ID)}, By: admin}); !errors.Is(err, grimoires.ErrNameTaken) {
		t.Errorf("a name repeated: %v; want name taken, for the user to replace or pick another", err)
	}
	w.save(w.a, "Time", grimoires.Selection{Characters: picks(dev.ID)}, func(r *grimoires.SaveRequest) { r.Replace = true })
	if _, err := w.orc.Grimoires.Restore(w.ctx, grimoires.RestoreRequest{Grimoire: "Time", Workspace: w.b.slug, Table: w.b.table.ID, By: observer}); !errors.Is(err, grimoires.ErrLevelDenied) {
		t.Errorf("restoring as an observer: %v", err)
	}
	if err := w.orc.Grimoires.Delete(w.ctx, approver, "Time"); !errors.Is(err, grimoires.ErrLevelDenied) {
		t.Errorf("deleting as an approver: %v", err)
	}
	list, err := w.orc.Grimoires.List(w.ctx, admin)
	if err != nil || len(list) != 1 || list[0].Name != "Time" || list[0].Workspace != w.a.slug || list[0].Characters != 1 || list[0].Author != "Ana" {
		t.Fatalf("list = %+v, %v", list, err)
	}
	rep, err := w.orc.Grimoires.Restore(w.ctx, w.restoreReq(w.b, "Time", nil))
	if err != nil {
		t.Fatal(err)
	}
	// Deleting the grimoire does not touch the table that was restored.
	if err := w.orc.Grimoires.Delete(w.ctx, admin, "Time"); err != nil {
		t.Fatal(err)
	}
	if !w.has(w.b, "Dev") || len(rep.Placed) != 1 {
		t.Error("the restored character is gone with the grimoire")
	}
	if _, err := w.orc.Grimoires.Get(w.ctx, admin, "Time"); !errors.Is(err, grimoires.ErrNoGrimoire) {
		t.Errorf("after the delete: %v", err)
	}
}

func TestARestorationIsRefusedInATableThatIsNotWorkingOrInReviewAndForAnUnknownVersion(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	dev := w.recruit(w.a, "Dev Claude", "Dev", "")
	w.save(w.a, "Time", grimoires.Selection{Characters: picks(dev.ID)})
	// Only a table that is working or in review takes a grimoire.
	for _, c := range []struct {
		state characters.TableState
		ok    bool
	}{
		{characters.TableOpen, false}, {characters.TableWorking, true}, {characters.TableInReview, true}, {characters.TableClosing, false},
		{characters.TableClosed, false}, {characters.TableCancelling, false}, {characters.TableCancelled, false},
	} {
		svc, err := grimoires.NewService(grimoires.Options{Store: w.orc.Grimoires.Store(), Clock: w.clk, Registry: w.orc.Registry, Characters: w.orc.Characters, Scrolls: w.orc.Scrolls,
			Connections: w.orc.Connections, Quests: w.orc.Quests, Tables: tableIn{c.state}, Inbox: w.orc.Inbox, Sheets: orcfakes.Presets{}})
		if err != nil {
			t.Fatal(err)
		}
		_, err = svc.Plan(w.ctx, grimoires.RestoreRequest{Grimoire: "Time", Workspace: w.b.slug, Table: w.b.table.ID, By: admin})
		if c.ok != (err == nil) || !c.ok && !errors.Is(err, grimoires.ErrTableState) {
			t.Errorf("table %s: %v; want ok=%v", c.state, err, c.ok)
		}
	}
	req := grimoires.RestoreRequest{Grimoire: "Time", Workspace: w.a.slug, Table: w.a.table.ID, By: admin}
	// A table closing or cancelled is refused too.
	req.Table = "nao-existe"
	if _, err := w.orc.Grimoires.Restore(w.ctx, req); !errors.Is(err, grimoires.ErrBadRequest) {
		t.Errorf("restoring in a table that is not there: %v", err)
	}
	// A grimoire from a newer engine is refused, saying the version expected.
	if err := w.orc.Grimoires.Store().Put(w.ctx, grimoires.Document{Version: 2, Name: "Futuro", Author: "x", Workspace: w.a.slug, Table: w.a.table.ID}, false, w.clk.Now()); err != nil {
		t.Fatal(err)
	}
	_, err := w.orc.Grimoires.Restore(w.ctx, w.restoreReq(w.b, "Futuro", nil))
	var ve *grimoires.VersionError
	if !errors.As(err, &ve) || ve.Have != 2 || ve.Max != 1 || !strings.Contains(ve.Error(), "up to version 1") {
		t.Errorf("a newer version: %v; want the version expected", err)
	}
	list, _ := w.orc.Grimoires.List(w.ctx, admin)
	if len(list) != 2 {
		t.Errorf("list = %+v; the grimoire of a newer version still shows", list)
	}
}

func TestSheetsThatAreMissingAndLimitsThatDoNotHoldRefuseBeforeCreatingAnything(t *testing.T) {
	t.Parallel()
	w := newWorld(t, worldOpts{perTable: 3})
	dev := w.recruit(w.a, "Dev Claude", "Dev", "")
	qa := w.recruit(w.a, "Dev Claude", "QA", "")
	w.save(w.a, "Time", grimoires.Selection{Characters: picks(dev.ID, qa.ID)})

	// Dev and QA are in the table: the limit is 3, so one more fits.
	_, err := w.orc.Grimoires.Restore(w.ctx, w.restoreReq(w.a, "Time", nil))
	var lim *grimoires.LimitError
	if !errors.As(err, &lim) || lim.Need != 2 || lim.Room != 1 {
		t.Fatalf("err = %v; want the limit error with room for 1", err)
	}
	if w.has(w.a, "Dev (2)") || w.has(w.a, "QA (2)") {
		t.Error("part of the team was created")
	}

	doc := grimoires.Document{Version: 1, Name: "Fichas", Author: "x", Workspace: w.a.slug, Table: w.a.table.ID, Characters: []grimoires.Character{
		{Name: "A", Sheet: "Fantasma Um"}, {Name: "B", Sheet: "Dev Claude"}, {Name: "C", Sheet: "Fantasma Dois"}}}
	if err := w.orc.Grimoires.Store().Put(w.ctx, doc, false, w.clk.Now()); err != nil {
		t.Fatal(err)
	}
	_, err = w.orc.Grimoires.Restore(w.ctx, w.restoreReq(w.b, "Fichas", nil))
	var miss *grimoires.MissingSheetsError
	if !errors.As(err, &miss) || !slices.Equal(miss.Sheets, []string{"Fantasma Dois", "Fantasma Um"}) {
		t.Fatalf("err = %v; want both missing sheets listed", err)
	}
	if w.has(w.b, "B") {
		t.Error("a character was created")
	}
}

func TestOnlyTheStructureOfTheScrollsCanBeSavedAndATomeTakesItsScrolls(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	a := w.scroll(w.a, "Requisitos", "segredo do cliente", "Docs")
	b := w.scroll(w.a, "Notas", "mais texto", "Docs")
	solo := w.scroll(w.a, "Solta", "sozinha", "")
	tome, _, err := w.orc.Scrolls.Store().GetTome(w.ctx, a.Tome)
	_ = tome
	if err != nil {
		t.Fatal(err)
	}
	res := w.save(w.a, "Docs", grimoires.Selection{Tomes: picks(a.Tome), Scrolls: picks(solo.Key)}, func(r *grimoires.SaveRequest) { r.StructureOnly = true })
	d := res.Document
	if len(d.Tomes) != 1 || d.Tomes[0].Name != "Docs" || len(d.Scrolls) != 3 {
		t.Fatalf("document = %+v; want the tome Docs and its two scrolls plus the loose one", d)
	}
	for _, s := range d.Scrolls {
		if s.Content != "" {
			t.Errorf("scroll %s kept its content", s.Name)
		}
	}
	_ = b
	rep, err := w.orc.Grimoires.Restore(w.ctx, w.restoreReq(w.b, "Docs", nil))
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Failed) != 0 {
		t.Fatalf("failed: %v", rep.Failed)
	}
	got, ok, err := w.orc.Scrolls.Store().Find(w.ctx, w.b.slug, w.b.table.ID, "Requisitos")
	if err != nil || !ok || got.TomeName != "Docs" {
		t.Errorf("Requisitos in B = %+v, %v; want it in the tome Docs", got, err)
	}
	_, text, _ := w.orc.Scrolls.Read(w.ctx, scrollsRead(w, "Requisitos"))
	if text != "" {
		t.Errorf("the content %q came back", text)
	}
}

func TestTheSelectionThatCannotBeSavedIsRefusedWithEveryProblem(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	dev, _, _, _, quest := w.team("")
	_, err := w.orc.Grimoires.Save(w.ctx, grimoires.SaveRequest{Workspace: w.a.slug, Table: w.a.table.ID, Name: "Torto", By: admin,
		Select: grimoires.Selection{Characters: picks(dev.ID, "nao-existe"), Scrolls: picks("x/y/p9999"), Quests: picks(quest)}})
	var sel *grimoires.SelectionError
	if !errors.As(err, &sel) || len(sel.Problems) != 3 {
		t.Fatalf("err = %v; want three problems (a character, a scroll and the quest that uses QA)", err)
	}
	if !strings.Contains(sel.Error(), "QA") {
		t.Errorf("the error does not name the character outside the selection: %v", err)
	}
	if list, _ := w.orc.Grimoires.List(w.ctx, admin); len(list) != 0 {
		t.Errorf("a grimoire was saved: %+v", list)
	}
}

// tableIn is the table in a given state.
type tableIn struct{ state characters.TableState }

func (t tableIn) Table(_ context.Context, ws, id string) (characters.TableInfo, error) {
	return characters.TableInfo{ID: id, Workspace: ws, State: t.state}, nil
}

func scrollsRead(w *world, name string) scrolls.ReadRequest {
	return scrolls.ReadRequest{Workspace: w.b.slug, Table: w.b.table.ID, Actor: scrolls.Engine, Name: name}
}
