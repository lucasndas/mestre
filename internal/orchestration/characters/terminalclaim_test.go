package characters_test

import (
	"errors"
	"testing"

	"github.com/mestre-ia/mestre/internal/orchestration/characters"
)

// A machine may report only the terminal the Citadel gave the character: a Tower that
// claims the terminal id of another character is refused, the registry keeps its ids,
// and the terminal still resolves to its own character.
func TestAReportCannotClaimTheTerminalOfAnotherCharacter(t *testing.T) {
	t.Parallel()
	r := newRig(t, rigOptions{})
	r.makeTM("3-login")
	victim := r.make("Dev Claude", "Dev", "3-login")
	thief := r.make("Dev Claude", "Dev2", "3-login")
	vt := r.get(victim.ID).TerminalID
	if vt == "" || vt == r.get(thief.ID).TerminalID {
		t.Fatalf("terminals: %q and %q", vt, r.get(thief.ID).TerminalID)
	}
	err := r.svc.Report(r.ctx, characters.Report{Character: thief.ID, State: characters.Busy, Reason: "turn", Terminal: vt})
	if !errors.Is(err, characters.ErrForeignTerminal) {
		t.Fatalf("the claim of a foreign terminal = %v", err)
	}
	if got := r.get(thief.ID).TerminalID; got == vt {
		t.Fatal("the thief holds the terminal of the victim")
	}
	c, err := r.reg.ByTerminal(r.ctx, vt)
	if err != nil || c.ID != victim.ID {
		t.Fatalf("the terminal of the victim resolves to %q, %v", c.ID, err)
	}
	// its own, in both forms, is fine
	for _, own := range []string{thief.ID, thief.ID + "-2"} {
		if err := r.svc.Report(r.ctx, characters.Report{Character: thief.ID, State: characters.Busy, Reason: "turn", Terminal: own}); err != nil {
			t.Fatalf("the own terminal %q: %v", own, err)
		}
	}
}
