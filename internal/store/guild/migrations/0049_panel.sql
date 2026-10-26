-- The login of the panel (docs/painel/login.md). A user logs in with a login
-- name and a password kept as an argon2id hash (salt per user), never the
-- password. Timestamps are Unix milliseconds in UTC.
ALTER TABLE users ADD COLUMN login TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN password_hash TEXT NOT NULL DEFAULT '';
CREATE UNIQUE INDEX users_login ON users (login) WHERE login <> '' AND state <> 'removed';

-- The code of setup of the Owner: single use, valid until used; a new one
-- voids the others. Only the hash of the code is kept.
CREATE TABLE panel_setup_codes (
	code_hash  TEXT    PRIMARY KEY,
	created_at INTEGER NOT NULL,
	used_at    INTEGER NOT NULL DEFAULT 0,
	voided_at  INTEGER NOT NULL DEFAULT 0
);

-- The codes of first access and of password reset of a user: single use, 24
-- hours. kind is first or reset. Only the hash of the code is kept.
CREATE TABLE panel_access_codes (
	code_hash  TEXT    PRIMARY KEY,
	user_id    TEXT    NOT NULL REFERENCES users (id),
	kind       TEXT    NOT NULL,
	created_at INTEGER NOT NULL,
	expires_at INTEGER NOT NULL,
	used_at    INTEGER NOT NULL DEFAULT 0,
	voided_at  INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX panel_access_codes_by_user ON panel_access_codes (user_id);

-- The sessions of the panel. token_hash is the SHA-256 of the token of the
-- cookie, which is never kept; id is what the lists show and end.
CREATE TABLE panel_sessions (
	id           TEXT    PRIMARY KEY,
	token_hash   TEXT    NOT NULL UNIQUE,
	user_id      TEXT    NOT NULL REFERENCES users (id),
	device       TEXT    NOT NULL DEFAULT '',
	ip           TEXT    NOT NULL DEFAULT '',
	created_at   INTEGER NOT NULL,
	last_used_at INTEGER NOT NULL
);
CREATE INDEX panel_sessions_by_user ON panel_sessions (user_id);

-- The wrong attempts that limit the login: kind setup_ip (wrong setup codes by
-- IP), login (wrong passwords by login name) and login_block (a block of a
-- login: at is when it ends).
CREATE TABLE panel_attempts (
	id   INTEGER PRIMARY KEY AUTOINCREMENT,
	kind TEXT    NOT NULL,
	key  TEXT    NOT NULL,
	at   INTEGER NOT NULL
);
CREATE INDEX panel_attempts_by_key ON panel_attempts (kind, key, at);
