//go:build full

package app_test

import (
	"testing"

	"github.com/mestre-ia/mestre/internal/app"
	"github.com/mestre-ia/mestre/internal/orchestration/canvas"
	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/panel"
)

// CA-CLI-COORD-04: the new terminal uses the new sheet and keeps the name, the
// two connections and the place on the canvas of the one it replaces.
func TestRecruitReplaceKeepsTheNameAndTheConnections(t *testing.T) {
	t.Parallel()
	w := newCommWorld(t)
	dev1, rev := w.recruit("Dev-1", "Dev Claude"), w.recruit("Revisor", "Revisor Codex")
	if r := w.run(w.master, "connect", "Dev-1", "Revisor"); r.code != 0 {
		t.Fatalf("connect: %+v", r)
	}
	host := app.NewCanvasHostForTest(w.orc, w.clk)
	place := panel.CanvasItem{Key: canvas.Key(canvas.KindCharacter, dev1.ID), X: 840, Y: -120, W: 720, H: 480, Parent: "group:time"}
	if err := host.SaveLayout(w.ctx, w.slug, w.table.ID, panel.LayoutRequest{Items: []panel.CanvasItem{place, {Key: "group:time", Label: "Time", W: 2000, H: 1000}}}); err != nil {
		t.Fatal(err)
	}
	before := connectedNames(t, w, dev1)
	if len(before) != 2 {
		t.Fatalf("Dev-1 is connected to %v; want the master and the Reviewer", before)
	}

	r := w.run(w.master, "recruit", "--replace", "Dev-1", "--sheet", "Dev Codex", "--json")
	v := decodeJSON(t, r)
	if r.code != 0 || v.Data["name"] != "Dev-1" || v.Data["sheet"] != "Dev Codex" {
		t.Fatalf("recruit --replace = %+v", r)
	}
	now, err := w.orc.Registry.ByName(w.ctx, w.slug, w.table.ID, "Dev-1")
	if err != nil {
		t.Fatal(err)
	}
	if now.ID == dev1.ID || now.Sheet != "Dev Codex" || !now.State.Alive() {
		t.Errorf("the new Dev-1: %+v", now)
	}
	if old := w.get(dev1); old.State.Alive() {
		t.Errorf("the old Dev-1 is %s", old.State)
	}
	if got := connectedNames(t, w, now); len(got) != 2 || got[0] != before[0] || got[1] != before[1] {
		t.Errorf("Dev-1 is connected to %v, was %v", got, before)
	}
	_ = rev
	st, err := host.State(w.ctx, w.slug, w.table.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := place
	want.Key = canvas.Key(canvas.KindCharacter, now.ID)
	var at []panel.CanvasItem
	for _, it := range st.Layout {
		if it.Key == want.Key || it.Key == place.Key {
			at = append(at, it)
		}
	}
	if len(at) != 1 || at[0] != want {
		t.Errorf("the places of the old and the new Dev-1 are %+v; want only %+v", at, want)
	}
	// A busy character is replaced at its safe point.
	busy := w.recruit("Dev-2", "Dev Claude")
	w.mach.Report(w.ctx, busy.ID, characters.Busy)
	r = w.run(w.master, "recruit", "--replace", "Dev-2", "--sheet", "Dev Codex", "--json")
	if v := decodeJSON(t, r); r.code != 9 || v.Error.Code != "scheduled" {
		t.Fatalf("recruit --replace of a busy character = %+v", r)
	}
	if got := w.get(busy); !got.State.Alive() {
		t.Errorf("the busy character was ended: %s", got.State)
	}
}

func connectedNames(t *testing.T, w *commWorld, c characters.Character) []string {
	t.Helper()
	l := decodeJSON(t, w.run(c, "list", "--json"))
	var out []string
	for _, a := range l.Data["agents"].([]any) {
		out = append(out, a.(map[string]any)["name"].(string))
	}
	return out
}
