package quests_test

import (
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/hook"
	"github.com/lucasndas/mestre/internal/orchestration/scrolls"
)

// Part of CA-CLASSES-04 (it closes with the fake agent in a quest, in the
// full suite of the hooks of the engine): the output of an asynchronous hook
// of the class with `output: scroll` is added to the scroll of the quest of the
// character, in a block of code, cut when it is long.
func TestTheOutputOfAnAsynchronousHookGoesToTheScrollOfTheQuest(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	q := w.run(w.create(oneStep))
	dev := w.char("Dev Claude 1")
	long := strings.Repeat("linha de teste\n", 1000)
	for _, r := range []hook.AsyncResult{
		{AgentID: dev.ID, TableID: w.table.ID, Hook: "testes", Output: "ok  3 testes\n```\nfechou o bloco?"},
		{AgentID: dev.ID, TableID: w.table.ID, Hook: "lint", Exit: 2, Output: long},
	} {
		if err := w.orc.Quests.Scroll(w.ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	text := w.scroll(q)
	for _, want := range []string{"## Hook testes — Dev Claude 1 —", "ok  3 testes", "'''", "## Hook lint — Dev Claude 1 —", "código 2", "… (cortado)"} {
		if !strings.Contains(text, want) {
			t.Errorf("the scroll lacks %q:\n%s", want, text)
		}
	}
	if strings.Count(text, "```") != 4 {
		t.Errorf("an output closed or opened a block of its own: %d fences", strings.Count(text, "```"))
	}
	// A character with no quest writes in the history of its table.
	free := w.recruit("Revisor Codex", "Revisor Codex 9")
	if err := w.orc.Quests.Scroll(w.ctx, hook.AsyncResult{AgentID: free.ID, TableID: w.table.ID, Hook: "x", Output: "história"}); err != nil {
		t.Fatal(err)
	}
	_, hist, err := w.orc.Scrolls.Read(w.ctx, scrolls.ReadRequest{Workspace: w.slug, Table: w.table.ID, Actor: scrolls.Engine, Name: scrolls.HistoryName})
	if err != nil || !strings.Contains(hist, "história") {
		t.Errorf("the history of the table: %q, %v", hist, err)
	}
}
