-- The quests (missões) of the Guild (docs/orquestracao/missoes.md). Timestamps
-- are Unix milliseconds in UTC and 0 means "never". Every table has a declared
-- primary key, as the change log of the replication needs.

-- The step of the quest a character was made for (its uid, "<quest>/<step>").
ALTER TABLE characters ADD COLUMN quest_step TEXT NOT NULL DEFAULT '';

-- One row per quest. state is draft, running, paused, completed or cancelled;
-- paused_reason says why it paused. definition is the JSON of the validated
-- definition. round starts at 0 for a draft and grows by one every time the
-- quest runs. designer is the character that made it ('' for a user on the
-- canvas): it never decides an approval point of the quest. scroll is the key
-- of the scroll "Missão: <name>". issue is the number of the issue on GitHub
-- (0 until it is made); issue_dirty is 1 when the mirror on GitHub has
-- something to do (the issue, a sub-issue, the checklist, a comment or a
-- closing that did not go yet); round_commented is the last round that got its comment;
-- issue_closed is 1 once the issue and the sub-issues were closed as not
-- planned (the quest was cancelled).
CREATE TABLE quests (
	id              TEXT    PRIMARY KEY,
	workspace       TEXT    NOT NULL REFERENCES workspaces (slug),
	table_id        TEXT    NOT NULL,
	name            TEXT    NOT NULL,
	state           TEXT    NOT NULL,
	paused_reason   TEXT    NOT NULL DEFAULT '',
	definition      TEXT    NOT NULL,
	max_returns     INTEGER NOT NULL,
	input           TEXT    NOT NULL DEFAULT '',
	round           INTEGER NOT NULL DEFAULT 0,
	designer        TEXT    NOT NULL DEFAULT '',
	scroll          TEXT    NOT NULL DEFAULT '',
	issue           INTEGER NOT NULL DEFAULT 0,
	issue_dirty     INTEGER NOT NULL DEFAULT 0,
	round_commented INTEGER NOT NULL DEFAULT 0,
	issue_closed    INTEGER NOT NULL DEFAULT 0,
	created_at      INTEGER NOT NULL,
	updated_at      INTEGER NOT NULL,
	UNIQUE (workspace, table_id, name)
);

CREATE INDEX quests_by_state ON quests (state);

-- One row per step. uid is "<quest>/<step id>"; pos the place in the
-- definition. state is pending, preparing, running, stuck, approving,
-- completed, discarded or cancelled. character is the character that does it
-- now. returns counts the times the step handed work back (or had an approval
-- after refused). entry is the reason of the last return, the input of the next
-- delivery. blocker is what holds a step in preparing: '', 'approval:<n>',
-- 'slot:<wait>', 'busy', 'sync', 'resume'. approved is 1 once an approval
-- before was granted. approval_request is the open request of approval (0:
-- none). resume_note is the note of a delivery again. discard_result is 1
-- when a return made what the step is doing useless. stuck_at is when the
-- step became stuck, escalated 1 once the Grand Master was told.
-- sub_issue is the sub-issue of the step (0: none yet), sub_closed 1 once it
-- was closed, commented_round the last round whose summary was commented.
CREATE TABLE quest_steps (
	uid              TEXT    PRIMARY KEY,
	quest            TEXT    NOT NULL REFERENCES quests (id),
	id               TEXT    NOT NULL,
	pos              INTEGER NOT NULL,
	state            TEXT    NOT NULL,
	character        TEXT    NOT NULL DEFAULT '',
	returns          INTEGER NOT NULL DEFAULT 0,
	entry            TEXT    NOT NULL DEFAULT '',
	blocker          TEXT    NOT NULL DEFAULT '',
	approved         INTEGER NOT NULL DEFAULT 0,
	approval_request INTEGER NOT NULL DEFAULT 0,
	resume_note      TEXT    NOT NULL DEFAULT '',
	discard_result   INTEGER NOT NULL DEFAULT 0,
	stuck_at         INTEGER NOT NULL DEFAULT 0,
	escalated        INTEGER NOT NULL DEFAULT 0,
	sub_issue        INTEGER NOT NULL DEFAULT 0,
	sub_closed       INTEGER NOT NULL DEFAULT 0,
	commented_round  INTEGER NOT NULL DEFAULT 0,
	updated_at       INTEGER NOT NULL,
	UNIQUE (quest, id)
);

CREATE INDEX quest_steps_by_character ON quest_steps (character, state);

-- One row per delivery of a step to a character (a run). outcome is '' while
-- it runs, then completed, discarded or returned. start_commit is the commit
-- of the branch of the table when the step was delivered, end_commit when it
-- gave its done. files is the JSON list of the changes (git diff
-- --name-status), uncommitted the JSON list of the files not committed.
CREATE TABLE step_runs (
	id             INTEGER PRIMARY KEY AUTOINCREMENT,
	step           TEXT    NOT NULL REFERENCES quest_steps (uid),
	quest          TEXT    NOT NULL,
	round          INTEGER NOT NULL,
	attempt        INTEGER NOT NULL,
	character      TEXT    NOT NULL,
	character_name TEXT    NOT NULL DEFAULT '',
	class          TEXT    NOT NULL DEFAULT '',
	start_commit   TEXT    NOT NULL DEFAULT '',
	end_commit     TEXT    NOT NULL DEFAULT '',
	started_at     INTEGER NOT NULL,
	ended_at       INTEGER NOT NULL DEFAULT 0,
	outcome        TEXT    NOT NULL DEFAULT '',
	summary        TEXT    NOT NULL DEFAULT '',
	files          TEXT    NOT NULL DEFAULT '[]',
	uncommitted    TEXT    NOT NULL DEFAULT '[]',
	warnings       TEXT    NOT NULL DEFAULT '[]'
);

CREATE INDEX step_runs_by_step ON step_runs (step, id);
