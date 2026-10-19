-- The messages of the users to the Grand Master that came while the Mestre was
-- locked (/travar): they are kept, and delivered in order when it is unlocked
-- (docs/telegram/seguranca-do-canal.md). Timestamps are Unix milliseconds in UTC.
CREATE TABLE telegram_held (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	user_id    TEXT    NOT NULL,
	kind       TEXT    NOT NULL,
	text       TEXT    NOT NULL,
	chat_id    INTEGER NOT NULL,
	message_id INTEGER NOT NULL,
	reply_to   INTEGER NOT NULL DEFAULT 0,
	reply_ws   TEXT    NOT NULL DEFAULT '',
	reply_mesa TEXT    NOT NULL DEFAULT '',
	has_ws     INTEGER NOT NULL DEFAULT 0,
	at         INTEGER NOT NULL
);
