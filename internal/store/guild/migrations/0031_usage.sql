-- The use and the limits of use (docs/plataforma/limites-de-uso.md). Timestamps
-- are Unix milliseconds in UTC and 0 means "never". Every table has a declared
-- primary key, as the change log of the replication needs.

-- One row per piece of use of a character. origin is etapa, conformidade or
-- teste_de_classe: the last two carry no quest, table or workspace and count
-- only in the total of the day and in the use of the account. day is the date
-- (YYYY-MM-DD) in the time zone of the Guild when the row was made. cost_usd
-- is NULL when the provider gave none (Codex); has_tokens is 0 when it gave no
-- tokens: a limit in a unit the row has no data for does not count it.
-- active_ms and turns are always recorded.
CREATE TABLE usage_records (
	id                 INTEGER PRIMARY KEY AUTOINCREMENT,
	character_id       TEXT    NOT NULL DEFAULT '',
	step               TEXT    NOT NULL DEFAULT '',
	quest              TEXT    NOT NULL DEFAULT '',
	workspace          TEXT    NOT NULL DEFAULT '',
	table_id           TEXT    NOT NULL DEFAULT '',
	profile            TEXT    NOT NULL DEFAULT '',
	provider           TEXT    NOT NULL,
	model              TEXT    NOT NULL DEFAULT '',
	at                 INTEGER NOT NULL,
	day                TEXT    NOT NULL,
	source             TEXT    NOT NULL,
	origin             TEXT    NOT NULL,
	input_tokens       INTEGER NOT NULL DEFAULT 0,
	output_tokens      INTEGER NOT NULL DEFAULT 0,
	cache_read_tokens  INTEGER NOT NULL DEFAULT 0,
	cache_write_tokens INTEGER NOT NULL DEFAULT 0,
	reasoning_tokens   INTEGER NOT NULL DEFAULT 0,
	has_tokens         INTEGER NOT NULL DEFAULT 0,
	cost_usd           REAL,
	active_ms          INTEGER NOT NULL DEFAULT 0,
	turns              INTEGER NOT NULL DEFAULT 0,
	estimated          INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX usage_by_table ON usage_records (workspace, table_id);
CREATE INDEX usage_by_quest ON usage_records (quest);
CREATE INDEX usage_by_day ON usage_records (day, workspace);

-- The limit of a scope. scope is mission (key: the quest), table
-- (key: "<workspace>/<table>"), workspace (key: the workspace), day (key: the
-- workspace; per day) or day_total (key: ''; the whole installation, per
-- day). unit is usd, tokens or hours.
CREATE TABLE usage_limits (
	scope TEXT NOT NULL,
	key   TEXT NOT NULL,
	unit  TEXT NOT NULL,
	value REAL NOT NULL,
	PRIMARY KEY (scope, key)
);

-- What was approved above a limit, for one period (the date for the day
-- scopes, '' for the others).
CREATE TABLE usage_extras (
	scope  TEXT NOT NULL,
	key    TEXT NOT NULL,
	period TEXT NOT NULL,
	extra  REAL NOT NULL,
	PRIMARY KEY (scope, key, period)
);

-- The warnings already given: one per limit per period and level (80 or 100).
CREATE TABLE usage_alerts (
	scope  TEXT    NOT NULL,
	key    TEXT    NOT NULL,
	period TEXT    NOT NULL,
	level  INTEGER NOT NULL,
	at     INTEGER NOT NULL,
	PRIMARY KEY (scope, key, period, level)
);

-- The custo_extra requests the limits opened, by the request of the chain of
-- approval. state is open, approved or denied. paused lists (JSON) the
-- workspaces the total of the day paused.
CREATE TABLE usage_requests (
	request   INTEGER PRIMARY KEY,
	scope     TEXT    NOT NULL,
	key       TEXT    NOT NULL,
	period    TEXT    NOT NULL,
	unit      TEXT    NOT NULL,
	workspace TEXT    NOT NULL DEFAULT '',
	table_id  TEXT    NOT NULL DEFAULT '',
	used      REAL    NOT NULL,
	cap       REAL    NOT NULL,
	extra     REAL    NOT NULL,
	paused    TEXT    NOT NULL DEFAULT '[]',
	state     TEXT    NOT NULL,
	at        INTEGER NOT NULL
);

-- The windows of the plan of a provider, by account profile, as the last
-- reading gave them. notified_resets is the resets_at the Mestre da mesa was
-- told the start is held until (0: none); warned_read is the reading the
-- warning of an unconfirmed use was given for.
CREATE TABLE usage_windows (
	provider        TEXT    NOT NULL,
	profile         TEXT    NOT NULL,
	win             TEXT    NOT NULL,
	bucket          TEXT    NOT NULL DEFAULT '',
	used_pct        REAL    NOT NULL,
	resets_at       INTEGER NOT NULL DEFAULT 0,
	read_at         INTEGER NOT NULL,
	notified_resets INTEGER NOT NULL DEFAULT 0,
	warned_read     INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (provider, profile, win, bucket)
);

-- The start threshold of a workspace, in percent (absent: the default, 95).
CREATE TABLE usage_thresholds (
	workspace TEXT PRIMARY KEY,
	pct       REAL NOT NULL
);

-- A character waiting for the quota. until is when it comes back (0: not
-- known).
CREATE TABLE usage_waits (
	character_id TEXT    PRIMARY KEY,
	provider     TEXT    NOT NULL,
	profile      TEXT    NOT NULL,
	until        INTEGER NOT NULL DEFAULT 0,
	since        INTEGER NOT NULL
);

-- The counters of a source that gives totals (the cost of the statusLine of
-- Claude Code is the total of the session): the next reading is read as the
-- difference.
CREATE TABLE usage_cursors (
	character_id       TEXT PRIMARY KEY,
	session            TEXT NOT NULL DEFAULT '',
	input_tokens       INTEGER NOT NULL DEFAULT 0,
	output_tokens      INTEGER NOT NULL DEFAULT 0,
	cache_read_tokens  INTEGER NOT NULL DEFAULT 0,
	cache_write_tokens INTEGER NOT NULL DEFAULT 0,
	reasoning_tokens   INTEGER NOT NULL DEFAULT 0,
	cost_usd           REAL    NOT NULL DEFAULT 0
);
