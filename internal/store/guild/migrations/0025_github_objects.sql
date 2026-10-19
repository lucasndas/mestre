-- What the engine created on GitHub for a table (docs/github/acoes-por-nivel.md):
-- the issue, the sub-issue, the PR, the comment. The owner of a target is read
-- from here before anything is asked of GitHub. op is the uuid of the operation
-- (the marker `<!-- mestre:op=... -->` of the body, or the one kept before the
-- sending of what has no body), unique, so that a resent operation finds what it
-- already made. Timestamps are Unix milliseconds in UTC.
CREATE TABLE github_objects (
	repo          TEXT    NOT NULL,
	kind          TEXT    NOT NULL,
	number        INTEGER NOT NULL,
	github_id     INTEGER NOT NULL DEFAULT 0,
	table_id      TEXT    NOT NULL DEFAULT '',
	parent_number INTEGER NOT NULL DEFAULT 0,
	op            TEXT    NOT NULL DEFAULT '',
	fingerprint   TEXT    NOT NULL DEFAULT '',
	created_at    INTEGER NOT NULL,
	PRIMARY KEY (repo, kind, number)
);
CREATE UNIQUE INDEX github_objects_op ON github_objects (op) WHERE op <> '';
