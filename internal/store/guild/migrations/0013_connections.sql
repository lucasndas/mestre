-- The connections of the Guild (docs/orquestracao/conexoes.md): who talks to
-- whom and who reads what. A connection has no direction, so the pair is kept
-- in one order only (a is the smaller of the two endpoints, by kind and then
-- id) and there is one row per pair. An endpoint is a 'character' (the id of a
-- row of characters), a 'scroll' or a 'tome' (ids of the scrolls of the table;
-- a tome stands for all its scrolls, the ones that join it later too).
--
-- A connection exists while it has at least one reason (connection_reasons):
-- 'mestre', 'mesa', 'criador', 'manual' or 'missao:<id>'. table_id is the
-- table of both endpoints; it is empty only for the connection between the
-- Grand Master and a table master, the one that crosses tables. Timestamps are
-- Unix milliseconds in UTC.
CREATE TABLE connections (
	id         TEXT    PRIMARY KEY,
	workspace  TEXT    NOT NULL,
	table_id   TEXT    NOT NULL DEFAULT '',
	a_kind     TEXT    NOT NULL CHECK (a_kind IN ('character', 'scroll', 'tome')),
	a_id       TEXT    NOT NULL,
	b_kind     TEXT    NOT NULL CHECK (b_kind IN ('character', 'scroll', 'tome')),
	b_id       TEXT    NOT NULL,
	created_at INTEGER NOT NULL,
	UNIQUE (a_kind, a_id, b_kind, b_id)
);

CREATE INDEX connections_by_a ON connections (a_kind, a_id);
CREATE INDEX connections_by_b ON connections (b_kind, b_id);
CREATE INDEX connections_by_table ON connections (workspace, table_id);

-- Why a connection exists. by is the character or the user that asked for a
-- manual one, empty for the engine's.
CREATE TABLE connection_reasons (
	connection_id TEXT    NOT NULL REFERENCES connections (id),
	reason        TEXT    NOT NULL,
	at            INTEGER NOT NULL,
	by            TEXT    NOT NULL DEFAULT '',
	PRIMARY KEY (connection_id, reason)
);

CREATE INDEX connection_reasons_by_reason ON connection_reasons (reason);
