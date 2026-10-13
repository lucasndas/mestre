-- Terminal registry (docs/arquitetura/persistencia.md#registro-de-terminais).
-- A row is written before the mestre-pty of the terminal starts
-- (docs/arquitetura/terminal.md): the socket path, the handshake secret and
-- the mestre-pty version first, the PID and its start identity right after
-- the process starts. Timestamps are Unix milliseconds in UTC. status values
-- are owned by the character states (docs/orquestracao/personagens.md), so
-- there is no CHECK on them.
CREATE TABLE terminals (
	id                  TEXT    PRIMARY KEY,
	character_id        TEXT    NOT NULL,
	machine_id          TEXT    NOT NULL,
	dir                 TEXT    NOT NULL,
	class               TEXT,
	status              TEXT    NOT NULL,
	provider_session_id TEXT,
	socket_path         TEXT    NOT NULL UNIQUE,
	handshake_secret    BLOB    NOT NULL,
	pty_version         TEXT    NOT NULL,
	-- PID of the mestre-pty and the start identity of that process, so a
	-- reused PID is never taken for it: on Linux the boot id and the start
	-- time in clock ticks (/proc/<pid>/stat), on Windows the creation time
	-- (GetProcessTimes). Both NULL until the process has started.
	pty_pid             INTEGER,
	pty_started         TEXT,
	started_at          INTEGER NOT NULL,
	last_seen_at        INTEGER NOT NULL,
	CHECK ((pty_pid IS NULL) = (pty_started IS NULL))
);

CREATE INDEX terminals_status ON terminals (status);
