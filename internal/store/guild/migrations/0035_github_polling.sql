-- The periodic reading of GitHub (docs/github/eventos.md): the ETag and the
-- cursor (the greatest updated_at already processed) of each list of each
-- repository, and the keys of the changes already turned into events, so a page
-- read again, a restart or the same fact seen by two lists is one event.
-- Timestamps are Unix milliseconds in UTC.
CREATE TABLE github_poll_state (
	repo   TEXT    NOT NULL,
	list   TEXT    NOT NULL,
	etag   TEXT    NOT NULL DEFAULT '',
	cursor INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (repo, list)
);

-- The keys are kept for 30 days.
CREATE TABLE github_seen_events (
	key TEXT    PRIMARY KEY,
	at  INTEGER NOT NULL
);
CREATE INDEX github_seen_events_at ON github_seen_events (at);
