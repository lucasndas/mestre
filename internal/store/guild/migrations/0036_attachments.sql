-- The attachments of the users (docs/telegram/anexos-e-transcricao.md): one row
-- per file received, with where it came from, the type declared and the type
-- detected from the content, the size and the SHA-256, the caption, the path
-- relative to the main clone (.mestre/anexos/AAAA/MM/id-name), the transcript of
-- a voice message and the tables it was copied to. Timestamps are Unix
-- milliseconds in UTC.
CREATE TABLE attachments (
	id             TEXT    PRIMARY KEY,
	workspace      TEXT    NOT NULL REFERENCES workspaces (slug),
	user_id        TEXT    NOT NULL,
	channel        TEXT    NOT NULL,
	chat_id        INTEGER NOT NULL DEFAULT 0,
	message_id     INTEGER NOT NULL DEFAULT 0,
	panel_session  TEXT    NOT NULL DEFAULT '',
	name_original  TEXT    NOT NULL,
	mime_declared  TEXT    NOT NULL DEFAULT '',
	mime_detected  TEXT    NOT NULL,
	mime_mismatch  INTEGER NOT NULL DEFAULT 0,
	size           INTEGER NOT NULL,
	sha256         TEXT    NOT NULL,
	caption        TEXT    NOT NULL DEFAULT '',
	path           TEXT    NOT NULL,
	transcript     TEXT    NOT NULL DEFAULT '',
	transcript_lang TEXT   NOT NULL DEFAULT '',
	transcript_error TEXT  NOT NULL DEFAULT '',
	tables         TEXT    NOT NULL DEFAULT '[]',
	created_at     INTEGER NOT NULL
);
CREATE INDEX attachments_by_workspace ON attachments (workspace, created_at);

-- The attachment a message of the Telegram carries, kept with the message that
-- waits (the Mestre locked, or the user who did not choose a workspace yet).
ALTER TABLE telegram_held ADD COLUMN file TEXT NOT NULL DEFAULT '';
ALTER TABLE telegram_pending ADD COLUMN file TEXT NOT NULL DEFAULT '';
