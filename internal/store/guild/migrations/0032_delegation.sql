-- Delegation (docs/aprovacoes/delegacao.md): the reminder of a delegation that
-- is about to end (reminded_at is when it was sent; 0 is not yet), the
-- subdelegations the Grand Master gives a Mestre da mesa and the log of the
-- emergency button of the Owner. Timestamps are Unix milliseconds in UTC.
ALTER TABLE decrees ADD COLUMN reminded_at INTEGER NOT NULL DEFAULT 0;

-- A subdelegation is narrower than what the Grand Master may decide: origin is
-- the number of the decree of delegation it comes from, or 0 when it comes from
-- the ceiling of the Grand Master (medio). state is active or revoked; it ends
-- with the origin, at until, or when it is revoked.
CREATE TABLE subdelegations (
	id           INTEGER PRIMARY KEY AUTOINCREMENT,
	workspace    TEXT    NOT NULL REFERENCES workspaces (slug),
	mesa         TEXT    NOT NULL,
	request_type TEXT    NOT NULL,
	grantor      TEXT    NOT NULL,
	grantee      TEXT    NOT NULL,
	origin       INTEGER NOT NULL DEFAULT 0,
	reason       TEXT    NOT NULL DEFAULT '',
	state        TEXT    NOT NULL,
	created_at   INTEGER NOT NULL,
	until        INTEGER NOT NULL,
	ended_at     INTEGER NOT NULL DEFAULT 0,
	ended_by     TEXT    NOT NULL DEFAULT '',
	ended_note   TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX subdelegations_live ON subdelegations (workspace, state);

-- What each use of a subdelegation decided.
CREATE TABLE subdelegation_uses (
	id            INTEGER PRIMARY KEY AUTOINCREMENT,
	subdelegation INTEGER NOT NULL REFERENCES subdelegations (id),
	at            INTEGER NOT NULL,
	character     TEXT    NOT NULL,
	action        TEXT    NOT NULL
);

-- "Revogar todas as delegações": who, when and how much it took away.
CREATE TABLE delegation_revocations (
	id             INTEGER PRIMARY KEY AUTOINCREMENT,
	at             INTEGER NOT NULL,
	by_user        TEXT    NOT NULL,
	decrees        INTEGER NOT NULL,
	subdelegations INTEGER NOT NULL
);
