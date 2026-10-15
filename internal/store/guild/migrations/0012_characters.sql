-- The characters and simple terminals of the Guild
-- (docs/orquestracao/personagens.md#registro-de-terminais): one row per
-- character or terminal, with who created it (the superior of the chain of
-- approval comes from there), its state and the id of the session of the
-- provider. The mestre-pty of the terminal (its PID with the identity of the
-- start of the process, and its version) is copied here without the secret;
-- the socket and the secret live in the machine database. Timestamps are Unix milliseconds in UTC and 0 means
-- "never".
--
-- kind is 'character' (an agent) or 'terminal' (a simple terminal); role is
-- 'grand_master', 'table_master' or 'character'. created_by_kind says what
-- created_by is: 'engine' (the engine itself: the Grand Master, the table
-- master of a `table open`, a step of a quest), 'character' (the id of the
-- character that recruited it) or 'user' (the id of the user, on the canvas).
-- state is one of the states of the document; reason is the reason of
-- waiting and blocked. task_open is 1 from the task or step delivered to it
-- until the done that answers it. stop_requested_at is the safe stop that
-- waits for the next safe point (0: none).
CREATE TABLE characters (
	id                   TEXT    PRIMARY KEY,
	name                 TEXT    NOT NULL,
	name_key             TEXT    NOT NULL,
	kind                 TEXT    NOT NULL CHECK (kind IN ('character', 'terminal')),
	role                 TEXT    NOT NULL CHECK (role IN ('grand_master', 'table_master', 'character')),
	workspace            TEXT    NOT NULL REFERENCES workspaces (slug),
	table_id             TEXT    NOT NULL DEFAULT '',
	machine_id           TEXT    NOT NULL,
	sheet                TEXT    NOT NULL,
	base                 TEXT    NOT NULL,
	class                TEXT    NOT NULL DEFAULT '',
	account              TEXT    NOT NULL DEFAULT '',
	terminal_id          TEXT    NOT NULL DEFAULT '',
	pty_pid              INTEGER NOT NULL DEFAULT 0,
	pty_started          TEXT    NOT NULL DEFAULT '',
	pty_version          TEXT    NOT NULL DEFAULT '',
	dir                  TEXT    NOT NULL DEFAULT '',
	state                TEXT    NOT NULL,
	reason               TEXT    NOT NULL DEFAULT '',
	reduced_monitoring   TEXT    NOT NULL DEFAULT '',
	pinned               INTEGER NOT NULL DEFAULT 0,
	lifecycle            TEXT    NOT NULL CHECK (lifecycle IN ('ephemeral', 'persistent')),
	max_idle_ms          INTEGER NOT NULL DEFAULT 0,
	created_by           TEXT    NOT NULL DEFAULT '',
	created_by_kind      TEXT    NOT NULL CHECK (created_by_kind IN ('engine', 'character', 'user')),
	quest                TEXT    NOT NULL DEFAULT '',
	provider_session_id  TEXT    NOT NULL DEFAULT '',
	task_open            INTEGER NOT NULL DEFAULT 0,
	idle_since           INTEGER NOT NULL DEFAULT 0,
	stop_requested_at    INTEGER NOT NULL DEFAULT 0,
	stop_requested_by    TEXT    NOT NULL DEFAULT '',
	stop_reason          TEXT    NOT NULL DEFAULT '',
	created_at           INTEGER NOT NULL,
	started_at           INTEGER NOT NULL DEFAULT 0,
	last_signal_at       INTEGER NOT NULL DEFAULT 0,
	stopped_at           INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX characters_by_workspace ON characters (workspace, state);
CREATE INDEX characters_by_table ON characters (table_id, state);
CREATE INDEX characters_by_machine ON characters (machine_id, state);

-- The texts of the done each character delivered: the summary of its end
-- (docs/orquestracao/personagens.md#encerramento-seguro).
CREATE TABLE character_dones (
	id           INTEGER PRIMARY KEY AUTOINCREMENT,
	character_id TEXT    NOT NULL REFERENCES characters (id),
	at           INTEGER NOT NULL,
	text         TEXT    NOT NULL
);

CREATE INDEX character_dones_by_character ON character_dones (character_id, at);

-- Every transition of state, with the state before, the state after, the
-- reason (the one of waiting or blocked, or the event that caused the change)
-- and the time by the injectable clock. The daily cleaning deletes the rows
-- older than 90 days.
CREATE TABLE character_state_log (
	id           INTEGER PRIMARY KEY AUTOINCREMENT,
	character_id TEXT    NOT NULL,
	from_state   TEXT    NOT NULL,
	to_state     TEXT    NOT NULL,
	reason       TEXT    NOT NULL DEFAULT '',
	at           INTEGER NOT NULL
);

CREATE INDEX character_state_log_by_character ON character_state_log (character_id, id);
CREATE INDEX character_state_log_by_time ON character_state_log (at);

-- The requests that wait for a vacancy (waiting_slot): a step of a quest, a
-- direct recruit or spawn. request is the JSON of the request; state is
-- 'active', 'satisfied', 'expired' or 'cancelled'; limit_name and holders say
-- which limit held it and who occupies it, for the notice when it times out.
CREATE TABLE character_slot_waits (
	id         TEXT    PRIMARY KEY,
	request    TEXT    NOT NULL,
	state      TEXT    NOT NULL,
	limit_name TEXT    NOT NULL DEFAULT '',
	holders    TEXT    NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL,
	expires_at INTEGER NOT NULL,
	ended_at   INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX character_slot_waits_by_state ON character_slot_waits (state, created_at);
