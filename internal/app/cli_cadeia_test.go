package app_test

import (
	"strings"
	"testing"

	"github.com/mestre-ia/mestre/internal/orchestration/characters"
)

// CA-CADEIA-13: `mestre list` shows what a character waits for: the level
// that has the approval, and the native prompt as another thing.
func TestListShowsTheLevelThatHasTheApprovalOfAWaitingCharacter(t *testing.T) {
	t.Parallel()
	w := newCommWorld(t)
	dev, rev := w.pair()
	set := func(c characters.Character, st characters.State, reason string) {
		t.Helper()
		if _, _, err := w.orc.Registry.Transition(w.ctx, c.ID, st, characters.Change{Reason: reason, At: w.clk.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	statusOf := func(name string) string {
		t.Helper()
		for _, a := range decodeJSON(t, w.run(w.master, "list", "--json")).Data["agents"].([]any) {
			if m := a.(map[string]any); m["name"] == name {
				return m["status"].(string)
			}
		}
		return ""
	}
	set(dev, characters.Busy, "")
	set(dev, characters.Waiting, "approval #3 · do Mestre")
	if got := statusOf("Dev"); got != "aguardando aprovação do Mestre" {
		t.Errorf("status = %q", got)
	}
	set(dev, characters.Busy, "")
	set(dev, characters.Waiting, "approval #3 · do Grão-Mestre")
	if got := statusOf("Dev"); got != "aguardando aprovação do Grão-Mestre" {
		t.Errorf("after climbing: %q", got)
	}
	set(rev, characters.Blocked, characters.ReasonPermission)
	if got := statusOf("Revisor"); got != "aguardando permissão" {
		t.Errorf("native prompt: %q", got)
	}
	if out := w.run(w.master, "list"); !strings.Contains(out.stdout, "aguardando aprovação do Grão-Mestre") {
		t.Errorf("text: %s", out.stdout)
	}
}
