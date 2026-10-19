-- The blocks of the body of the PR of a table (<!-- mestre:bloco=name -->…): which
-- class made each one. Only that class (or the Mestre da mesa) rewrites it
-- (docs/cli/coordenacao-github.md, `mestre pr update --block`).
CREATE TABLE pr_blocks (
	repo  TEXT NOT NULL,
	pr    INTEGER NOT NULL,
	name  TEXT NOT NULL,
	class TEXT NOT NULL,
	PRIMARY KEY (repo, pr, name)
);
