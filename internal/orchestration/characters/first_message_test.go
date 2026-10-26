package characters_test

import (
	"strings"
	"testing"

	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/orchestration/inbox"
	"github.com/mestre-ia/mestre/internal/providers"
)

// A task queued before the first hook is typed after the Mestre layer, never
// before it: the inbox waits for the first message.
func TestTaskIsTypedAfterTheMestreLayer(t *testing.T) {
	t.Parallel()
	r := newRig(t, rigOptions{stage7: true, first: "THE LAYER"})
	res, err := r.create(characters.CreateRequest{Sheet: "Dev Claude", Role: characters.RoleGrandMaster})
	if err != nil {
		t.Fatal(err)
	}
	gm := res.Character
	r.enqueue(inbox.NewItem{Target: gm.ID, Kind: inbox.KindUser, Sender: "Lucas", Text: "THE TASK", Origin: lucas("")})
	r.hook(gm, providers.EventSessionStart)
	typed := r.machineOf(gm).host.proc(gm.ID).typedAll()
	if len(typed) == 0 || typed[0] != paste("THE LAYER") {
		t.Fatalf("the first thing typed is not the Mestre layer: %q", typed)
	}
	if len(typed) != 1 {
		t.Fatalf("something was typed with the layer: %q", typed)
	}
	r.hook(gm, providers.EventTurnEnd) // the turn of the layer ends: the task goes now
	typed = r.machineOf(gm).host.proc(gm.ID).typedAll()
	if len(typed) != 2 || !strings.Contains(typed[1], "THE TASK") {
		t.Fatalf("the task did not follow the layer: %q", typed)
	}
}
