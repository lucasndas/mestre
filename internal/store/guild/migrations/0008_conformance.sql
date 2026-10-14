-- The results of the conformance test of the providers
-- (docs/provedores/conformidade.md#resultado). One row per machine, account
-- profile and provider: a new run replaces the row of its combination, with
-- the version of the CLI it tested. conformant is 1 when steps 1, 2, 4 and 6
-- passed and every required event arrived. steps is the JSON list of the
-- steps (number, outcome, duration, reason and the masked transcript of the
-- terminal); flags is the JSON object of what the result switches on
-- (the managed copy of the skills that may be turned off, the resume that
-- reborns, the subagent tool denied, the diagnosis without the provider).
-- Timestamps are Unix milliseconds in UTC.
CREATE TABLE conformance_results (
	machine     TEXT    NOT NULL,
	profile     TEXT    NOT NULL,
	provider    TEXT    NOT NULL,
	version     TEXT    NOT NULL,
	conformant  INTEGER NOT NULL,
	reason      TEXT    NOT NULL,
	origin      TEXT    NOT NULL,
	steps       TEXT    NOT NULL,
	flags       TEXT    NOT NULL,
	started_at  INTEGER NOT NULL,
	finished_at INTEGER NOT NULL,
	PRIMARY KEY (machine, profile, provider)
);
