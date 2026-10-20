-- The requests of creation and installation of the GitHub Apps by link
-- (docs/github/criacao-de-apps.md): one for each link sent to the Owner. Only the
-- hash of the token of the link and of the state is kept. status: link (sent),
-- opened (the Owner opened it and has one hour to hand the code back), created
-- (the code was exchanged and the key is in the vault), exchanging (while the
-- code is exchanged), unconfirmed (no installation seen in an hour), done, expired, failed
-- or replaced (a newer link took its place). Timestamps are Unix milliseconds
-- in UTC.
CREATE TABLE github_app_requests (
	id              INTEGER PRIMARY KEY AUTOINCREMENT,
	app             TEXT    NOT NULL REFERENCES github_apps (name),
	link_hash       BLOB    NOT NULL UNIQUE,
	state_hash      BLOB    NOT NULL UNIQUE,
	status          TEXT    NOT NULL,
	requested_by    TEXT    NOT NULL,
	opened_by       TEXT    NOT NULL DEFAULT '',
	link_expires_at INTEGER NOT NULL,
	code_deadline   INTEGER NOT NULL DEFAULT 0,
	polled_at       INTEGER NOT NULL DEFAULT 0,
	flow_told       INTEGER NOT NULL DEFAULT 0,
	created_at      INTEGER NOT NULL,
	updated_at      INTEGER NOT NULL
);
CREATE INDEX github_app_requests_app ON github_app_requests (app, status);

-- What was done with the links and the Apps: who asked, who opened, what came out.
CREATE TABLE github_app_audit (
	id      INTEGER PRIMARY KEY AUTOINCREMENT,
	at      INTEGER NOT NULL,
	app     TEXT    NOT NULL,
	action  TEXT    NOT NULL,
	user_id TEXT    NOT NULL DEFAULT '',
	detail  TEXT    NOT NULL DEFAULT ''
);
