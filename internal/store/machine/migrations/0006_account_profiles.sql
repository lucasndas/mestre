-- The account profiles of this machine (docs/orquestracao/bases-e-fichas.md#perfis-de-conta):
-- the isolated configuration folder of an account of a provider, under the
-- machine's contas folder. is_default marks the profile a sheet without
-- `account` uses. setup_step is where the guided setup stands (install,
-- login, hooks, conformance, done) and setup_detail what it waits for or why
-- it stopped. Timestamps are Unix milliseconds in UTC.
CREATE TABLE account_profiles (
	name          TEXT    PRIMARY KEY,
	provider      TEXT    NOT NULL,
	dir           TEXT    NOT NULL,
	is_default    INTEGER NOT NULL,
	setup_step    TEXT    NOT NULL,
	setup_detail  TEXT    NOT NULL,
	created_at    INTEGER NOT NULL,
	setup_done_at INTEGER
);
