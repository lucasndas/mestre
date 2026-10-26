-- The conversation of a user with the Grão-Mestre in the panel
-- (docs/painel/conversa-e-aprovacoes.md#conversa-com-o-grão-mestre). The messages of
-- the user and the answers of the Grão-Mestre are the ones of the inbox
-- (inbox_messages, outbound_messages); what the panel adds is below. Timestamps are
-- Unix milliseconds in UTC. Every table has a declared primary key, as the change log
-- of the replication needs.

-- The question of `mestre choose` goes to the channel of the message that caused it:
-- telegram (the buttons of the bot) or panel (the buttons of the conversation). A
-- question is answered only by the channel it was asked in.
ALTER TABLE choose_questions ADD COLUMN channel TEXT NOT NULL DEFAULT 'telegram';
CREATE INDEX choose_questions_by_user ON choose_questions (user_id, workspace, id);

-- Up to which answer of the Grão-Mestre (outbound_messages.id) each user has read
-- the conversation of a workspace; the unread counter is what comes after it.
CREATE TABLE panel_chat_reads (
	user_id     TEXT    NOT NULL,
	workspace   TEXT    NOT NULL,
	outbound_id INTEGER NOT NULL,
	read_at     INTEGER NOT NULL,
	PRIMARY KEY (user_id, workspace)
);

-- The message of progress of each workspace, the last one, masked: the card at the top
-- of the conversation, updated in place (one row per workspace, never a second card).
CREATE TABLE panel_chat_progress (
	workspace  TEXT    PRIMARY KEY,
	text       TEXT    NOT NULL,
	updated_at INTEGER NOT NULL
);

CREATE INDEX outbound_messages_by_user ON outbound_messages (workspace, user_id, id);
