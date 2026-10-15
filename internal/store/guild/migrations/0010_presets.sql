-- The presets of the Guild (docs/orquestracao/bases-e-fichas.md#onde-ficam-e-quem-edita):
-- the bases (how a terminal starts) and the character sheets (who the
-- character is), for every workspace. doc is the JSON of the base or of the
-- sheet as saved, already checked; the columns beside it are what is looked
-- up (sheets by base, to refuse deleting a base in use). builtin marks the
-- base the Mestre creates and nobody deletes (the Operator of the PC).
-- Timestamps are Unix milliseconds in UTC.
CREATE TABLE bases (
	name       TEXT    PRIMARY KEY,
	doc        BLOB    NOT NULL,
	builtin    INTEGER NOT NULL DEFAULT 0,
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL
);

CREATE TABLE sheets (
	name       TEXT    PRIMARY KEY,
	base       TEXT    NOT NULL,
	doc        BLOB    NOT NULL,
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL
);

CREATE INDEX sheets_by_base ON sheets (base);
