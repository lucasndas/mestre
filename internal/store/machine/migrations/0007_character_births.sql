-- What a character was born with (docs/sessao-zero/personagem.md#modelos-versionados-e-regeneração):
-- the version of the models of the Mestre layer, which stays until someone
-- regenerates, and the copy of its sheet and base, so a later edit of either
-- applies to new characters only (docs/orquestracao/bases-e-fichas.md). The
-- sheet and the base are JSON. Timestamps are Unix milliseconds in UTC.
CREATE TABLE character_births (
	agent_id      TEXT    PRIMARY KEY,
	provider      TEXT    NOT NULL,
	model_version TEXT    NOT NULL,
	sheet         TEXT    NOT NULL,
	base          TEXT    NOT NULL,
	born_at       INTEGER NOT NULL
);
