-- The epochs of the Guild and its audit trail (docs/guilda/cidadela-e-torres.md,
-- docs/arquitetura/persistencia.md). Every table has a declared primary key
-- and there are no triggers, for the replication log (CA-REPL-11).
-- Timestamps are Unix milliseconds in UTC.

-- epochs is one row per epoch: the machine its certificate names, the last
-- lsn that machine had applied when it took the command (from_lsn; what the
-- old Citadel committed after it was lost, docs/guilda/replicacao.md#perda-na-troca;
-- 0 at the creation of the Guild) and the approval of the Owner.
CREATE TABLE epochs (
	epoch              INTEGER PRIMARY KEY,
	citadel_machine_id TEXT    NOT NULL,
	from_lsn           INTEGER NOT NULL,
	approval_id        TEXT    NOT NULL DEFAULT '',
	approved_by        TEXT    NOT NULL DEFAULT '',
	started_at         INTEGER NOT NULL
);

-- audit_log is the audit trail of the Guild. action names the event
-- (citadel.promoted, citadel.discarded…); detail is JSON and never holds a
-- value of the vault.
CREATE TABLE audit_log (
	id         TEXT    PRIMARY KEY,
	at         INTEGER NOT NULL,
	machine_id TEXT    NOT NULL,
	action     TEXT    NOT NULL,
	detail     TEXT    NOT NULL DEFAULT '{}'
);

CREATE INDEX audit_log_at ON audit_log (at);
