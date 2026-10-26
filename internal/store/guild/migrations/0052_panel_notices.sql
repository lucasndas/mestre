-- The notices of the panel (docs/painel/conversa-e-aprovacoes.md#avisos): what a
-- user would get on the Telegram from the engine, kept on the Citadel for the bell
-- of the panel. audience says who sees it: workspace (the users who act in the
-- workspace and may be notified), admins (the Owner and the Administrators who act
-- in the workspace, or any when it is empty), owner or user (user_id). link_kind and
-- link_ref say where the notice leads (request, table, blocking, machine or none).
-- Timestamps are Unix milliseconds in UTC. Every table has a declared primary key,
-- as the change log of the replication needs.
CREATE TABLE panel_notices (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	kind       TEXT    NOT NULL,
	workspace  TEXT    NOT NULL DEFAULT '',
	mesa       TEXT    NOT NULL DEFAULT '',
	audience   TEXT    NOT NULL,
	user_id    TEXT    NOT NULL DEFAULT '',
	text       TEXT    NOT NULL,
	link_kind  TEXT    NOT NULL DEFAULT '',
	link_ref   TEXT    NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL
);
CREATE INDEX panel_notices_by_time ON panel_notices (created_at);

-- What each user already read: one row per user and notice.
CREATE TABLE panel_notice_reads (
	user_id   TEXT    NOT NULL,
	notice_id INTEGER NOT NULL REFERENCES panel_notices (id),
	read_at   INTEGER NOT NULL,
	PRIMARY KEY (user_id, notice_id)
);

-- The parts of a notice that blocks, masked, so that the panel shows the whole
-- evidence and the steps each user may do (the value of a secret is never here).
ALTER TABLE blocking_notices ADD COLUMN stopped  TEXT NOT NULL DEFAULT '';
ALTER TABLE blocking_notices ADD COLUMN evidence TEXT NOT NULL DEFAULT '';
ALTER TABLE blocking_notices ADD COLUMN steps    TEXT NOT NULL DEFAULT '[]';
ALTER TABLE blocking_notices ADD COLUMN owner    TEXT NOT NULL DEFAULT '';
ALTER TABLE blocking_notices ADD COLUMN agent    TEXT NOT NULL DEFAULT '';
