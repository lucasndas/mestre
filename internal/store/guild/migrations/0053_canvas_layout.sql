-- The layout of the canvas of a table (docs/painel/canvas.md#canvas-infinito):
-- where each element stands, how big it is and the free group it sits in. key is
-- "<kind>:<id>" (character, scroll, tome or group); a group is an element too, with
-- its own label. Sizes of 0 mean the default size of the kind.
CREATE TABLE canvas_items (
	workspace  TEXT    NOT NULL,
	table_id   TEXT    NOT NULL,
	key        TEXT    NOT NULL,
	label      TEXT    NOT NULL DEFAULT '',
	x          REAL    NOT NULL DEFAULT 0,
	y          REAL    NOT NULL DEFAULT 0,
	w          REAL    NOT NULL DEFAULT 0,
	h          REAL    NOT NULL DEFAULT 0,
	parent     TEXT    NOT NULL DEFAULT '',
	updated_at INTEGER NOT NULL,
	PRIMARY KEY (workspace, table_id, key)
);
