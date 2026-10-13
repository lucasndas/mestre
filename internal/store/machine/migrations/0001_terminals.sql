-- Terminal registry (docs/arquitetura/persistencia.md#registro-de-terminais).
-- A terminal is registered here before its process starts. Timestamps are
-- Unix milliseconds in UTC. status values are owned by the character states
-- (docs/orquestracao/personagens.md), so there is no CHECK on them.
CREATE TABLE terminals (
	id                  TEXT    PRIMARY KEY,
	character_id        TEXT    NOT NULL,
	machine_id          TEXT    NOT NULL,
	pid                 INTEGER,
	dir                 TEXT    NOT NULL,
	class               TEXT,
	status              TEXT    NOT NULL,
	provider_session_id TEXT,
	started_at          INTEGER NOT NULL,
	last_seen_at        INTEGER NOT NULL
);

CREATE INDEX terminals_status ON terminals (status);
