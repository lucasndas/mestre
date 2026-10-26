package app_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/mestre-ia/mestre/internal/app"
	"github.com/mestre-ia/mestre/internal/orchestration/canvas"
	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/orchestration/grimoires"
	"github.com/mestre-ia/mestre/internal/orchestration/scrolls"
	"github.com/mestre-ia/mestre/internal/panel"
	"github.com/mestre-ia/mestre/internal/users"
)

var (
	canvasAdmin    = users.User{ID: "u-adm", Name: "Ana", Level: users.Administrator, State: users.Active}
	canvasObserver = users.User{ID: "u-obs", Name: "Beto", Level: users.Observer, State: users.Active}
)

// anyWorkspace lets every user use every workspace (the production access reads the users).
type anyWorkspace struct{}

func (anyWorkspace) CanUse(context.Context, grimoires.Actor, string) bool { return true }

func char(id string) panel.CanvasRef { return panel.CanvasRef{Kind: "character", ID: id} }

func connected(st panel.CanvasState, a, b string) (panel.CanvasConnection, bool) {
	for _, c := range st.Connections {
		if c.A.ID == a && c.B.ID == b || c.A.ID == b && c.B.ID == a {
			return c, true
		}
	}
	return panel.CanvasConnection{}, false
}

// Part of CA-CANVAS-01 (the heavy part is milestone 7): the state of the table has
// the characters with the machine badge, the connections and the places.
func TestCanvasStateHasTheElementsOfTheTable(t *testing.T) {
	t.Parallel()
	w := newCommWorld(t)
	dev := w.recruit("Dev-1", "Dev Claude")
	host := app.NewCanvasHostForTest(w.orc, w.clk)

	st, err := host.State(w.ctx, w.slug, w.table.ID)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, c := range st.Characters {
		names = append(names, c.Name)
		if c.MachineName == "" || c.State == "" {
			t.Errorf("%s has no machine badge or state: %+v", c.Name, c)
		}
	}
	if !slices.Contains(names, "Dev-1") || !slices.Contains(names, w.master.Name) {
		t.Errorf("the characters are %v; want Dev-1 and the table master", names)
	}
	if _, ok := connected(st, dev.ID, w.master.ID); !ok {
		t.Errorf("Dev-1 is not connected to the table master: %+v", st.Connections)
	}
	places := map[string]bool{}
	for _, it := range st.Layout {
		places[it.Key] = true
	}
	for _, c := range st.Characters {
		if !places[canvas.Key(canvas.KindCharacter, c.ID)] {
			t.Errorf("%s has no place on the canvas", c.Name)
		}
	}
}

// CA-CANVAS-10
// CA-CONX-05
// The automatic connection of
// the table master to the Grand Master is refused with its reason, and the one a
// user made goes away.
func TestCanvasRemovesTheManualConnectionAndRefusesTheAutomaticOne(t *testing.T) {
	t.Parallel()
	w := newCommWorld(t)
	dev, rev := w.recruit("Dev-1", "Dev Claude"), w.recruit("Revisor", "Revisor Codex")
	host := app.NewCanvasHostForTest(w.orc, w.clk)

	if err := host.Connect(w.ctx, canvasAdmin, w.slug, w.table.ID, char(dev.ID), char(rev.ID)); err != nil {
		t.Fatal(err)
	}
	st, _ := host.State(w.ctx, w.slug, w.table.ID)
	if c, ok := connected(st, dev.ID, rev.ID); !ok || !c.Manual {
		t.Fatalf("the drawn connection is %+v (found %v)", c, ok)
	}
	if _, ok := connected(st, w.master.ID, w.gm.ID); !ok {
		t.Fatalf("the automatic connection to the Grand Master is not on the canvas: %+v", st.Connections)
	}

	err := host.Disconnect(w.ctx, canvasAdmin, w.slug, w.table.ID, char(w.master.ID), char(w.gm.ID))
	var ref *panel.Refusal
	if !errors.As(err, &ref) || ref.Reason != "automatic_connection" {
		t.Fatalf("removing the automatic connection = %v; want the refusal automatic_connection", err)
	}
	if got, _ := ref.Params["reasons"].([]string); !slices.Contains(got, "mestre") {
		t.Errorf("the refusal carries the reasons %v; want mestre", ref.Params["reasons"])
	}
	if err := host.Disconnect(w.ctx, canvasAdmin, w.slug, w.table.ID, char(dev.ID), char(rev.ID)); err != nil {
		t.Fatalf("removing the manual connection: %v", err)
	}
	st, _ = host.State(w.ctx, w.slug, w.table.ID)
	if _, ok := connected(st, dev.ID, rev.ID); ok {
		t.Error("the manual connection is still there")
	}
	if _, ok := connected(st, w.master.ID, w.gm.ID); !ok {
		t.Error("the automatic connection went away")
	}
}

// Part of CA-CANVAS-05: the engine refuses a drag with its reason, and an
// Observer cannot connect.
func TestCanvasRefusesWhatTheEngineRefuses(t *testing.T) {
	t.Parallel()
	w := newCommWorld(t)
	dev := w.recruit("Dev-1", "Dev Claude")
	host := app.NewCanvasHostForTest(w.orc, w.clk)
	cases := map[string]struct {
		by   users.User
		a, b panel.CanvasRef
		want string
	}{
		"an observer":         {canvasObserver, char(dev.ID), char(w.master.ID), "level_denied"},
		"to itself":           {canvasAdmin, char(dev.ID), char(dev.ID), "same_item"},
		"to the Grand Master": {canvasAdmin, char(dev.ID), char(w.gm.ID), "grand_master"},
		"to nobody":           {canvasAdmin, char(dev.ID), char("nobody"), "not_found"},
	}
	for name, c := range cases {
		err := host.Connect(w.ctx, c.by, w.slug, w.table.ID, c.a, c.b)
		var ref *panel.Refusal
		if !errors.As(err, &ref) || ref.Reason != c.want {
			t.Errorf("%s: %v; want the refusal %s", name, err, c.want)
		}
	}
}

// CA-CANVAS-06: what the user moved is where it was after the page loads again.
func TestCanvasKeepsTheLayoutPerTable(t *testing.T) {
	t.Parallel()
	w := newCommWorld(t)
	dev := w.recruit("Dev-1", "Dev Claude")
	host := app.NewCanvasHostForTest(w.orc, w.clk)
	key := canvas.Key(canvas.KindCharacter, dev.ID)
	req := panel.LayoutRequest{Items: []panel.CanvasItem{
		{Key: key, X: 1200, Y: -300, W: 700, H: 500, Parent: "group:g1"},
		{Key: "group:g1", Label: "Revisão", X: 900, Y: -400, W: 1200, H: 800},
	}}
	if err := host.SaveLayout(w.ctx, w.slug, w.table.ID, req); err != nil {
		t.Fatal(err)
	}
	st, err := host.State(w.ctx, w.slug, w.table.ID)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]panel.CanvasItem{}
	for _, it := range st.Layout {
		got[it.Key] = it
	}
	if got[key] != req.Items[0] || got["group:g1"] != req.Items[1] {
		t.Errorf("the layout is %+v; want %+v", got, req.Items)
	}
	if err := host.SaveLayout(w.ctx, w.slug, w.table.ID, panel.LayoutRequest{Remove: []string{"group:g1"}}); err != nil {
		t.Fatal(err)
	}
	st, _ = host.State(w.ctx, w.slug, w.table.ID)
	for _, it := range st.Layout {
		if it.Key == "group:g1" {
			t.Error("the group was not removed")
		}
		if it.Key == key && it.Parent != "" {
			t.Error("the character stayed in a group that is gone")
		}
	}
}

func TestCanvasPinsACharacterOnlyForAnAdministrator(t *testing.T) {
	t.Parallel()
	w := newCommWorld(t)
	dev := w.recruit("Dev-1", "Dev Claude")
	host := app.NewCanvasHostForTest(w.orc, w.clk)
	var ref *panel.Refusal
	if err := host.Pin(w.ctx, canvasObserver, w.slug, w.table.ID, dev.ID, true); !errors.As(err, &ref) || ref.Reason != "level_denied" {
		t.Fatalf("an observer pinning = %v", err)
	}
	if err := host.Pin(w.ctx, canvasAdmin, w.slug, w.table.ID, dev.ID, true); err != nil {
		t.Fatal(err)
	}
	if got := w.get(dev); !got.Pinned {
		t.Error("the character is not pinned")
	}
	if err := host.Pin(w.ctx, canvasAdmin, w.slug, w.table.ID, dev.ID, false); err != nil || w.get(dev).Pinned {
		t.Errorf("unpin: %v, pinned %v", err, w.get(dev).Pinned)
	}
	_ = characters.LevelOwner
}

// Part of CA-CANVAS-01 and the mission actions of the canvas: arrows drawn between
// characters make a draft mission with the numbered steps, and drawing them again
// replaces the steps.
func TestCanvasDrawsAndRedrawsAMission(t *testing.T) {
	t.Parallel()
	w := newCommWorld(t)
	w.recruit("Dev-1", "Dev Claude")
	w.recruit("Revisor", "Revisor Codex")
	host := app.NewCanvasHostForTest(w.orc, w.clk)

	q, err := host.CreateQuest(w.ctx, canvasAdmin, w.slug, w.table.ID, panel.QuestRequest{Name: "entrega", Steps: []panel.QuestStepDrawn{
		{ID: "dev", Character: "Dev-1"},
		{ID: "rev", Character: "Revisor", After: []string{"dev"}, CanReturnTo: "dev"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !q.Editable || len(q.Steps) != 2 || q.Steps[1].After[0] != "dev" || q.Steps[1].ReturnTo != "dev" {
		t.Fatalf("the mission is %+v", q)
	}
	q, err = host.EditQuest(w.ctx, canvasAdmin, w.slug, w.table.ID, q.ID, panel.QuestRequest{Steps: []panel.QuestStepDrawn{
		{ID: "rev", Character: "Revisor"},
		{ID: "dev", Character: "Dev-1", After: []string{"rev"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if q.Name != "entrega" || q.Steps[0].ID != "rev" || q.Steps[1].After[0] != "rev" {
		t.Errorf("the redrawn mission is %+v", q)
	}
	st, _ := host.State(w.ctx, w.slug, w.table.ID)
	if len(st.Quests) != 1 || st.Quests[0].Steps[0].ID != "rev" {
		t.Errorf("the state has the quests %+v", st.Quests)
	}
	// a step that names nobody is refused, and so is an Observer
	var ref *panel.Refusal
	if _, err := host.CreateQuest(w.ctx, canvasAdmin, w.slug, w.table.ID, panel.QuestRequest{Name: "x", Steps: []panel.QuestStepDrawn{{ID: "a", Character: "Ninguém"}}}); !errors.As(err, &ref) || ref.Reason != "invalid" {
		t.Errorf("a step with no character = %v", err)
	}
	if _, err := host.CreateQuest(w.ctx, canvasObserver, w.slug, w.table.ID, panel.QuestRequest{Name: "y"}); !errors.As(err, &ref) || ref.Reason != "level_denied" {
		t.Errorf("an observer drawing a mission = %v", err)
	}
}

// A scroll written at the canvas is the scroll of the engine, with its revision: a
// write over an old revision is a conflict.
func TestCanvasEditsAScrollOverItsRevision(t *testing.T) {
	t.Parallel()
	w := newCommWorld(t)
	host := app.NewCanvasHostForTest(w.orc, w.clk)
	n, err := w.orc.Scrolls.Create(w.ctx, scrolls.CreateRequest{Workspace: w.slug, Table: w.table.ID, Actor: scrolls.Engine, Content: "# Plano\n"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := host.WriteScroll(w.ctx, canvasAdmin, w.slug, w.table.ID, n.Key, panel.ScrollWriteRequest{Content: "# Plano\nfeito\n", Base: n.Revision})
	if err != nil || got.Revision != n.Revision+1 {
		t.Fatalf("write = %+v, %v", got, err)
	}
	if _, err := host.WriteScroll(w.ctx, canvasAdmin, w.slug, w.table.ID, n.Key, panel.ScrollWriteRequest{Content: "outro", Base: n.Revision}); !errors.Is(err, panel.ErrCanvasConflict) {
		t.Errorf("a write over an old revision = %v; want a conflict", err)
	}
	if _, err := host.WriteScroll(w.ctx, canvasObserver, w.slug, w.table.ID, n.Key, panel.ScrollWriteRequest{Content: "x", Base: got.Revision}); err == nil {
		t.Error("an observer wrote a scroll")
	}
	st, _ := host.State(w.ctx, w.slug, w.table.ID)
	if len(st.Scrolls) == 0 || !strings.Contains(st.Scrolls[0].Content, "feito") {
		t.Errorf("the state has the scrolls %+v", st.Scrolls)
	}
}

// Save a selection as a grimoire; the list shows it.
func TestCanvasSavesASelectionAsAGrimoire(t *testing.T) {
	t.Parallel()
	w := newCommWorld(t, func(_ *commWorld, o *app.OrchestrationOptions) { o.GrimoireAccess = anyWorkspace{} })
	dev := w.recruit("Dev-1", "Dev Claude")
	host := app.NewCanvasHostForTest(w.orc, w.clk)
	g, err := host.SaveGrimoire(w.ctx, canvasAdmin, w.slug, w.table.ID, panel.GrimoireSaveRequest{Name: "time", Selection: []panel.CanvasItem{{Key: canvas.Key(canvas.KindCharacter, dev.ID), X: 10, Y: 20}}})
	if err != nil || g.Characters != 1 {
		t.Fatalf("save = %+v, %v", g, err)
	}
	list, err := host.Grimoires(w.ctx, canvasAdmin)
	if err != nil || len(list) != 1 || list[0].Name != "time" {
		t.Fatalf("list = %+v, %v", list, err)
	}
	var ref *panel.Refusal
	if _, err := host.SaveGrimoire(w.ctx, canvasAdmin, w.slug, w.table.ID, panel.GrimoireSaveRequest{Name: "time", Selection: []panel.CanvasItem{{Key: canvas.Key(canvas.KindCharacter, dev.ID)}}}); !errors.As(err, &ref) || ref.Reason != "name_taken" {
		t.Errorf("a repeated name = %v", err)
	}
	if _, err := host.Grimoires(w.ctx, canvasObserver); err == nil {
		t.Error("an observer listed the grimoires")
	}
}
