-- Protocol tables (docs/conectividade/protocolo.md#ack-e-idempotência), the
-- same as in the machine database: on the Citadel the outbox is kept per Tower
-- (peer is the machine id) and processed_commands per sender.
-- Timestamps are Unix milliseconds in UTC.
CREATE TABLE outbox (
	seq        INTEGER PRIMARY KEY AUTOINCREMENT,
	peer       TEXT    NOT NULL,
	id         TEXT    NOT NULL,
	type       TEXT    NOT NULL,
	body       BLOB    NOT NULL,
	created_at INTEGER NOT NULL,
	UNIQUE (peer, id)
);

CREATE INDEX outbox_peer ON outbox (peer, seq);

CREATE TABLE processed_commands (
	sender       TEXT    NOT NULL,
	id           TEXT    NOT NULL,
	type         TEXT    NOT NULL,
	ack          BLOB,
	processed_at INTEGER NOT NULL,
	PRIMARY KEY (sender, id)
);

CREATE INDEX processed_commands_at ON processed_commands (processed_at);
