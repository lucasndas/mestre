-- The users of the Mestre and their levels (docs/telegram/usuarios-e-niveis.md).
-- level is owner, administrator, approver or observer; state is active,
-- suspended or removed (a removed row stays for the history and the audit, and
-- frees its Telegram id). Only one row that is not removed is the owner. The
-- PIN is kept as an argon2id hash, never the PIN. Timestamps are Unix
-- milliseconds in UTC.
CREATE TABLE users (
	id           TEXT    PRIMARY KEY,
	name         TEXT    NOT NULL,
	level        TEXT    NOT NULL,
	state        TEXT    NOT NULL,
	telegram_id  INTEGER NOT NULL DEFAULT 0,
	github_login TEXT    NOT NULL DEFAULT '',
	pin_hash     TEXT    NOT NULL DEFAULT '',
	pin_locked_until INTEGER NOT NULL DEFAULT 0,
	created_at   INTEGER NOT NULL,
	updated_at   INTEGER NOT NULL
);
CREATE UNIQUE INDEX users_one_owner ON users (level) WHERE level = 'owner' AND state <> 'removed';
CREATE UNIQUE INDEX users_telegram ON users (telegram_id) WHERE telegram_id <> 0 AND state <> 'removed';

-- The workspaces a user acts in (the owner acts in all, whatever is here).
CREATE TABLE user_workspaces (
	user_id   TEXT NOT NULL REFERENCES users (id),
	workspace TEXT NOT NULL,
	PRIMARY KEY (user_id, workspace)
);

-- The codes of pairing: single use, 8 characters, valid for 30 minutes.
CREATE TABLE telegram_pairing_codes (
	code       TEXT    PRIMARY KEY,
	user_id    TEXT    NOT NULL REFERENCES users (id),
	created_at INTEGER NOT NULL,
	expires_at INTEGER NOT NULL,
	used_at    INTEGER NOT NULL DEFAULT 0
);

-- The wrong codes, by Telegram id: more than 5 in an hour and the bot is silent.
CREATE TABLE telegram_pairing_failures (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	telegram_id INTEGER NOT NULL,
	at          INTEGER NOT NULL
);
CREATE INDEX telegram_pairing_failures_by_id ON telegram_pairing_failures (telegram_id, at);

-- The requests of PIN (the engine asks, 5 minutes) and the wrong PINs.
CREATE TABLE pin_requests (
	id         TEXT    PRIMARY KEY,
	user_id    TEXT    NOT NULL REFERENCES users (id),
	action     TEXT    NOT NULL,
	created_at INTEGER NOT NULL,
	expires_at INTEGER NOT NULL,
	errors     INTEGER NOT NULL DEFAULT 0,
	state      TEXT    NOT NULL
);
CREATE TABLE pin_failures (
	id      INTEGER PRIMARY KEY AUTOINCREMENT,
	user_id TEXT    NOT NULL REFERENCES users (id),
	at      INTEGER NOT NULL
);
CREATE INDEX pin_failures_by_user ON pin_failures (user_id, at);

-- The transfer of the post of Owner: asked by the Owner with the PIN, accepted
-- by the chosen user with the PIN within 24 hours.
CREATE TABLE owner_transfers (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	from_user  TEXT    NOT NULL REFERENCES users (id),
	to_user    TEXT    NOT NULL REFERENCES users (id),
	state      TEXT    NOT NULL,
	created_at INTEGER NOT NULL,
	expires_at INTEGER NOT NULL,
	ended_at   INTEGER NOT NULL DEFAULT 0
);
