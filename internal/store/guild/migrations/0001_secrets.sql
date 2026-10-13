-- Vault table (docs/plataforma/cofre.md#formato-proposta). Timestamps are
-- Unix milliseconds in UTC. Secret values are only ever stored encrypted.
CREATE TABLE secrets (
	name         TEXT    PRIMARY KEY,
	type         TEXT    NOT NULL,
	ciphertext   BLOB    NOT NULL,
	nonce        BLOB    NOT NULL,
	key_version  INTEGER NOT NULL,
	fingerprint  TEXT    NOT NULL,
	owner_user   TEXT,
	classes      TEXT,
	domains      TEXT,
	kind         TEXT,
	expires_at   INTEGER,
	created_at   INTEGER NOT NULL,
	updated_at   INTEGER NOT NULL,
	last_used_at INTEGER
);
