// Package replication keeps the state of the Guild on every machine
// (docs/guilda/replicacao.md): the database, the files of the Citadel, the
// mirrors of the workspaces and the encrypted copy of the vault.
//
// # The two sides
//
//   - Primary runs on the Citadel. For each Tower that connects it decides, from
//     the hello (replica_lsn and replica_epoch), whether to continue the stream
//     of change records from there or to start with a full copy (VACUUM INTO,
//     sent in parts as kind db-snapshot); then it ships every row of
//     replication_log, in order, in binary frames 0x03, right after each commit.
//     Before a record that cites a file it makes sure the Tower has the file
//     (kind file); the vault key and envelope go in vault.replica, and vault.drop
//     takes them away from an excluded machine. It keeps the lag of each Tower
//     (the checklist of milestone 7 reads LagAlerts) and applies the retention of
//     the log.
//   - Replica runs on a Tower. It queues the frames, applies them to the replica
//     database in order and in the same transaction that stores the record in its
//     own replication_log (so max(lsn) is what it has applied), confirms with
//     replica.ack every second or 256 records, and asks again from the last lsn
//     when there is a hole or for a full copy when a record does not fit.
//
// # What lives elsewhere
//
// The change log itself, the hooks that fill it and the guard that keeps a
// replica read-only are in internal/store: this package only reads the log and
// calls Apply, Replace and Purge. Opening the vault, and the format of its key
// and envelope, are internal/vault's; the exclusion hook that sets vault.drop in
// motion is guild.Hooks.ExclusionChanged. Time comes only from pkg/clock.
package replication
