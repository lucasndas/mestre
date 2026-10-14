-- The workspaces of the Guild (docs/orquestracao/workspaces.md): one row per
-- GitHub repository. repo is the name as GitHub spells it; repo_key its lower
-- case, which is what must be unique (a repository belongs to one workspace).
-- state is preparing, active or paused; stage is the stage of the session
-- zero a preparing workspace is in (apps, mirror, files, analysis,
-- approval_test) and '' once it is active. A stage that failed keeps the
-- code and the reason of the failure, how many times it failed and when the
-- engine tries it again (stage_failures is 0 when the stage did not fail).
-- The fetch columns are the health of the mirror: when it was last fetched,
-- since when it has been failing (0: it is not), the last error, and whether
-- the operational alert of the hour was sent (once per outage). The advance
-- columns are the same for the fast-forward of the main clone: since when it
-- has been held back, why, and whether the Grand Master was told. Timestamps
-- are Unix milliseconds in UTC and 0 means "never".
CREATE TABLE workspaces (
	slug                  TEXT    PRIMARY KEY,
	repo                  TEXT    NOT NULL,
	repo_key              TEXT    NOT NULL UNIQUE,
	clone_url             TEXT    NOT NULL,
	default_branch        TEXT    NOT NULL,
	state                 TEXT    NOT NULL,
	stage                 TEXT    NOT NULL,
	stage_error_code      TEXT    NOT NULL DEFAULT '',
	stage_error_reason    TEXT    NOT NULL DEFAULT '',
	stage_failures        INTEGER NOT NULL DEFAULT 0,
	stage_failed_at       INTEGER NOT NULL DEFAULT 0,
	stage_retry_at        INTEGER NOT NULL DEFAULT 0,
	created_at            INTEGER NOT NULL,
	updated_at            INTEGER NOT NULL,
	last_fetch_at         INTEGER NOT NULL DEFAULT 0,
	fetch_failing_since   INTEGER NOT NULL DEFAULT 0,
	fetch_error           TEXT    NOT NULL DEFAULT '',
	fetch_alerted         INTEGER NOT NULL DEFAULT 0,
	advance_blocked_since INTEGER NOT NULL DEFAULT 0,
	advance_reason        TEXT    NOT NULL DEFAULT '',
	advance_alerted       INTEGER NOT NULL DEFAULT 0
);
