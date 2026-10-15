-- The copy, in the machine database, of what the engine of a machine knows of
-- the characters that run on it (docs/arquitetura/persistencia.md): the state
-- they are in, so that an engine that comes back finds each character in the
-- state it was in, without passing through interrupted
-- (docs/orquestracao/personagens.md#retomada-após-reinício), and what it needs
-- to supervise them again. The Citadel is the truth about the rest.
--
-- launch is the JSON of what the engine needs to rebuild the environment and
-- the supervision of the character: the worktree, MESTRE_TMP, the table, the
-- provider and how the base says the terminal is ready. Timestamps are Unix
-- milliseconds in UTC.
CREATE TABLE character_runtime (
	agent_id           TEXT    PRIMARY KEY,
	terminal_id        TEXT    NOT NULL,
	state              TEXT    NOT NULL,
	reason             TEXT    NOT NULL DEFAULT '',
	provider_session_id TEXT   NOT NULL DEFAULT '',
	last_turn          TEXT    NOT NULL DEFAULT '',
	reduced_monitoring TEXT    NOT NULL DEFAULT '',
	starts             INTEGER NOT NULL DEFAULT 1,
	launch             TEXT    NOT NULL,
	state_since        INTEGER NOT NULL,
	last_signal_at     INTEGER NOT NULL
);
