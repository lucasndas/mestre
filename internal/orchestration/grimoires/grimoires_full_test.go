//go:build full

package grimoires_test

import (
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/connections"
	"github.com/lucasndas/mestre/internal/orchestration/grimoires"
	"github.com/lucasndas/mestre/internal/orchestration/inbox"
	"github.com/lucasndas/mestre/internal/orchestration/quests"
	"github.com/lucasndas/mestre/internal/orchestration/scrolls"
)

// CA-GRIM-05: with a valid grimoire, when it is restored, the characters are born
// connected as they were saved, the scrolls have the content that was saved and
// the quest exists as a draft with its issue under the epic of the destination.
func TestARestoredGrimoireBringsTheTeamConnectedTheScrollsFilledAndTheQuestAsADraft(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	dev, qa, scroll, _, quest := w.team("")
	w.link(w.a, connections.Char(dev.ID), connections.Char(qa.ID))
	n := w.scroll(w.a, "Decisões", "Usar sessões curtas.\n", "Docs")
	w.scroll(w.a, "Notas", "Uma nota do tome.\n", "Docs")
	w.link(w.a, connections.Scroll(n.Key), connections.Char(qa.ID))
	if err := w.orc.Characters.Pin(w.ctx, dev.ID, true, characters.Creator{Kind: characters.CreatedByUser, ID: "ana", Level: characters.LevelAdministrator}); err != nil {
		t.Fatal(err)
	}
	at := &grimoires.Position{X: 120, Y: 40}
	w.save(w.a, "Time de login", grimoires.Selection{
		Characters: []grimoires.Pick{{ID: dev.ID, Position: at}, {ID: qa.ID}}, Scrolls: picks(scroll), Tomes: picks(n.Tome), Quests: picks(quest)})

	rep, err := w.orc.Grimoires.Restore(w.ctx, w.restoreReq(w.b, "Time de login", nil))
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Failed) != 0 || len(rep.Pending) != 0 || len(rep.Interrupted) != 0 {
		t.Fatalf("report = %+v", rep)
	}

	// The characters are born in B, with the sheets, and the pin.
	devB, qaB := w.char(w.b, "Dev"), w.char(w.b, "QA")
	if devB.Sheet != "Dev Claude" || qaB.Sheet != "Revisor Codex" || !devB.Pinned || qaB.Pinned {
		t.Errorf("Dev = %+v, QA = %+v", devB, qaB)
	}
	if devB.CreatedBy.Kind != characters.CreatedByUser || devB.CreatedBy.ID != "ana" {
		t.Errorf("Dev was created by %+v; want the user that restored", devB.CreatedBy)
	}
	var placed *grimoires.Placement
	for i, p := range rep.Placed {
		if p.Kind == "character" && p.Name == "Dev" {
			placed = &rep.Placed[i]
		}
	}
	if placed == nil || placed.Position == nil || *placed.Position != *at || placed.ID != devB.ID {
		t.Errorf("placement of Dev = %+v; want the position that was saved", placed)
	}

	// The connections are the ones that were saved: Dev-QA and Dev-Plano, QA-Decisões; and
	// the ones the engine makes by itself (the Master).
	st := w.orc.Connections.Store()
	manual := func(a, b connections.Ref) bool {
		c, ok, err := st.Between(w.ctx, a, b)
		return err == nil && ok && c.Has(connections.ReasonManual)
	}
	plano, _, _ := w.orc.Scrolls.Store().Find(w.ctx, w.b.slug, w.b.table.ID, "Plano")
	decisions, _, _ := w.orc.Scrolls.Store().Find(w.ctx, w.b.slug, w.b.table.ID, "Decisões")
	if !manual(connections.Char(devB.ID), connections.Char(qaB.ID)) || !manual(connections.Char(devB.ID), connections.Scroll(plano.Key)) {
		t.Error("Dev, QA and Plano are not connected as they were saved")
	}
	if !manual(connections.Char(qaB.ID), connections.Tome(decisions.Tome)) && !manual(connections.Char(qaB.ID), connections.Scroll(decisions.Key)) {
		t.Error("QA is not connected to Decisões")
	}
	if auto, ok, _ := st.Between(w.ctx, connections.Char(devB.ID), connections.Char(w.b.master.ID)); !ok || auto.Has(connections.ReasonManual) {
		t.Errorf("Dev and the Master of B: %+v, %v; want the connection of the table, not a manual one", auto, ok)
	}
	if _, ok, _ := st.Between(w.ctx, connections.Char(devB.ID), connections.Char(w.a.master.ID)); ok {
		t.Error("Dev of B is connected to the Master of A")
	}

	// The scrolls have what was saved, and the tome came with them.
	_, text, err := w.orc.Scrolls.Read(w.ctx, scrolls.ReadRequest{Workspace: w.b.slug, Table: w.b.table.ID, Actor: scrolls.Engine, Name: "Plano"})
	if err != nil || text != "# Plano\nFazer o login.\n" {
		t.Errorf("Plano in B = %q, %v", text, err)
	}
	_, text, err = w.orc.Scrolls.Read(w.ctx, scrolls.ReadRequest{Workspace: w.b.slug, Table: w.b.table.ID, Actor: scrolls.Engine, Name: "Decisões"})
	if err != nil || text != "Usar sessões curtas.\n" || decisions.TomeName != "Docs" {
		t.Errorf("Decisões in B = %q in the tome %q, %v", text, decisions.TomeName, err)
	}

	// The quest is a draft of B, with its issue under the epic of B.
	q, err := w.orc.Quests.Get(w.ctx, w.b.slug, w.b.table.ID, "Fluxo")
	if err != nil {
		t.Fatal(err)
	}
	if q.State != quests.Draft || q.Round != 0 || len(q.Def.Steps) != 2 || q.Def.Steps[0].Character != "Dev" {
		t.Errorf("quest = %s round %d %+v; want a draft with the two steps", q.State, q.Round, q.Def.Steps)
	}
	if q.Issue == 0 || w.hub.epicOf(q.Issue) != w.b.epic {
		t.Errorf("issue %d is under the epic %d; want the epic %d of the destination", q.Issue, w.hub.epicOf(q.Issue), w.b.epic)
	}
	qA, _ := w.orc.Quests.Get(w.ctx, w.a.slug, w.a.table.ID, "Fluxo")
	if qA.ID == q.ID || qA.State != quests.Draft {
		t.Errorf("the quest of A changed or is the same: %+v", qA)
	}

	// The Master of B was told what was restored.
	told := w.items(w.b.master, inbox.KindSystem)
	if len(told) != 1 {
		t.Fatalf("the Master of B got %d system items", len(told))
	}
	for _, want := range []string{"Time de login", "Dev", "QA", "Plano", "Fluxo"} {
		if !strings.Contains(told[0].Text, want) {
			t.Errorf("the item to the Master = %q; want %q in it", told[0].Text, want)
		}
	}

	// Restored again in the same table: the names go to (2), and the quest follows its characters.
	if _, err := w.orc.Grimoires.Restore(w.ctx, w.restoreReq(w.b, "Time de login", nil)); err != nil {
		t.Fatal(err)
	}
	q2, err := w.orc.Quests.Get(w.ctx, w.b.slug, w.b.table.ID, "Fluxo (2)")
	if err != nil {
		t.Fatal(err)
	}
	if q2.Def.Steps[0].Character != "Dev (2)" || q2.Def.Steps[1].Character != "QA (2)" {
		t.Errorf("steps of the second quest = %+v; want them to follow the new names", q2.Def.Steps)
	}
}
