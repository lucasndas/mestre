//go:build full

package app_test

import (
	"strings"
	"testing"

	"github.com/mestre-ia/mestre/internal/orchestration/characters"
)

// CA-GM-04: the Citadel restarted with two tables open; when the engine is
// back the Grand Master is the first character started and its message lists
// the two tables (and the rest of what the database knows of the workspace).
func TestTheGrandMasterIsTheFirstCharacterResumedAndGetsTheTablesInItsMessage(t *testing.T) {
	t.Parallel()
	w := newCommWorld(t)
	second, _ := w.openTable(43)
	for _, c := range []characters.Character{w.gm, w.master} {
		w.mach.Report(w.ctx, c.ID, characters.Idle)
	}
	alive, err := w.orc.Registry.Alive(w.ctx)
	if err != nil || len(alive) < 3 {
		t.Fatalf("alive = %v, %v", alive, err)
	}
	for _, c := range alive {
		if _, _, err := w.orc.Registry.Transition(w.ctx, c.ID, characters.Interrupted, characters.Change{Reason: "restart", At: w.clk.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	before := len(w.mach.Started())

	if err := w.orc.Characters.ResumeMachine(w.ctx, "cidadela"); err != nil {
		t.Fatal(err)
	}
	started := w.mach.Started()[before:]
	if len(started) < 3 || started[0].Character != w.gm.ID {
		t.Fatalf("started = %+v; want the Grand Master first", started)
	}
	msg := started[0].ResumeMessage
	for _, want := range []string{w.table.ID, second.ID, "Open tables", "Items waiting in your inbox"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the resumption message lacks %q:\n%s", want, msg)
		}
	}
	if strings.Contains(started[1].ResumeMessage, "State of the workspace") {
		t.Errorf("a table master got the state of the workspace:\n%s", started[1].ResumeMessage)
	}
}
