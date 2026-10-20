-- The approval on GitHub (docs/github/aprovacao-no-github.md). Timestamps are
-- Unix milliseconds in UTC. Every table has a declared primary key, as the
-- change log of the replication needs.

-- How a workspace gets its approvals applied on GitHub: 'app' (the App
-- "Grão-Mestre" reviews, the default and the absence of a row) or 'human' (the
-- review of the App does not count and the repository takes no exception for it:
-- every merge asks a person).
CREATE TABLE github_review_modes (
	workspace TEXT    PRIMARY KEY,
	mode      TEXT    NOT NULL,
	reason    TEXT    NOT NULL DEFAULT '',
	at        INTEGER NOT NULL
);

-- The personal credential of a user: what the engine knows of it besides the
-- tokens, which are only in the vault under secret. state is 'connected' or
-- 'expired' (the renewal was refused).
CREATE TABLE github_user_credentials (
	user_id        TEXT    PRIMARY KEY REFERENCES users (id),
	login          TEXT    NOT NULL,
	state          TEXT    NOT NULL,
	secret         TEXT    NOT NULL,
	expires_at     INTEGER NOT NULL,
	connected_at   INTEGER NOT NULL,
	updated_at     INTEGER NOT NULL
);

-- What the engine did with each review made on GitHub itself for a request
-- that waits for people: counted as the vote of the user, or ignored with the
-- reason. state is the state of the review the decision was made on, so a
-- review that is dismissed later is another row.
CREATE TABLE approval_github_reviews (
	request  INTEGER NOT NULL REFERENCES approval_requests (id),
	review   INTEGER NOT NULL,
	state    TEXT    NOT NULL,
	login    TEXT    NOT NULL,
	commit_id TEXT   NOT NULL DEFAULT '',
	user_id  TEXT    NOT NULL DEFAULT '',
	outcome  TEXT    NOT NULL,
	reason   TEXT    NOT NULL DEFAULT '',
	at       INTEGER NOT NULL,
	PRIMARY KEY (request, review, state)
);
