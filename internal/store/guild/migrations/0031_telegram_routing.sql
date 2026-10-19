-- The routing of the messages of the Telegram (docs/telegram/roteamento.md): the
-- workspace that is active for each user, and the messages of a user who acts in
-- several workspaces and did not choose one yet (kept until he chooses, then
-- delivered in order). The actions of each message the bot sent are kept with it,
-- so a text that answers a "Sistema" message can be told what that message
-- allows. Timestamps are Unix milliseconds in UTC.
CREATE TABLE telegram_active (
	user_id   TEXT    PRIMARY KEY,
	workspace TEXT    NOT NULL,
	at        INTEGER NOT NULL
);

CREATE TABLE telegram_pending (
	id      INTEGER PRIMARY KEY AUTOINCREMENT,
	user_id TEXT    NOT NULL,
	kind    TEXT    NOT NULL,
	text    TEXT    NOT NULL,
	chat_id INTEGER NOT NULL,
	message_id INTEGER NOT NULL,
	reply_to INTEGER NOT NULL DEFAULT 0,
	at      INTEGER NOT NULL
);

ALTER TABLE bot_messages ADD COLUMN actions TEXT NOT NULL DEFAULT '';
