// Package tables is the life of the tables, the mesas of the Mestre
// (docs/orquestracao/mesas.md): a git worktree with its own branch, tied to an
// epic, with a table master.
//
// Service is the Citadel's half. Open makes the branch epico/<n>-<slug> from
// origin/<main> in the mirror, the worktree by its record (gitx.Record, the
// only way the engine opens it afterwards), runs the session zero of the
// table, recruits the table master for the Grand Master and hands it the
// request; if a step fails the ones before it are undone. The states follow
// the diagram of the document (Transition), moved by the events the other
// parts tell (PROpened, MergeConfirmed, MissionStarted, the first turn of the
// table master); Pause and Resume mark the table paused over its state;
// Close ends it, the characters first and the table master last, the
// worktrees only when integrated and clean, and with Cancel (a destructive
// action) abandons it, keeping the branch in the mirror for the retention
// period. A table that cannot end yet stays closing, and Sweep takes it on.
// Service is the characters.Tables, the inbox.Pauses and the
// characters.Observer of the Guild, and its commands (Handlers) are
// `mestre table open|list|status|close`.
//
// The branches travel between the Citadel and the Towers as git bundles over
// the connection of the protocol: Receiver is the Finalizer of what a Tower
// sends (a fast-forward of the worktree of the Citadel, or a notice of
// divergence), SyncTower sends the tip of the Citadel to a Tower, and Tower is
// the half of a machine that is not the Citadel. CheckRefs compares the
// branches of the tables and the refs of the Towers with what the engine
// registered, because a Codex character reaches the refs of the whole mirror:
// what moved without the engine is an alert and is not trusted.
//
// GitHub, the messages of the users, the attachments, the approval of a
// destructive action and the connection to the Towers are milestone 5 and
// come in as small interfaces (ports.go).
package tables
