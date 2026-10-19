-- The Telegram bot (docs/telegram/bot.md): the offset of the long polling, the
-- updates received (recorded before the offset moves, the body cleared once
-- processed), the queue of what the bot sends, the messages it sent (for the
-- threaded reply), the buttons it gave out, the answers it waits for (a PIN, a
-- second factor) and the terms already explained to each user. Timestamps are
-- Unix milliseconds in UTC.
CREATE TABLE telegram_state (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);

CREATE TABLE telegram_updates (
	update_id   INTEGER PRIMARY KEY,
	received_at INTEGER NOT NULL,
	state       TEXT    NOT NULL,
	body        TEXT    NOT NULL
);

CREATE TABLE telegram_outbox (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	chat_id    INTEGER NOT NULL,
	method     TEXT    NOT NULL,
	params     TEXT    NOT NULL,
	workspace  TEXT    NOT NULL DEFAULT '',
	mesa       TEXT    NOT NULL DEFAULT '',
	origin     TEXT    NOT NULL DEFAULT '',
	state      TEXT    NOT NULL,
	not_before INTEGER NOT NULL,
	attempts   INTEGER NOT NULL DEFAULT 0,
	message_id INTEGER NOT NULL DEFAULT 0,
	created_at INTEGER NOT NULL
);
CREATE INDEX telegram_outbox_queued ON telegram_outbox (state, id);

CREATE TABLE bot_messages (
	chat_id    INTEGER NOT NULL,
	message_id INTEGER NOT NULL,
	workspace  TEXT    NOT NULL DEFAULT '',
	mesa       TEXT    NOT NULL DEFAULT '',
	origin     TEXT    NOT NULL DEFAULT '',
	at         INTEGER NOT NULL,
	PRIMARY KEY (chat_id, message_id)
);

CREATE TABLE telegram_callbacks (
	key        TEXT    PRIMARY KEY,
	user_id    TEXT    NOT NULL,
	action     TEXT    NOT NULL,
	arg        TEXT    NOT NULL DEFAULT '',
	capability TEXT    NOT NULL DEFAULT '',
	expires_at INTEGER NOT NULL,
	state      TEXT    NOT NULL,
	decided_by TEXT    NOT NULL DEFAULT ''
);

CREATE TABLE telegram_captures (
	user_id    TEXT    PRIMARY KEY,
	kind       TEXT    NOT NULL,
	ref        TEXT    NOT NULL,
	outbox_id  INTEGER NOT NULL DEFAULT 0,
	expires_at INTEGER NOT NULL
);

CREATE TABLE glossary_seen (
	user_id TEXT NOT NULL,
	term    TEXT NOT NULL,
	PRIMARY KEY (user_id, term)
);
