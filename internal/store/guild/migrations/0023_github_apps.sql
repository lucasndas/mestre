-- The GitHub Apps of the Mestre (docs/github/identidades-e-apps.md): the two
-- fixed ones (grand_master, mestre) and one for each class that asks for its
-- own. The private key of an App is in the vault, never here. state uses the
-- identifiers of docs/github/criacao-de-apps.md#estados-de-um-app. workflows is 1
-- when the App was made with the Workflows permission. Timestamps are Unix
-- milliseconds in UTC.
CREATE TABLE github_apps (
	name        TEXT    PRIMARY KEY,
	role        TEXT    NOT NULL,
	class       TEXT    NOT NULL DEFAULT '',
	slug        TEXT    NOT NULL DEFAULT '',
	app_id      INTEGER NOT NULL DEFAULT 0,
	client_id   TEXT    NOT NULL DEFAULT '',
	bot_user_id INTEGER NOT NULL DEFAULT 0,
	state       TEXT    NOT NULL,
	workflows   INTEGER NOT NULL DEFAULT 0,
	created_at  INTEGER NOT NULL,
	updated_at  INTEGER NOT NULL
);
CREATE UNIQUE INDEX github_apps_class ON github_apps (class) WHERE role = 'class';
CREATE UNIQUE INDEX github_apps_fixed ON github_apps (role) WHERE role <> 'class';

-- The repositories each App is installed in, and the installation.
CREATE TABLE app_installations (
	app             TEXT    NOT NULL REFERENCES github_apps (name),
	repo            TEXT    NOT NULL,
	installation_id INTEGER NOT NULL,
	PRIMARY KEY (app, repo)
);

-- The settings of the integration: the prefix of the names, the login of the
-- Owner on GitHub, the Apps of the account that are not the Mestre's (informed
-- by the Owner) and the repository of the lock.
CREATE TABLE github_settings (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);
