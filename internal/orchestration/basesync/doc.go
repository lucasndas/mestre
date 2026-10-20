// Package basesync keeps the branch of a table in step with the main branch of
// the repository (docs/orquestracao/sincronia-com-a-base.md). The engine
// notices that the base moved (the tip of origin/<base> in the mirror is not in
// the branch of the table) when the mirror is fetched and when the Master of the
// table asks `mestre pr open`, and then makes the mission `sync`: one step, for
// the Dev of the table, that integrates the base in the worktree and proves that
// the result verifies. A conflict is a task for the Dev, never a block for the
// users.
package basesync
