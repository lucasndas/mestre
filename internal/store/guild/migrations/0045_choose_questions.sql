-- The questions the Grão-Mestre asks a user with buttons (`mestre choose`,
-- docs/cli/grao-mestre.md#mestre-choose). options is the JSON list of the
-- labels, in the order the buttons are shown; state is open or answered, and
-- an answer is taken once. Timestamps are Unix milliseconds in UTC. Every table
-- has a declared primary key, as the change log of the replication needs.
CREATE TABLE choose_questions (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	workspace   TEXT    NOT NULL,
	mesa        TEXT    NOT NULL DEFAULT '',
	asker       TEXT    NOT NULL,
	user_id     TEXT    NOT NULL,
	question    TEXT    NOT NULL,
	options     TEXT    NOT NULL,
	state       TEXT    NOT NULL DEFAULT 'open',
	answer      TEXT    NOT NULL DEFAULT '',
	created_at  INTEGER NOT NULL,
	answered_at INTEGER NOT NULL DEFAULT 0
);
