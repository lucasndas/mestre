-- The inbox, the waits and the scrolls of the Guild
-- (docs/orquestracao/esperas-e-caixa-de-entrada.md and
-- docs/orquestracao/pergaminhos-e-tomos.md). Timestamps are Unix milliseconds
-- in UTC and 0 means "never". Every table has a declared primary key, as the
-- change log of the replication needs.

-- The inbox: one queue per character (target). seq is the number the id is
-- made of ("m81"). kind is user, agent, answer, quest, ritual, github, system,
-- wake or approval. priority is 1 (the Owner), 2 (the other users) or 3 (the
-- rest); rank orders inside a priority (the approval requests go last, 1).
-- state is 'queued', 'delivering' (typed, no turn began yet), 'delivered',
-- 'expired' or 'cancelled'. unconfirmed is 1 for a delivery whose turn never
-- began within the time limit: it is never typed again. origin is the JSON of
-- the origin of a message of a user (channel, user, level, conversation,
-- message, replied-to, table, transcribed), refs the JSON of the references
-- (ask, wait, table, quest, op_id) and attachments the JSON list of paths.
CREATE TABLE inbox_messages (
	seq          INTEGER PRIMARY KEY AUTOINCREMENT,
	workspace    TEXT    NOT NULL,
	target       TEXT    NOT NULL,
	kind         TEXT    NOT NULL,
	sender       TEXT    NOT NULL DEFAULT '',
	sender_id    TEXT    NOT NULL DEFAULT '',
	body         TEXT    NOT NULL,
	attachments  TEXT    NOT NULL DEFAULT '',
	origin       TEXT    NOT NULL DEFAULT '',
	refs         TEXT    NOT NULL DEFAULT '',
	priority     INTEGER NOT NULL,
	rank         INTEGER NOT NULL DEFAULT 0,
	state        TEXT    NOT NULL,
	unconfirmed  INTEGER NOT NULL DEFAULT 0,
	created_at   INTEGER NOT NULL,
	typed_at     INTEGER NOT NULL DEFAULT 0,
	delivered_at INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX inbox_messages_by_target ON inbox_messages (target, state, priority, rank, seq);

-- The answers kept for a channel that is down (the reply of the Grand Master
-- to a message of a user). state is 'pending' or 'sent'; message is the id of
-- the inbox message that is answered.
CREATE TABLE outbound_messages (
	id           INTEGER PRIMARY KEY AUTOINCREMENT,
	workspace    TEXT    NOT NULL,
	message      TEXT    NOT NULL DEFAULT '',
	channel      TEXT    NOT NULL,
	user_id      TEXT    NOT NULL DEFAULT '',
	conversation TEXT    NOT NULL DEFAULT '',
	reply_to     TEXT    NOT NULL DEFAULT '',
	body         TEXT    NOT NULL,
	attachments  TEXT    NOT NULL DEFAULT '',
	state        TEXT    NOT NULL,
	created_at   INTEGER NOT NULL,
	sent_at      INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX outbound_messages_by_state ON outbound_messages (state, channel, id);

-- The waits (docs/orquestracao/esperas-e-caixa-de-entrada.md#esperas). kind is
-- 'character', 'steps', 'approval', 'time' or 'sync' (the wait for a vacancy
-- stays in character_slot_waits); mode is 'all' or 'first'; targets is the
-- JSON list of what is waited for, each with a done flag and its result;
-- reason is what the canvas and `mestre list` show; state is 'active',
-- 'satisfied', 'expired' or 'cancelled'; quest is the quest it belongs to
-- (cancelled with it); until_at is the moment of a wait for a time.
CREATE TABLE waits (
	id         TEXT    PRIMARY KEY,
	workspace  TEXT    NOT NULL,
	table_id   TEXT    NOT NULL DEFAULT '',
	waiter     TEXT    NOT NULL,
	kind       TEXT    NOT NULL,
	mode       TEXT    NOT NULL DEFAULT 'all',
	targets    TEXT    NOT NULL DEFAULT '[]',
	reason     TEXT    NOT NULL DEFAULT '',
	quest      TEXT    NOT NULL DEFAULT '',
	state      TEXT    NOT NULL,
	result     TEXT    NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL,
	timeout_at INTEGER NOT NULL DEFAULT 0,
	until_at   INTEGER NOT NULL DEFAULT 0,
	ended_at   INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX waits_by_state ON waits (state, waiter);

-- The asks that were made (`mestre ask`). state is 'open', 'answered' or
-- 'cancelled'. blocking is 1 while a call of the CLI waits for the answer;
-- a timed-out ask stays open with blocking 0 and timed_out_at set, and its
-- late answer goes to the inbox of the asker as an 'answer' item. item is the
-- id of the inbox message that carries the question.
CREATE TABLE asks (
	id           TEXT    PRIMARY KEY,
	workspace    TEXT    NOT NULL,
	table_id     TEXT    NOT NULL DEFAULT '',
	asker        TEXT    NOT NULL,
	target       TEXT    NOT NULL,
	item         TEXT    NOT NULL DEFAULT '',
	state        TEXT    NOT NULL,
	blocking     INTEGER NOT NULL DEFAULT 0,
	response     TEXT    NOT NULL DEFAULT '',
	created_at   INTEGER NOT NULL,
	timed_out_at INTEGER NOT NULL DEFAULT 0,
	answered_at  INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX asks_by_state ON asks (state, target, asker);

-- The deadlocks that were warned about, one row per cycle (key is the ids of
-- the characters of the cycle, sorted). ended_at is when the cycle went away;
-- a cycle warned about is not warned about again while it lasts.
CREATE TABLE wait_cycles (
	key        TEXT    PRIMARY KEY,
	workspace  TEXT    NOT NULL,
	text       TEXT    NOT NULL,
	created_at INTEGER NOT NULL,
	ended_at   INTEGER NOT NULL DEFAULT 0
);

-- The scrolls of the tables (docs/orquestracao/pergaminhos-e-tomos.md). key is
-- "<workspace>/<table>/<id>", the identity the connections use (the id,
-- "p0001", repeats from table to table: it is the name of the file). name_key
-- is the name without difference of case; fixed_name is 1 when the name came
-- from --name. tome is the key of the tome that holds it ('' for none).
-- revision grows by one at every change; size is in bytes.
CREATE TABLE notes (
	key        TEXT    PRIMARY KEY,
	workspace  TEXT    NOT NULL,
	table_id   TEXT    NOT NULL,
	id         TEXT    NOT NULL,
	name       TEXT    NOT NULL,
	name_key   TEXT    NOT NULL,
	fixed_name INTEGER NOT NULL DEFAULT 0,
	tome       TEXT    NOT NULL DEFAULT '',
	revision   INTEGER NOT NULL,
	size       INTEGER NOT NULL,
	author     TEXT    NOT NULL DEFAULT '',
	author_id  TEXT    NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL,
	UNIQUE (workspace, table_id, id),
	UNIQUE (workspace, table_id, name_key)
);

CREATE INDEX notes_by_tome ON notes (tome);

-- The tomes: groups of scrolls of a table, with a name that is unique in it.
CREATE TABLE tomes (
	key        TEXT    PRIMARY KEY,
	workspace  TEXT    NOT NULL,
	table_id   TEXT    NOT NULL,
	id         TEXT    NOT NULL,
	name       TEXT    NOT NULL,
	name_key   TEXT    NOT NULL,
	created_at INTEGER NOT NULL,
	UNIQUE (workspace, table_id, id),
	UNIQUE (workspace, table_id, name_key)
);

-- The last number given to a scroll and to a tome of each table: an id is
-- never used twice.
CREATE TABLE note_counters (
	workspace  TEXT    NOT NULL,
	table_id   TEXT    NOT NULL,
	scrolls    INTEGER NOT NULL DEFAULT 0,
	tomes      INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (workspace, table_id)
);
