-- The proof of the criteria of a quest and the failures that repeat
-- (docs/orquestracao/prova-e-devolucao.md). Timestamps are Unix milliseconds in
-- UTC. Every table has a declared primary key, as the change log of the
-- replication needs.

-- The criteria of the quest, as the items `- [ ] C<n> <text>` of its issue read
-- when the quest ran: frozen for the round, so editing the issue changes the
-- next round. marked is 1 when the engine checked the item on the issue (a
-- new round takes back only what it checked).
CREATE TABLE quest_criteria (
	quest  TEXT    NOT NULL REFERENCES quests (id),
	id     TEXT    NOT NULL,
	pos    INTEGER NOT NULL,
	text   TEXT    NOT NULL,
	marked INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (quest, id)
);

-- The last proof of each criterion in each round. origin is declared (the
-- agent said so) or verified (the same step passed the verification of the
-- engine); by_name is the character.
CREATE TABLE quest_proofs (
	quest     TEXT    NOT NULL REFERENCES quests (id),
	round     INTEGER NOT NULL,
	criterion TEXT    NOT NULL,
	text      TEXT    NOT NULL,
	origin    TEXT    NOT NULL,
	by_name   TEXT    NOT NULL,
	step      TEXT    NOT NULL,
	at        INTEGER NOT NULL,
	PRIMARY KEY (quest, round, criterion)
);

-- The failure a step keeps repeating: its fingerprint, how many times in a
-- row, and the places (JSON) of the last one.
CREATE TABLE step_failures (
	step        TEXT    PRIMARY KEY REFERENCES quest_steps (uid),
	fingerprint TEXT    NOT NULL,
	count       INTEGER NOT NULL,
	places      TEXT    NOT NULL DEFAULT '[]'
);
