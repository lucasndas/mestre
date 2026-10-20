package app_test

import (
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/inbox"
)

// Part of CA-MM-08 (the criterion is *pesada* with the real providers, M7): the
// table master gives back a request of another area with the reason and the
// message it came from; the Grand Master receives the reason and the original
// text, and the table is as it was.
func TestTheTableMasterGivesBackWhatIsNotOfTheTableAndNothingChanges(t *testing.T) {
	t.Parallel()
	w := newCommWorld(t)
	text := "Quero um relatório mensal de vendas.\n\n  Em PDF — obrigado!\n"
	it, err := w.orc.Inbox.Enqueue(w.ctx, inbox.NewItem{Workspace: w.slug, Target: w.master.ID, Kind: inbox.KindUser, Sender: "Lucas", Text: text,
		Origin: &inbox.Origin{Channel: "telegram", User: "u1", UserName: "Lucas", Level: characters.LevelOwner}})
	if err != nil {
		t.Fatal(err)
	}
	r := w.run(w.master, "ask", "Grão-Mestre", "Não é do login: é de relatórios.", "--from-message", it.ID, "--no-wait", "--json")
	if v := decodeJSON(t, r); r.code != 0 || v.Data["status"] != "sent" {
		t.Fatalf("ask the Grand Master = %+v", r)
	}
	got := w.mach.Delivered(w.get(w.gm))
	if len(got) == 0 || !strings.Contains(got[len(got)-1], text) || !strings.HasPrefix(got[len(got)-1], "Não é do login: é de relatórios.") {
		t.Errorf("the Grand Master got %q; want the reason and then the original text", got)
	}
	if tb, err := w.orc.Tables.Get(w.ctx, w.slug, w.table.ID); err != nil || tb.State != w.table.State {
		t.Errorf("the table = %+v, %v; want it as it was (%s)", tb, err, w.table.State)
	}
}
