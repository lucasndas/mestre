-- Effective rules of the characters of this machine
-- (docs/classes/permissoes.md#regras-efetivas): the last agent.rules of each
-- one, by generated_at (an older document never replaces a newer one), and
-- the exceptions already used, so none is used twice before the Citadel
-- drops it from the next version. doc is the JSON document as received.
-- Timestamps are Unix milliseconds in UTC.
CREATE TABLE agent_rules (
	agent_id     TEXT    PRIMARY KEY,
	generated_at INTEGER NOT NULL,
	doc          BLOB    NOT NULL,
	received_at  INTEGER NOT NULL
);

CREATE TABLE used_exceptions (
	agent_id  TEXT    NOT NULL,
	decree_id INTEGER NOT NULL,
	used_at   INTEGER NOT NULL,
	PRIMARY KEY (agent_id, decree_id)
);
