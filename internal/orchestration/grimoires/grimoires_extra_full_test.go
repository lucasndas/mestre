//go:build full

package grimoires_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/grimoires"
	"github.com/lucasndas/mestre/internal/orchestration/scrolls"
	"github.com/lucasndas/mestre/internal/testkit/orcfakes"
)

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

func scrollsRead(w *world, name string) scrolls.ReadRequest {
	return scrolls.ReadRequest{Workspace: w.b.slug, Table: w.b.table.ID, Actor: scrolls.Engine, Name: name}
}

func (t tableIn) Table(_ context.Context, ws, id string) (characters.TableInfo, error) {
	return characters.TableInfo{ID: id, Workspace: ws, State: t.state}, nil
}
