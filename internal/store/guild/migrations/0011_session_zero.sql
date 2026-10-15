-- The session zero of the workspaces (docs/sessao-zero/workspace.md): what
-- it decided and what it still waits for, one row per workspace. The stage
-- of the workspace itself is in workspaces; here are the questions that do
-- not hold the workspace in preparing and the result of the approval test.
--
-- versioning: '' while the users have not answered (.mestre/ stays local),
-- then 'local' or 'versioned'. apps_notified_at is when the owner was sent
-- the link to install the fixed Apps (0: not yet). agents_md_state is ''
-- (not looked at), 'present' (the project has an AGENTS.md, never touched),
-- 'proposed' (the proposal went to the users: the full text and the id of
-- the approval request are kept), 'approved' or 'rejected'. pr_url is the
-- pull request of the session zero (empty: none yet).
-- approval_test_result is '' (not run), 'sem_exigencia', 'conta' or
-- 'nao_conta'; the summary says which rules were read.
-- Timestamps are Unix milliseconds in UTC and 0 means "never".
CREATE TABLE session_zero (
	slug                  TEXT    PRIMARY KEY REFERENCES workspaces (slug),
	versioning            TEXT    NOT NULL DEFAULT '',
	versioning_asked_at   INTEGER NOT NULL DEFAULT 0,
	versioning_answered_at INTEGER NOT NULL DEFAULT 0,
	apps_notified_at      INTEGER NOT NULL DEFAULT 0,
	agents_md_state       TEXT    NOT NULL DEFAULT '',
	agents_md_request     TEXT    NOT NULL DEFAULT '',
	agents_md_text        TEXT    NOT NULL DEFAULT '',
	claude_md_warned_at   INTEGER NOT NULL DEFAULT 0,
	pr_url                TEXT    NOT NULL DEFAULT '',
	approval_test_result  TEXT    NOT NULL DEFAULT '',
	approval_test_at      INTEGER NOT NULL DEFAULT 0,
	approval_test_summary TEXT    NOT NULL DEFAULT '',
	updated_at            INTEGER NOT NULL
);
