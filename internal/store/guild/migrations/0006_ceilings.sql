-- The approved version of the ceiling of each workspace
-- (docs/classes/teto-do-workspace.md#arquivo-mudado-por-fora): the hash of
-- teto.yaml and the file itself, so the approved version keeps holding when
-- the file changes outside the panel and the decrees. approved_level and
-- approved_via are who approved it and how (panel or decree); decree_id the
-- permanent decree, when there is one. Timestamps are Unix milliseconds in
-- UTC.
CREATE TABLE workspace_ceilings (
	workspace      TEXT    PRIMARY KEY,
	hash           TEXT    NOT NULL,
	doc            BLOB    NOT NULL,
	approved_at    INTEGER NOT NULL,
	approved_level TEXT    NOT NULL,
	approved_via   TEXT    NOT NULL,
	decree_id      INTEGER
);
