-- The decrees (docs/telegram/decretos.md): the orders of the users that stand
-- above the rules. A decree is a permanent rule or a one-time exception; the
-- number is the id, sequential in the Guild. state is active, used, expired,
-- revoked or suspended. The effect is the JSON of the effect (permission,
-- behavior, policy, ceiling or delegation); action and target_ref bind an
-- exception to the exact action and to the state it was approved in
-- (impression is the hash of that state). Timestamps are Unix milliseconds in
-- UTC; until 0 is forever.
CREATE TABLE decrees (
	id           INTEGER PRIMARY KEY AUTOINCREMENT,
	kind         TEXT    NOT NULL,
	state        TEXT    NOT NULL,
	effect_kind  TEXT    NOT NULL,
	effect       TEXT    NOT NULL,
	target_kind  TEXT    NOT NULL,
	target       TEXT    NOT NULL,
	workspace    TEXT    NOT NULL,
	mesa         TEXT    NOT NULL DEFAULT '',
	scope        TEXT    NOT NULL DEFAULT '',
	author_id    TEXT    NOT NULL,
	author_level TEXT    NOT NULL,
	text         TEXT    NOT NULL,
	origin       TEXT    NOT NULL DEFAULT '{}',
	pin          INTEGER NOT NULL DEFAULT 0,
	request      INTEGER NOT NULL DEFAULT 0,
	action       TEXT    NOT NULL DEFAULT '',
	target_ref   TEXT    NOT NULL DEFAULT '',
	request_type TEXT    NOT NULL DEFAULT '',
	impression   TEXT    NOT NULL DEFAULT '',
	suspended_by TEXT    NOT NULL DEFAULT '',
	created_at   INTEGER NOT NULL,
	until        INTEGER NOT NULL DEFAULT 0,
	ended_at     INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX decrees_live ON decrees (workspace, state);
CREATE UNIQUE INDEX decrees_one_exception_per_request ON decrees (request) WHERE kind = 'exception' AND request <> 0;

-- The history of the states of each decree.
CREATE TABLE decree_events (
	id     INTEGER PRIMARY KEY AUTOINCREMENT,
	decree INTEGER NOT NULL REFERENCES decrees (id),
	at     INTEGER NOT NULL,
	state  TEXT    NOT NULL,
	by     TEXT    NOT NULL DEFAULT '',
	note   TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX decree_events_by_decree ON decree_events (decree, id);

-- What was done on the strength of each decree.
CREATE TABLE decree_uses (
	id        INTEGER PRIMARY KEY AUTOINCREMENT,
	decree    INTEGER NOT NULL REFERENCES decrees (id),
	at        INTEGER NOT NULL,
	character TEXT    NOT NULL,
	action    TEXT    NOT NULL
);
CREATE INDEX decree_uses_by_decree ON decree_uses (decree, id);

-- The proposals the Grand Master makes and the author confirms or cancels:
-- nothing is a decree before the click of the author. state is open,
-- confirmed or cancelled; payload is the JSON of the proposal.
CREATE TABLE decree_proposals (
	id         TEXT    PRIMARY KEY,
	author_id  TEXT    NOT NULL,
	payload    TEXT    NOT NULL,
	state      TEXT    NOT NULL,
	needs_pin  INTEGER NOT NULL DEFAULT 0,
	decree     INTEGER NOT NULL DEFAULT 0,
	created_at INTEGER NOT NULL,
	expires_at INTEGER NOT NULL
);
