-- The classes of the Guild (docs/classes/classes.md#onde-a-classe-é-alterada).
-- classes is the index and the approved version of each class: the hash of
-- the package and the package itself, so the approved version keeps holding
-- when the files change outside the Mestre. scope is global, workspace or
-- table; tbl is the table of a table class and '' for the others. version
-- grows with each approved version. approved_level is the level of who
-- approved it (or created it with the authority of the scope). Timestamps
-- are Unix milliseconds in UTC.
CREATE TABLE classes (
	workspace      TEXT    NOT NULL,
	scope          TEXT    NOT NULL,
	tbl            TEXT    NOT NULL,
	name           TEXT    NOT NULL,
	hash           TEXT    NOT NULL,
	package        BLOB    NOT NULL,
	app            TEXT    NOT NULL,
	version        INTEGER NOT NULL,
	approved_at    INTEGER NOT NULL,
	approved_level TEXT    NOT NULL,
	approved_by    TEXT    NOT NULL,
	PRIMARY KEY (workspace, scope, tbl, name)
);

-- A change of a class that waits for approval: made through the Mestre by
-- someone who only proposes (the Grand Master), or found on disk different
-- from the approved version (a hand edit, a git pull). problems is the list
-- of what makes it invalid (empty when it is valid); a proposal with
-- problems cannot be approved. state is pending, approved or superseded.
CREATE TABLE class_proposals (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	workspace  TEXT    NOT NULL,
	scope      TEXT    NOT NULL,
	tbl        TEXT    NOT NULL,
	name       TEXT    NOT NULL,
	hash       TEXT    NOT NULL,
	package    BLOB    NOT NULL,
	origin     TEXT    NOT NULL,
	proposed_by TEXT   NOT NULL,
	problems   TEXT    NOT NULL,
	state      TEXT    NOT NULL,
	created_at INTEGER NOT NULL
);
