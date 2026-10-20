-- The commands of verification of a workspace that an Administrator confirmed
-- (docs/sessao-zero/workspace.md#comandos-de-verificação). The file
-- .mestre/regras/verificacao.yaml of the main clone holds the proposal and, later,
-- what people edit; only this record says what holds: the last version confirmed.
-- Timestamps are Unix milliseconds in UTC.
--
-- hash is the sha256 of the confirmed document and doc is the document itself, so
-- an edit made outside the panel changes nothing; alerted_hash is the hash of the
-- outside edit the Administrators were told about, so each one is told once.
CREATE TABLE verification_commands (
	workspace    TEXT    NOT NULL PRIMARY KEY,
	hash         TEXT    NOT NULL,
	doc          TEXT    NOT NULL,
	confirmed_by TEXT    NOT NULL,
	confirmed_at INTEGER NOT NULL,
	alerted_hash TEXT    NOT NULL DEFAULT ''
);
