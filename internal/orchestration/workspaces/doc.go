// Package workspaces owns the workspaces of the Guild: one GitHub repository
// each, with its slug, state and stage of the session zero, and, on the
// Citadel, the mirror (a bare clone of the repository) and the main clone (a
// worktree of the mirror on the main branch)
// (docs/orquestracao/workspaces.md).
//
// The Service creates a workspace, walks it through the stages of the
// session zero (it runs the mirror stage itself and takes the others from
// the pieces that own them), keeps the mirror fresh (a fetch every
// FetchInterval, on demand through Fetch and Poke, with backoff and a
// single operational alert after AlertAfter without success) and advances
// the main clone with a fast-forward only when it is safe. Everything
// outside the workspace is an interface: the repository on GitHub
// (GitHub), its installation token (Credentials), the operational alert
// (Alerts) and the Grand Master (GrandMaster); the clock is injected and
// git is only internal/gitx.
//
// The main clone is a folder the Grand Master works in, so the engine opens
// it only by its record (gitx.WorktreeRecord): the record is derived from
// the layout of the workspace folder, which is the engine's own, and checked
// against what git says when the clone is created, before any agent runs
// there (docs/arquitetura/seguranca.md#git-do-motor).
package workspaces
