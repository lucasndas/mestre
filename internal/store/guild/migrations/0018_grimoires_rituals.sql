-- The grimoires (docs/orquestracao/grimorios.md) and the rituals
-- (docs/orquestracao/rituais.md) of the Guild. Timestamps are Unix
-- milliseconds in UTC and 0 means "never". Every table has a declared primary
-- key, as the change log of the replication needs.

-- One row per grimoire: a saved piece of the canvas of a table. The name is
-- unique in the whole Guild (a grimoire is restored in any workspace).
-- workspace and table_id are where it was saved, not a link: the grimoire
-- outlives them. document is the JSON of the recipe (version 1).
CREATE TABLE grimoires (
	name       TEXT    PRIMARY KEY COLLATE NOCASE,
	version    INTEGER NOT NULL,
	author     TEXT    NOT NULL,
	workspace  TEXT    NOT NULL,
	table_id   TEXT    NOT NULL,
	document   TEXT    NOT NULL,
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL
);

-- One row per ritual. table_id is the table of a ritual of a table ('' for a
-- ritual of the workspace, whose owner is the Grand Master). Exactly one of
-- target (the name of a character) and quest (the name of a quest of the
-- table) is set; target_table is the table of the table master a ritual of the
-- workspace aims at ('' for the Grand Master). action is prompt, command or
-- quest; content the prompt or the line; schedule the JSON of the schedule;
-- pre_run the command that fills {{output}} ('' none); timeout_ms how long a
-- firing waits in the queue of a busy target. state is enabled, disabled or
-- finished (a --once that fired). next_run is the next slot (0: none).
CREATE TABLE rituals (
	id           TEXT    PRIMARY KEY,
	workspace    TEXT    NOT NULL REFERENCES workspaces (slug),
	table_id     TEXT    NOT NULL DEFAULT '',
	name         TEXT    NOT NULL COLLATE NOCASE,
	target       TEXT    NOT NULL DEFAULT '',
	target_table TEXT    NOT NULL DEFAULT '',
	quest        TEXT    NOT NULL DEFAULT '',
	action       TEXT    NOT NULL,
	content      TEXT    NOT NULL DEFAULT '',
	schedule     TEXT    NOT NULL,
	pre_run      TEXT    NOT NULL DEFAULT '',
	timeout_ms   INTEGER NOT NULL,
	state        TEXT    NOT NULL,
	next_run     INTEGER NOT NULL DEFAULT 0,
	created_by   TEXT    NOT NULL DEFAULT '',
	created_at   INTEGER NOT NULL,
	updated_at   INTEGER NOT NULL,
	UNIQUE (workspace, table_id, name)
);

CREATE INDEX rituals_by_next_run ON rituals (state, next_run);

-- One row per firing, the history of a ritual. state is queued, pre_running,
-- delivered, expired, failed or skipped. scheduled_for is the slot it is for,
-- created_at when it entered the queue, late is 1 for a --once delivered after
-- the engine was down. reason says why it ended as it did and output keeps the
-- tail of the error output of a pre-run that failed.
CREATE TABLE ritual_firings (
	id            INTEGER PRIMARY KEY AUTOINCREMENT,
	ritual        TEXT    NOT NULL REFERENCES rituals (id),
	scheduled_for INTEGER NOT NULL,
	created_at    INTEGER NOT NULL,
	state         TEXT    NOT NULL,
	reason        TEXT    NOT NULL DEFAULT '',
	late          INTEGER NOT NULL DEFAULT 0,
	output        TEXT    NOT NULL DEFAULT '',
	delivered_at  INTEGER NOT NULL DEFAULT 0,
	updated_at    INTEGER NOT NULL
);

CREATE INDEX ritual_firings_by_ritual ON ritual_firings (ritual, id);
CREATE INDEX ritual_firings_by_state ON ritual_firings (state);
