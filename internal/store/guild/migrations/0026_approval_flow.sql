-- The flow of approval over the people (docs/aprovacoes/fluxo.md): what the
-- question to the people needs to be answered, who answered what and where
-- each recipient read it. Timestamps are Unix milliseconds in UTC.

-- One row per request that was put to the people. approvals is the quorum,
-- require the JSON list of the users whose approval is required, min_level the
-- lowest level that answers and pin 1 when the approval asks the PIN after the
-- button. text is the question as the recipients read it, kept to edit it.
CREATE TABLE approval_flow (
	request    INTEGER PRIMARY KEY REFERENCES approval_requests (id),
	approvals  INTEGER NOT NULL,
	require    TEXT    NOT NULL DEFAULT '[]',
	min_level  TEXT    NOT NULL,
	pin        INTEGER NOT NULL DEFAULT 0,
	text       TEXT    NOT NULL DEFAULT '',
	asked_at   INTEGER NOT NULL
);

-- The answer of each user to a request: the first one is final, and only an
-- approval counts to the quorum.
CREATE TABLE approval_votes (
	request  INTEGER NOT NULL REFERENCES approval_requests (id),
	user_id  TEXT    NOT NULL,
	answer   TEXT    NOT NULL,
	counts   INTEGER NOT NULL DEFAULT 1,
	at       INTEGER NOT NULL,
	PRIMARY KEY (request, user_id)
);

-- Where each recipient read the question (the reference of the message of the
-- channel), to edit it when the request is decided, expires or changes.
CREATE TABLE approval_messages (
	request  INTEGER NOT NULL REFERENCES approval_requests (id),
	user_id  TEXT    NOT NULL,
	ref      TEXT    NOT NULL,
	PRIMARY KEY (request, user_id)
);

-- An approval that waits for the PIN of the user: the request of PIN of the
-- users (users.PINRequest) and the answer it will apply, once, if the PIN is right.
CREATE TABLE approval_pins (
	pin_request TEXT    PRIMARY KEY,
	request     INTEGER NOT NULL REFERENCES approval_requests (id),
	user_id     TEXT    NOT NULL,
	answer      TEXT    NOT NULL,
	reason      TEXT    NOT NULL DEFAULT '',
	instruction TEXT    NOT NULL DEFAULT '',
	pattern     TEXT    NOT NULL DEFAULT '',
	for_ms      INTEGER NOT NULL DEFAULT 0
);
