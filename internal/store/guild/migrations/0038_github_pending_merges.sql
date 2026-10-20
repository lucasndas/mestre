-- A merge of the PR of a table whose commit is not in the mirror yet: the Grand
-- Master was warned and the commit is checked again at each cycle for an hour.
CREATE TABLE github_pending_merges (
	repo     TEXT    NOT NULL,
	number   INTEGER NOT NULL,
	table_id TEXT    NOT NULL,
	sha      TEXT    NOT NULL,
	author   TEXT    NOT NULL DEFAULT '',
	link     TEXT    NOT NULL DEFAULT '',
	summary  TEXT    NOT NULL DEFAULT '',
	since    INTEGER NOT NULL,
	PRIMARY KEY (repo, number)
);
