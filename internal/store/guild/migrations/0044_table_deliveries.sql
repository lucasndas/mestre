-- The last useful delivery of a table (docs/orquestracao/supervisao-da-cadeia.md):
-- a new commit on its branch, the pull request opened or updated, or a step of a
-- mission that ended. Timestamps are Unix milliseconds in UTC. Every table has a
-- declared primary key, as the change log of the replication needs.
--
-- tip is the commit of the branch the last time the engine looked, to see a new
-- one; alerted_for is the instant of delivery (or of entering work) the warning
-- of a stalled table was sent for: one warning until the next delivery.
CREATE TABLE table_deliveries (
	workspace    TEXT    NOT NULL,
	table_id     TEXT    NOT NULL,
	delivered_at INTEGER NOT NULL DEFAULT 0,
	tip          TEXT    NOT NULL DEFAULT '',
	alerted_for  INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (workspace, table_id)
);
