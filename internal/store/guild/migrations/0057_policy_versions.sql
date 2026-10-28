-- The last valid approval policy of each workspace (docs/aprovacoes/arquivo-de-politica.md).
-- The file lives in a folder the agents reach; this is the copy the Citadel falls back to
-- when the file is replaced, missing or untrusted after a restart. hash is the SHA-256 of
-- doc. Every table has a declared primary key, as the change log of the replication needs.
CREATE TABLE policy_versions (
	workspace TEXT PRIMARY KEY,
	hash      TEXT NOT NULL,
	doc       TEXT NOT NULL
) STRICT;
