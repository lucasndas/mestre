-- The refs of a table that moved without the engine and nobody trusted again
-- (docs/orquestracao/mesas.md#worktree-por-máquina-e-sincronização): one row
-- per table and ref, with the commit the ref had when the engine alerted. A
-- table with any row here is not trusted: nothing of it is sent to a Tower or
-- to GitHub and no bundle is applied to it. Only trusting the ref again (an
-- Administrator) deletes the rows, so a restart or a promotion of the Citadel
-- does not forget the alert. Timestamps are Unix milliseconds in UTC.
CREATE TABLE table_ref_alerts (
	workspace TEXT    NOT NULL,
	table_id  TEXT    NOT NULL,
	ref       TEXT    NOT NULL,
	commit_id TEXT    NOT NULL,
	at        INTEGER NOT NULL,
	PRIMARY KEY (workspace, table_id, ref)
);
