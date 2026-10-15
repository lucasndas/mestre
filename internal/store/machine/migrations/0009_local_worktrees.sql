-- The worktrees of the tables on this machine (docs/arquitetura/persistencia.md,
-- docs/orquestracao/mesas.md#worktree-por-máquina-e-sincronização): where
-- each one is, the record the engine took when it created it (the folder, its
-- git folder in the replica of the mirror and the replica: the only way the
-- engine opens it) and the last commit of the branch that the Citadel is known
-- to have, which is the base of the next incremental bundle. Timestamps are
-- Unix milliseconds in UTC.
CREATE TABLE local_worktrees (
	workspace      TEXT    NOT NULL,
	table_id       TEXT    NOT NULL,
	branch         TEXT    NOT NULL,
	dir            TEXT    NOT NULL,
	git_dir        TEXT    NOT NULL,
	common_dir     TEXT    NOT NULL,
	citadel_commit TEXT    NOT NULL DEFAULT '',
	created_at     INTEGER NOT NULL,
	updated_at     INTEGER NOT NULL,
	PRIMARY KEY (workspace, table_id)
);
