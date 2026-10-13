-- Who this machine is in the Guild (docs/arquitetura/persistencia.md,
-- docs/guilda/guilda.md). One row, written at the creation of the Guild or at
-- the entry; no row means the machine is outside any Guild. The public data
-- only: the private keys live in files protected by the system. role is not
-- stored, it follows from the certificate of epoch (epoch_cert): the Citadel
-- is the machine it names. epoch_cert is the highest certificate seen (the
-- JSON of docs/guilda/replicacao.md#certidão-de-época), only ever replaced by
-- a higher one. Timestamps are Unix milliseconds in UTC.
CREATE TABLE machine_identity (
	id               INTEGER PRIMARY KEY CHECK (id = 1),
	machine_id       TEXT    NOT NULL,
	guild_id         TEXT    NOT NULL,
	instance         TEXT    NOT NULL DEFAULT '',
	ca_cert          BLOB    NOT NULL,
	motor_cert       BLOB    NOT NULL,
	guardian_cert    BLOB    NOT NULL,
	epoch_cert       TEXT    NOT NULL,
	protocol_version INTEGER,
	joined_at        INTEGER NOT NULL,
	updated_at       INTEGER NOT NULL
);
