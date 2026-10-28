package main

import (
	"context"
	"testing"

	"github.com/mestre-ia/mestre/internal/panel"
)

// The Dev-1 of each table of loja is in the state of its table, on the canvas and in its terminal.
func TestTheDevOfEachLojaTableIsInItsState(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	terms := &fakeTerminals{}
	canvas := newFakeCanvas()
	for table, sc := range scenarios {
		id := devOf(lojaWorkspace, table)
		info, err := terms.ByCharacter(ctx, id)
		if err != nil {
			t.Fatalf("%s: %v", table, err)
		}
		if info.State != sc.state || info.Busy != sc.busy || info.Signal == sc.noSignal || info.Table != table {
			t.Errorf("%s: terminal says %+v, want %+v", table, info, sc)
		}
		byTerminal, err := terms.Info(ctx, info.TerminalID)
		if err != nil || byTerminal.Character != id {
			t.Errorf("%s: by terminal id: %+v, %v", table, byTerminal, err)
		}
		st, err := canvas.State(ctx, lojaWorkspace, table)
		if err != nil {
			t.Fatalf("%s: %v", table, err)
		}
		var found *panel.CanvasCharacter
		for i := range st.Characters {
			if st.Characters[i].ID == id {
				found = &st.Characters[i]
			}
		}
		if found == nil || found.State != sc.state || found.Reason != sc.reason || !found.HasTerminal {
			t.Errorf("%s: canvas says %+v, want state %q reason %q", table, found, sc.state, sc.reason)
		}
	}
	if info, _ := terms.ByCharacter(ctx, fakeCharacter); info.State != "idle" {
		t.Errorf("the Dev-1 of the smoke test changed: %+v", info)
	}
}
