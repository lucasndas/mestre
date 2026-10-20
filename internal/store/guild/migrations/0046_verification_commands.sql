-- The verification commands a workspace has confirmed
-- (docs/sessao-zero/workspace.md#comandos-de-verificação). Timestamps are Unix
-- milliseconds in UTC. Every table has a declared primary key, as the change log
-- of the replication needs.

-- The last confirmed version of .mestre/regras/verificacao.yaml of each
-- workspace: what the verification runs is this document, never the file on
-- disk or the one of a branch. hash is the SHA-256 of doc.
CREATE TABLE verification_commands (
	workspace    TEXT    PRIMARY KEY,
	hash         TEXT    NOT NULL,
	doc          TEXT    NOT NULL,
	confirmed_by TEXT    NOT NULL,
	confirmed_at INTEGER NOT NULL
);
