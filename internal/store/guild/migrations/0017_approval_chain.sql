-- The chain of approval of the Guild (docs/orquestracao/cadeia-de-aprovacao.md).
-- Timestamps are Unix milliseconds in UTC and 0 means "never". Every table has
-- a declared primary key, as the change log of the replication needs.

-- One row per request of approval. id is the number the hook, the quests and
-- the people know it by. state is open, approved, denied, expired or cancelled.
-- requester is the character that asked ('' for a request with none: a PR, an
-- extra cost). fingerprint is the exact action of a permissao_de_classe, so
-- that repeating it while the request is open finds the same one. level_kind
-- and level_agent are the level the request is at now (agent, mesa,
-- grand_master or people; the character for the first three); deadline is when
-- that level's time runs out (0: none, the request waits for an answer of who
-- asked). body is the JSON of the rest (the action, the event, the risk with
-- its groups, the justification, the item of the inbox of the level).
CREATE TABLE approval_requests (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	workspace   TEXT    NOT NULL,
	table_id    TEXT    NOT NULL DEFAULT '',
	type        TEXT    NOT NULL,
	state       TEXT    NOT NULL,
	requester   TEXT    NOT NULL DEFAULT '',
	fingerprint TEXT    NOT NULL DEFAULT '',
	level_kind  TEXT    NOT NULL DEFAULT '',
	level_agent TEXT    NOT NULL DEFAULT '',
	deadline    INTEGER NOT NULL DEFAULT 0,
	body        TEXT    NOT NULL,
	created_at  INTEGER NOT NULL,
	closed_at   INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX approval_requests_open ON approval_requests (state, requester);

-- The audit: one row per decision, accepted or refused, and per climb. answer
-- is what was given (approve, deny, climb…); outcome is accepted, refused or
-- climbed. body carries the reason, the refusal code and the versions of the
-- risk table, of the effective rules and of the policy that held.
CREATE TABLE approval_audit (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	request    INTEGER NOT NULL REFERENCES approval_requests (id),
	at         INTEGER NOT NULL,
	approver   TEXT    NOT NULL DEFAULT '',
	level_kind TEXT    NOT NULL DEFAULT '',
	answer     TEXT    NOT NULL,
	outcome    TEXT    NOT NULL,
	body       TEXT    NOT NULL
);

CREATE INDEX approval_audit_by_request ON approval_audit (request, id);

-- The circuit breaker: denials in a row of the requests of a character, and
-- whether the character is paused by it.
CREATE TABLE approval_breaker (
	character TEXT    PRIMARY KEY,
	denials   INTEGER NOT NULL DEFAULT 0,
	paused    INTEGER NOT NULL DEFAULT 0,
	paused_at INTEGER NOT NULL DEFAULT 0
);

-- What the agents could not decide ("approve always", "deny always") and
-- became a suggestion for the summary of the Owner.
CREATE TABLE approval_suggestions (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	request    INTEGER NOT NULL REFERENCES approval_requests (id),
	at         INTEGER NOT NULL,
	kind       TEXT    NOT NULL,
	by         TEXT    NOT NULL DEFAULT '',
	body       TEXT    NOT NULL
);
