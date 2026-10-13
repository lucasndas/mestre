-- Protocol tables (docs/conectividade/protocolo.md#ack-e-idempotência).
-- outbox is the queue of the messages that demand an ack and have not got it
-- yet, per peer ("citadel" on a Tower, the machine id on the Citadel); the
-- row keeps the envelope as it was first sent, and a connection resends the
-- rows in seq order with the same id. processed_commands is the memory of the
-- receiver: the ids already taken from each sender, with the ack that was
-- given (NULL while the handler has not finished), pruned after 24 hours.
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
