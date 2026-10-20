-- The dependencies between epics (docs/orquestracao/dependencia-entre-mesas.md).
-- Timestamps are Unix milliseconds in UTC. Every table has a declared primary
-- key, as the change log of the replication needs.

-- An epic that cannot go on before another: state is pending (the other epic
-- has not been merged yet), satisfied (its table was merged and the commit is
-- in the base, or the epic was closed as completed) or cancelled (its table
-- was cancelled, or the epic was closed as not planned: the dependent epic
-- stays held until the Grand Master decides) or released (the Grand Master
-- dropped it). wait is the id of the wait of the Grand Master while the
-- dependent epic has a table.
CREATE TABLE epic_dependencies (
	workspace  TEXT    NOT NULL,
	epic       INTEGER NOT NULL,
	depends_on INTEGER NOT NULL,
	state      TEXT    NOT NULL,
	wait       TEXT    NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL,
	ended_at   INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (workspace, epic, depends_on)
);

CREATE INDEX epic_dependencies_by_target ON epic_dependencies (workspace, depends_on, state);
