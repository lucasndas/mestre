-- What the watcher of the vault already told about the site credentials
-- (docs/plataforma/cofre.md#credenciais-de-site): one row per credential and
-- reason and person, so a notice is made once for each person (one who could not
-- be told is tried again without the others being told twice) and a restart does
-- not repeat it.
-- Timestamps are Unix milliseconds in UTC. The table holds names, people and
-- times, never a value, and goes with the vault tables: not to an excluded
-- machine.
--
-- kind is 'expiring' (ref is the expires_at the notice was made for: a new date
-- starts over) or 'stale' (ref is the moment the 90 days counted from; the next
-- reminder counts from noticed_at).
CREATE TABLE vault_notices (
	name        TEXT    NOT NULL,
	kind        TEXT    NOT NULL,
	person      TEXT    NOT NULL,
	ref         INTEGER NOT NULL,
	noticed_at  INTEGER NOT NULL,
	PRIMARY KEY (name, kind, person)
);
