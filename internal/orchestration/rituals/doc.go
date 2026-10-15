// Package rituals runs the rituals (docs/orquestracao/rituais.md): routines
// that fire, by the clock, a prompt or a command at a character that exists, or
// a quest of the table. The engine keeps them in the database, schedules them
// and hands them over through the inbox of the characters; it never types in
// the middle of someone's work.
//
// A Ritual belongs to a table (its owner is the Master of the table) or to the
// workspace (its owner is the Grand Master). Service.Tick is the whole life of
// a firing:
//
//   - a ritual whose slot came makes a Firing in the queue of its target. A
//     busy target does not stop the firing: it waits. A firing of a ritual whose
//     previous firing is still in the queue is recorded as skipped. After the
//     engine was down only the latest missed slot of a recurring ritual may
//     fire, and only if it is less than half an interval late (at most 1 h);
//     the others are skipped and the owner is told;
//   - when the target is free (and the table is not paused) the pre-run, if
//     there is one, runs on the machine and in the folder of the target, after
//     the same decision of permissions as any command of the target; its
//     output replaces {{output}} in the content, which then goes to the inbox
//     of the agent (an item `ritual`) or, for a simple terminal, is typed with
//     Enter. A quest is started (a quest that is under way counts as a busy
//     target);
//   - a firing that is not delivered before the timeout is cancelled and given
//     back to the owner as an item `ritual` with the reason; one that fails (no
//     target, pre-run denied, in error or past 60 s) is given back too.
//
// The pre-run, the decision of permissions and the typing in a terminal are
// small interfaces (PreRunner, Permissions, Terminals): the machines and the
// protocol to the Towers are milestone 5. Loop runs Tick until the context ends.
package rituals
