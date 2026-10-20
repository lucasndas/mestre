-- The periodic summary of the decisions of the agents and the rule suggestions
-- (docs/aprovacoes/resumo-e-sugestoes.md). Timestamps are Unix milliseconds in UTC.
-- The last slot of the summary of each workspace: what came after it is the next summary.
CREATE TABLE digest_state (
	workspace TEXT    PRIMARY KEY REFERENCES workspaces (slug),
	last_slot INTEGER NOT NULL
);

-- A suggestion of rule for the Owner. effect is allow or deny; source is repeat
-- (the same pattern approved again and again) or always ("aprovar sempre" and
-- "negar sempre" of an agent). state is open, accepted, declined or expired; a
-- declined pattern is not suggested again until declined_until. body is the JSON
-- of the examples (the actions already approved and the action the pattern was
-- checked against).
CREATE TABLE digest_suggestions (
	id            INTEGER PRIMARY KEY AUTOINCREMENT,
	workspace     TEXT    NOT NULL REFERENCES workspaces (slug),
	class         TEXT    NOT NULL,
	mesa          TEXT    NOT NULL DEFAULT '',
	pattern       TEXT    NOT NULL,
	effect        TEXT    NOT NULL,
	source        TEXT    NOT NULL,
	reason        TEXT    NOT NULL DEFAULT '',
	state         TEXT    NOT NULL,
	body          TEXT    NOT NULL,
	request       INTEGER NOT NULL DEFAULT 0,
	created_at    INTEGER NOT NULL,
	declined_until INTEGER NOT NULL DEFAULT 0,
	decided_by    TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX digest_suggestions_by_state ON digest_suggestions (workspace, state);

-- "Foi um erro": who marked a decision of an agent as a mistake, and when.
CREATE TABLE digest_marks (
	request INTEGER PRIMARY KEY REFERENCES approval_requests (id),
	by_user TEXT    NOT NULL,
	at      INTEGER NOT NULL
);
