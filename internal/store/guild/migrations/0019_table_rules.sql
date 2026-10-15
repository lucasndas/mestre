-- The temporary table rules of the agents of the approval chain
-- (docs/orquestracao/regras-temporarias-de-mesa.md). Timestamps are Unix
-- milliseconds in UTC and 0 means "never". Every table has a declared primary
-- key, as the change log of the replication needs.

-- One row per rule. state is active, expired, revoked or suspended; level is
-- the level of the author (agent, mesa or grand_master) and author the
-- character that made it; risk is the highest risk it covers (baixo or medio).
-- until is when it ends by itself; mission, when set, is the quest whose end
-- ends it too. body is the JSON of what it covers: the pattern with its tool
-- kind, or the exact action.
CREATE TABLE table_rules (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	workspace  TEXT    NOT NULL,
	table_id   TEXT    NOT NULL,
	class      TEXT    NOT NULL,
	request    INTEGER NOT NULL DEFAULT 0,
	state      TEXT    NOT NULL,
	level      TEXT    NOT NULL,
	author     TEXT    NOT NULL,
	risk       TEXT    NOT NULL,
	until      INTEGER NOT NULL,
	mission    TEXT    NOT NULL DEFAULT '',
	body       TEXT    NOT NULL,
	created_at INTEGER NOT NULL,
	ended_at   INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX table_rules_by_table ON table_rules (workspace, table_id, state);

-- The history of the states of a rule, with why each change happened.
CREATE TABLE table_rule_states (
	id     INTEGER PRIMARY KEY AUTOINCREMENT,
	rule   INTEGER NOT NULL REFERENCES table_rules (id),
	at     INTEGER NOT NULL,
	state  TEXT    NOT NULL,
	reason TEXT    NOT NULL DEFAULT '',
	by     TEXT    NOT NULL DEFAULT ''
);

CREATE INDEX table_rule_states_by_rule ON table_rule_states (rule, id);

-- What a rule let through: the action, when, and who made it.
CREATE TABLE table_rule_uses (
	id        INTEGER PRIMARY KEY AUTOINCREMENT,
	rule      INTEGER NOT NULL REFERENCES table_rules (id),
	at        INTEGER NOT NULL,
	character TEXT    NOT NULL,
	action    TEXT    NOT NULL
);

CREATE INDEX table_rule_uses_by_rule ON table_rule_uses (rule, id);
