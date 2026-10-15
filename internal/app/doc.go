// Package app assembles the engine of one instance (docs/arquitetura/componentes.md):
// what every machine of the Guild runs, and the functions the Citadel turns
// on and off as the epoch says (docs/guilda/cidadela-e-torres.md).
//
// # Every machine
//
// The machine database and the place in the Guild (guild.Member, Epochs),
// the engine port with the mTLS of the transport (the route of the Towers,
// /join, /healthz), the local channel of the CLI and the answer to the
// broadcast of the discovery.
//
// # The roles
//
// The certificate of epoch names the Citadel; the engine runs one period of
// a role at a time and changes when the epoch says so.
//
//   - Citadel: the Guild database as the main one, the vault opened, the CA,
//     the entries (guild.Citadel), the Towers (protocol.Server), the
//     replication (replication.Primary), the machine sheets, the cli.call of
//     the Towers, and the external functions, which the fence (guild.Fence)
//     turns on only with the epoch confirmed by the lock.
//   - Tower: the replica, the connection to the Citadel with its outbox
//     (protocol.Client, found by the discovery.Locator), the machine sheet,
//     the forwarding of the CLI as cli.call, and 4007 to whoever takes it for
//     the Citadel.
//
// A Citadel that sees a higher epoch stops every external action at once,
// closes the Towers with 4007, keeps its database as descartado-<época>.db
// and becomes a Tower, which reports the transactions the change lost. A
// Tower that the Owner promotes stops the replication, reads its last lsn,
// wins the next epoch in the lock, and only then opens the replica as the
// main database, signs the certificate and becomes the Citadel, which waits
// 60 s before acting outside (what is left of them after a restart). The
// acting Citadel presents the certificate with mestre.local and keeps its
// machine certificate aside; both renew in mestre run, as do the Towers'.
//
// # What stays out
//
// The real lock, the bot and the panel arrive in their milestones: here they are interfaces (guild.Lock, the Approvers,
// ExternalFunc). Main is `mestre run`; the hooks of the full suite exist only
// with the tag full (testhooks_full.go).
//
// # The orchestration
//
// Orchestration (orchestration.go) is what milestone 4 can run on the
// Citadel: the workspaces, the tables, the characters, the connections, the
// inbox, the waits, the scrolls and the quests, built and joined (the loops between them
// are broken with lazy references), their commands registered in
// Engine.Commands and their loops run by External while the fence lets the
// Citadel act. What it needs from milestones 5 and 6 (GitHub, the users and
// the channels, the approvals, the builder of the requests of the characters)
// and from the machines (the terminals) comes in through OrchestrationOptions
// as small interfaces; whoever builds the Deps puts them there.
package app
