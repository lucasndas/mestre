package app

import (
	"errors"
	"testing"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/providers"
	"github.com/lucasndas/mestre/internal/sessionzero"
)

// A Tower has no library of classes to run the hooks of a class, so a character whose class
// has hooks does not start there (fail closed), before anything is sent to the Tower.
func TestACharacterWhoseClassHasHooksDoesNotStartOnATower(t *testing.T) {
	t.Parallel()
	m := &machineRouter{local: "citadel"}
	class := &classes.Class{Name: "dev", Hooks: classes.Hooks{providers.EventPostTool: {{Name: "format"}}}}
	cmd := characters.StartCommand{Character: "c1", Terminal: "t1", Request: sessionzero.CharacterRequest{Class: class}}
	if _, err := m.Start(t.Context(), "torre-1", cmd); !errors.Is(err, errClassHooksOnTower) {
		t.Fatalf("the start on a Tower = %v, want the refusal for the hooks of the class", err)
	}
}
