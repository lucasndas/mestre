package grimoires_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/connections"
	"github.com/lucasndas/mestre/internal/orchestration/grimoires"
	"github.com/lucasndas/mestre/internal/orchestration/inbox"
	"github.com/lucasndas/mestre/internal/orchestration/quests"
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

// Part of CA-GRIM-04: the name of a quest has a limit (docs/orquestracao/missoes.md#definição), so
// the suffix of a name that exists in the destination makes room for itself.
// CA-GRIM-08
func TestTheNameOfAQuestAtTheLimitStillFitsWithItsSuffix(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	dev := w.recruit(w.a, "Dev Claude", "Dev", "")
	qa := w.recruit(w.a, "Revisor Codex", "QA", "")
	name := strings.Repeat("q", quests.MaxName)
	v := w.quest(w.a, "name: "+name+"\nsteps:\n  - {id: dev, character: Dev, instructions: \"Faça o login\"}\n  - {id: qa, character: QA, after: [dev]}\n")
	w.save(w.a, "Longo", grimoires.Selection{Characters: picks(dev.ID, qa.ID), Quests: picks(v.ID)})

	plan, err := w.orc.Grimoires.Plan(w.ctx, w.restoreReq(w.a, "Longo", nil))
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Quests) != 1 || utf8.RuneCountInString(plan.Quests[0].To) > quests.MaxName || !strings.HasSuffix(plan.Quests[0].To, " (2)") {
		t.Fatalf("quests = %+v, want the name shortened to fit its suffix in %d characters", plan.Quests, quests.MaxName)
	}
	rep, err := w.orc.Grimoires.Restore(w.ctx, w.restoreReq(w.a, "Longo", nil))
	if err != nil || len(rep.Failed) != 0 {
		t.Fatalf("restore = %+v, %v; want the quest made", rep, err)
	}
	if _, err := w.orc.Quests.Get(w.ctx, w.a.slug, w.a.table.ID, plan.Quests[0].To); err != nil {
		t.Errorf("the quest %q was not made: %v", plan.Quests[0].To, err)
	}
}
