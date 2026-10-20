-- The commit of a pull request the Grão-Mestre last looked at with `gh review`
-- (docs/cli/grao-mestre.md#mestre-gh): `gh approve` is for that commit and for
-- no other. Timestamps are Unix milliseconds in UTC. Every table has a declared
-- primary key, as the change log of the replication needs.
CREATE TABLE github_reviewed_heads (
	repo     TEXT    NOT NULL,
	pr       INTEGER NOT NULL,
	head_sha TEXT    NOT NULL,
	at       INTEGER NOT NULL,
	PRIMARY KEY (repo, pr)
);
