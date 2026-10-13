-- The Guild, its machines and their credentials (docs/guilda/guilda.md,
-- docs/arquitetura/persistencia.md). Every table has a declared primary key
-- and there are no triggers: the replication log captures the changes of a
-- row by key (docs/guilda/replicacao.md, CA-REPL-11). Timestamps are Unix
-- milliseconds in UTC.

-- guild is the one row of the Guild: its id, the public CA certificate (the
-- key lives in the vault), the highest epoch and its certificate (the JSON
-- of docs/guilda/replicacao.md#certidão-de-época) and the Guild time zone.
CREATE TABLE guild (
	id                  INTEGER PRIMARY KEY CHECK (id = 1),
	guild_id            TEXT    NOT NULL,
	ca_cert             BLOB    NOT NULL,
	epoch               INTEGER NOT NULL,
	epoch_certificate   TEXT    NOT NULL,
	timezone            TEXT    NOT NULL,
	recovery_public_key BLOB,
	created_at          INTEGER NOT NULL
);

-- machines is the registry of the machines of the Guild. state is active,
-- removed (by the Owner) or left (uninstalled by itself). names is the JSON
-- {"hostname", "ips", "extra"} the certificates are issued for; sheet is the
-- last machine sheet (docs/guilda/ficha-da-maquina.md) as JSON.
CREATE TABLE machines (
	machine_id TEXT    PRIMARY KEY,
	name       TEXT    NOT NULL,
	instance   TEXT    NOT NULL DEFAULT '',
	priority   INTEGER NOT NULL DEFAULT 0,
	excluded   INTEGER NOT NULL DEFAULT 0,
	state      TEXT    NOT NULL,
	names      TEXT    NOT NULL,
	sheet      TEXT,
	joined_at  INTEGER NOT NULL,
	removed_at INTEGER
);

-- machine_credentials is the identifier of every certificate the CA issued
-- and whether it was revoked; the private keys never come here. role is
-- motor or guardiao. cert is the public certificate (DER), kept so a renewal
-- knows the one it replaces.
CREATE TABLE machine_credentials (
	serial     TEXT    PRIMARY KEY,
	machine_id TEXT    NOT NULL,
	role       TEXT    NOT NULL,
	cert       BLOB    NOT NULL,
	not_after  INTEGER NOT NULL,
	issued_at  INTEGER NOT NULL,
	revoked_at INTEGER
);

CREATE INDEX machine_credentials_machine ON machine_credentials (machine_id, role, issued_at);

-- join_requests is the entry of a new machine (docs/guilda/guilda.md#entrada-na-guilda).
-- state is pending, approved, refused or expired. The nonce of the machine
-- is known only after the reveal; code is then computed and the Owner asked.
-- bundle is what the machine receives on approval (public data only).
CREATE TABLE join_requests (
	join_id     TEXT    PRIMARY KEY,
	state       TEXT    NOT NULL,
	remote_ip   TEXT    NOT NULL,
	name        TEXT    NOT NULL,
	request     TEXT    NOT NULL,
	csr_motor   BLOB    NOT NULL,
	csr_guard   BLOB    NOT NULL,
	commitment  BLOB    NOT NULL,
	nonce_c     BLOB    NOT NULL,
	nonce_n     BLOB,
	fingerprint BLOB    NOT NULL,
	code        TEXT,
	approval_id TEXT,
	machine_id  TEXT,
	bundle      TEXT,
	decided_by  TEXT,
	created_at  INTEGER NOT NULL,
	expires_at  INTEGER NOT NULL,
	decided_at  INTEGER
);

CREATE INDEX join_requests_ip ON join_requests (remote_ip, created_at);
CREATE INDEX join_requests_state ON join_requests (state, created_at);
