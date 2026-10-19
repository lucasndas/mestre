-- The lock of the channel (/travar and /destravar, docs/telegram/seguranca-do-canal.md).
-- One row: whether the Mestre is locked, who locked it and when. The state
-- survives the restarts of the Citadel and goes to the Towers and the Guardians
-- with the replication of the Guild. Timestamps are Unix milliseconds in UTC.
CREATE TABLE channel_lock (
	id      INTEGER PRIMARY KEY CHECK (id = 1),
	locked  INTEGER NOT NULL DEFAULT 0,
	by_user TEXT    NOT NULL DEFAULT '',
	at      INTEGER NOT NULL DEFAULT 0
);
INSERT INTO channel_lock (id, locked) VALUES (1, 0);

-- The audit of the lock: who tried what, and what became of it (a /travar of a
-- level without permission is refused and recorded).
CREATE TABLE channel_lock_audit (
	id      INTEGER PRIMARY KEY AUTOINCREMENT,
	at      INTEGER NOT NULL,
	user_id TEXT    NOT NULL,
	action  TEXT    NOT NULL,
	outcome TEXT    NOT NULL,
	detail  TEXT    NOT NULL DEFAULT ''
);
