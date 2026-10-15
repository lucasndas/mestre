// Package sessionzero is the session zero of a workspace: what happens, once
// per repository, between the creation of the workspace and the first table
// (docs/sessao-zero/workspace.md). It runs the stages of the workspace that
// are not the mirror's, in the order of internal/orchestration/workspaces:
//
//   - apps: the fixed Apps are installed in the repository, or the owner is
//     asked to install them and the stage waits (workspaces.WaitError);
//   - files: the .mestre/ folder (never overwriting what exists), the files
//     the providers need in the main clone (skills and hooks, merged into
//     what is already there) and the exclusions in info/exclude, and then
//     the Grand Master starts and the users are asked whether .mestre/ is
//     versioned;
//   - analysis: the Grand Master analyses the project; without an AGENTS.md
//     it proposes one as an approval of type proposta_agents_md, with an
//     existing one it touches nothing, with a CLAUDE.md it warns;
//   - approval_test: whether the review of the App counts for the required
//     reviews of the repository, recorded for the panel.
//
// What the session zero needs from later milestones is behind small
// interfaces (ports.go): GitHub, the users, the approvals, the checklist and
// the Grand Master. The files the providers need in a worktree are made by
// Worktree, which the session zero of the table reuses.
//
// Table is the session zero of a table (docs/sessao-zero/mesa.md): in each
// worktree it generates the files of the providers (Worktree), copies the
// classes and the attachments, writes the trust of the folder in the account
// profiles of the machine and takes it out when the worktree is removed, holds
// back the characters of a provider whose files were not generated
// (CheckStart), and wraps the update of the branch so that the Mestre's part
// of a hook file the project versions is out of the way (Sync).
//
// The engine writes in the main clone, a folder the agents write too, so
// every file goes through internal/safefile, git only through the record of
// the clone (workspaces.Service.OpenPrincipal), and the .gitignore and the
// AGENTS.md of the project are never touched.
package sessionzero
