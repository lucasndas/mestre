-- The supervision of the approval chain
-- (docs/orquestracao/supervisao-da-cadeia.md). Timestamps are Unix
-- milliseconds in UTC and 0 means "never". Every table has a declared primary
-- key, as the change log of the replication needs.

-- The tables whose autonomy is paused: the Mestre da mesa does not decide
-- their medio. A row lives until the pause is resumed or the table ends. by_kind
-- is character (the Grão-Mestre) or person (an Approver or above).
CREATE TABLE supervision_pauses (
	workspace TEXT    NOT NULL,
	table_id  TEXT    NOT NULL,
	by        TEXT    NOT NULL,
	by_kind   TEXT    NOT NULL,
	reason    TEXT    NOT NULL,
	since     INTEGER NOT NULL,
	PRIMARY KEY (workspace, table_id)
);

-- Every pause and every resume, with who and why.
CREATE TABLE supervision_audit (
	id        INTEGER PRIMARY KEY AUTOINCREMENT,
	workspace TEXT    NOT NULL,
	table_id  TEXT    NOT NULL,
	at        INTEGER NOT NULL,
	action    TEXT    NOT NULL,
	by        TEXT    NOT NULL,
	by_kind   TEXT    NOT NULL,
	reason    TEXT    NOT NULL
);

-- What the agents of the chain decided, as the summary to the Grão-Mestre and
-- the sample of the Owner's digest read it. level is mesa or grand_master;
-- about is the request as the engine put it. delivered says the summary to the
-- Grão-Mestre already carried it.
CREATE TABLE supervision_decisions (
	id        INTEGER PRIMARY KEY AUTOINCREMENT,
	workspace TEXT    NOT NULL,
	table_id  TEXT    NOT NULL,
	request   INTEGER NOT NULL,
	at        INTEGER NOT NULL,
	approver  TEXT    NOT NULL,
	level     TEXT    NOT NULL,
	answer    TEXT    NOT NULL,
	risk      TEXT    NOT NULL,
	reason    TEXT    NOT NULL,
	about     TEXT    NOT NULL,
	delivered INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX supervision_decisions_pending ON supervision_decisions (workspace, delivered, level);

-- When the summary of a workspace last went out.
CREATE TABLE supervision_digests (
	workspace TEXT PRIMARY KEY,
	last_at   INTEGER NOT NULL
);

-- The queues that have an alert out and have not gone back below the limits:
-- one alert at a time per queue. table_id is '' for the queues that are not a
-- table's.
CREATE TABLE supervision_alerts (
	workspace  TEXT NOT NULL,
	level_kind TEXT NOT NULL,
	table_id   TEXT NOT NULL,
	since      INTEGER NOT NULL,
	PRIMARY KEY (workspace, level_kind, table_id)
);
