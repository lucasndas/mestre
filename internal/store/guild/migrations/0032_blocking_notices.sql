-- The messages that ask something of the users because a table can not go on
-- without it (docs/aprovacoes/pedido-que-bloqueia.md): the whole text, masked,
-- that "Ver detalhes" shows and the panel reads. The evidence is never kept as the
-- engine had it: only the masked text. Timestamps are Unix milliseconds in UTC.
CREATE TABLE blocking_notices (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	workspace  TEXT    NOT NULL,
	mesa       TEXT    NOT NULL DEFAULT '',
	motive     TEXT    NOT NULL,
	summary    TEXT    NOT NULL,
	full       TEXT    NOT NULL,
	machine    TEXT    NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL
);
