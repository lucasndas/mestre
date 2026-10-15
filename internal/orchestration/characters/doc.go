// Package characters is the life of the characters and of the simple
// terminals of the Mestre (docs/orquestracao/personagens.md): how they are
// born from sheets and bases, on which machine, within which limits, how they
// move through their states, how they end without losing work and how they
// come back after a restart.
//
// It has two halves, joined by small interfaces so that a Tower, which runs
// processes the Citadel does not, can be the other end of the same code.
//
// Service is the Citadel's half. It owns the registry (the characters table
// of the Guild database, with who created each one, which is where the
// superior of the chain of approval comes from; and character_state_log, the
// history of every transition), checks who may create what and in which
// table, chooses the machine (presets.Choose and the order of preference),
// keeps the limits and the waiting_slot queue, schedules the safe end
// (dismiss, the ephemeral and persistent lifecycles, the pin), and turns the
// reports of the machines into states. It reaches the machines through
// Machines and tells people through Notifier.
//
// Runtime is the half of one machine. From the plan that
// sessionzero.Character.Prepare gives it, it checks the table's files
// (CheckStart), runs Launch.Verify, creates the terminal (internal/terminal,
// behind Host), types the Mestre layer when the provider's channel is the
// first message, and then follows the character: the events of the hook
// engine (OnHook), the screens and timeouts of providers.Supervisor, the 15
// minutes without a signal, and the exit of the process. Every transition is
// kept in the machine database (character_runtime) and reported to the
// Citadel (Reporter); an engine that comes back (Recover) finds the
// terminals again with the states they had. It is also the hook.Characters,
// hook.States and hook.Contexts of the machine.
//
// The pieces that belong to later stages or milestones (the inbox and the
// waits, the scrolls, the tables, the users, the connections, the files of
// a class) are interfaces in ports.go with fakes in the tests.
package characters
