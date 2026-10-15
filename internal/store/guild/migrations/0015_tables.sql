-- The tables (mesas) of the Guild (docs/orquestracao/mesas.md): one row per
-- table, a git worktree with its own branch tied to an epic. id is
-- "<epic>-<slug>" (MESTRE_MESA), the branch without its prefix. state is
-- open, working, in_review, closing, closed, cancelling or cancelled. paused
-- is the mark of the pause over the state (1 is paused) with who asked and
-- why. pr_number and pr_head are the pull request of the table and its head
-- commit as the engine last saw it; merged is 1 once the merge was confirmed
-- and merged_head the commit that was merged. keep_worktrees is 1 when the
-- Grand Master decided to keep the worktrees that did not fulfil the rules of
-- the end. cancel_remote_done is 1 once the cancellation closed the pull
-- request and the epic on GitHub (it happens once). cancel_branch_until is when the branch of a cancelled table leaves
-- the mirror (0: nothing to delete) and branch_deleted says it did. A branch
-- name is unique per workspace, and so is the epic of a table that is not
-- over. Timestamps are Unix milliseconds in UTC and 0 means "never".
CREATE TABLE tables (
	workspace           TEXT    NOT NULL REFERENCES workspaces (slug),
	id                  TEXT    NOT NULL,
	epic                INTEGER NOT NULL,
	epic_title          TEXT    NOT NULL,
	branch              TEXT    NOT NULL,
	base_branch         TEXT    NOT NULL,
	base_commit         TEXT    NOT NULL,
	state               TEXT    NOT NULL,
	paused              INTEGER NOT NULL DEFAULT 0,
	paused_reason       TEXT    NOT NULL DEFAULT '',
	paused_by           TEXT    NOT NULL DEFAULT '',
	paused_at           INTEGER NOT NULL DEFAULT 0,
	pr_number           INTEGER NOT NULL DEFAULT 0,
	pr_head             TEXT    NOT NULL DEFAULT '',
	merged              INTEGER NOT NULL DEFAULT 0,
	merged_head         TEXT    NOT NULL DEFAULT '',
	merged_at           INTEGER NOT NULL DEFAULT 0,
	keep_worktrees      INTEGER NOT NULL DEFAULT 0,
	cancel_reason       TEXT    NOT NULL DEFAULT '',
	cancel_remote_done  INTEGER NOT NULL DEFAULT 0,
	cancel_branch_until INTEGER NOT NULL DEFAULT 0,
	branch_deleted      INTEGER NOT NULL DEFAULT 0,
	opened_by           TEXT    NOT NULL DEFAULT '',
	created_at          INTEGER NOT NULL,
	updated_at          INTEGER NOT NULL,
	closed_at           INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (workspace, id)
);

CREATE UNIQUE INDEX tables_live_epic ON tables (workspace, epic) WHERE state NOT IN ('closed', 'cancelled');
CREATE INDEX tables_by_state ON tables (state);

-- The worktree of a table on each machine, as the engine recorded it when it
-- created it (gitx.WorktreeRecord: the folder, its git folder in the mirror
-- and the mirror), and the last commit of the branch the engine itself
-- registered there: the reference the check of the refs compares with
-- (docs/orquestracao/mesas.md#worktree-por-máquina-e-sincronização). For a
-- Tower last_commit is the last commit of the branch the Tower is known to
-- have (the base of the next incremental bundle) and ref_commit what the
-- engine registered for refs/mestre/torres/<tower>/<branch> on the Citadel,
-- the other reference of that check. On the Citadel the mirror is the main
-- one; on a Tower it is the replica.
CREATE TABLE table_worktrees (
	workspace   TEXT    NOT NULL,
	table_id    TEXT    NOT NULL,
	machine_id  TEXT    NOT NULL,
	dir         TEXT    NOT NULL,
	git_dir     TEXT    NOT NULL DEFAULT '',
	common_dir  TEXT    NOT NULL DEFAULT '',
	last_commit TEXT    NOT NULL,
	ref_commit  TEXT    NOT NULL DEFAULT '',
	created_at  INTEGER NOT NULL,
	updated_at  INTEGER NOT NULL,
	PRIMARY KEY (workspace, table_id, machine_id)
);

-- The bundles that crossed between the Citadel and a Tower, by the SHA-256 of
-- the bundle: a transfer repeated after a reconnection is recognised and does
-- not happen twice. direction is 'to_tower' or 'to_citadel'; outcome says
-- what the application did (applied, diverged, rejected).
CREATE TABLE bundle_transfers (
	sha256     TEXT    NOT NULL,
	workspace  TEXT    NOT NULL,
	table_id   TEXT    NOT NULL,
	machine_id TEXT    NOT NULL,
	direction  TEXT    NOT NULL,
	tip        TEXT    NOT NULL,
	outcome    TEXT    NOT NULL,
	at         INTEGER NOT NULL,
	PRIMARY KEY (sha256, machine_id, direction)
);
