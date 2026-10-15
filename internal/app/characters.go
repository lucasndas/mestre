package app

import (
	"github.com/lucasndas/mestre/internal/hook"
	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/providers"
)

// CharacterRuntime is what the engine of a machine needs of the characters
// that run on it: the runtime of internal/orchestration/characters.
// It is the registry of terminals of the hook dispatcher (the environment of
// each character), the state a character is put in when it waits for an
// approval, the source of the Mestre layer at the start of a session and of a
// turn, and the sink of the events of the hooks that move the states.
type CharacterRuntime interface {
	hook.Characters
	hook.States
	hook.Contexts
	OnHook(providers.Event, hook.Answer)
}

// UseCharacters wires the characters of the machine into the engine: the hook
// dispatcher asks them for the environment of the caller, the layer of the
// Mestre and the state of the approval waits, and every event it answers
// moves the state of the character. identifier is how the local channel knows
// which terminal a caller runs in (localchan.NewSystemIdentifier over the
// directory the terminal host keeps); nil leaves the one the Deps have.
//
// The Citadel's half (the registry, the limits, the machine choice) and the
// terminals of the machine (the launcher that starts the mestre-pty as the
// account of the agents) are assembled by whoever builds the Deps: they need
// the tables, the users and the installation, which later stages bring.
func (d *Deps) UseCharacters(rt CharacterRuntime, identifier localchan.Identifier) {
	d.Characters, d.States, d.Contexts = rt, rt, rt
	prev := d.OnHookEvent
	d.OnHookEvent = func(ev providers.Event, ans hook.Answer) {
		rt.OnHook(ev, ans)
		if prev != nil {
			prev(ev, ans)
		}
	}
	if identifier != nil {
		d.Identifier = identifier
	}
}
