package github

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/store"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// The limits of an account (docs/github/identidades-e-apps.md#limite-de-100-apps).
const (
	// AppLimit is how many Apps an account may register.
	AppLimit = 100
	// AppWarning is the count from which the Owner is warned.
	AppWarning = 90
)

// The errors.
var (
	ErrAppLimit     = errors.New("github: the account has the 100 Apps it may register")
	ErrNoApp        = errors.New("github: no such App")
	ErrNameTaken    = errors.New("github: an App with that name is already registered")
	ErrNotInstalled = errors.New("github: the App is not installed in the repository")
)

// App is an App of the Mestre.
type App struct {
	// Name is the name on GitHub; Slug is how GitHub normalized it (the bot is
	// `<slug>[bot]`).
	Name, Slug string
	Role       string
	// Class is the class the App was asked for (Role "class").
	Class     string
	AppID     int64
	ClientID  string
	BotUserID int64
	State     classes.AppState
	// Workflows says the App was made with the Workflows permission.
	Workflows bool
}

// Ready says the App can act.
func (a App) Ready() bool { return a.State == classes.AppReady }

// BotLogin is `<slug>[bot]`, the name and the login of the author of its commits.
func (a App) BotLogin() string { return a.Slug + "[bot]" }

// BotEmail is the e-mail that links a commit to the bot:
// `<bot_user_id>+<slug>[bot]@users.noreply.github.com`.
func (a App) BotEmail() string {
	return fmt.Sprintf("%d+%s[bot]@users.noreply.github.com", a.BotUserID, a.Slug)
}

// Registry keeps the Apps in the Guild database.
type Registry struct {
	DB    *store.DB
	Clock clock.Clock
}

const appColumns = `name, role, class, slug, app_id, client_id, bot_user_id, state, workflows`

func scanApp(sc interface{ Scan(...any) error }) (App, error) {
	var a App
	var state string
	var wf int
	if err := sc.Scan(&a.Name, &a.Role, &a.Class, &a.Slug, &a.AppID, &a.ClientID, &a.BotUserID, &state, &wf); err != nil {
		return App{}, err
	}
	a.State, a.Workflows = classes.AppState(state), wf != 0
	return a, nil
}

// Get returns an App by its name.
func (r *Registry) Get(ctx context.Context, name string) (App, error) {
	a, err := scanApp(r.DB.Reader().QueryRowContext(ctx, `SELECT `+appColumns+` FROM github_apps WHERE name = ?`, name))
	if errors.Is(err, sql.ErrNoRows) {
		return App{}, fmt.Errorf("%w: %s", ErrNoApp, name)
	}
	return a, err
}

// BySlug returns an App by the slug GitHub gave it.
func (r *Registry) BySlug(ctx context.Context, slug string) (App, error) {
	a, err := scanApp(r.DB.Reader().QueryRowContext(ctx, `SELECT `+appColumns+` FROM github_apps WHERE slug = ? AND slug <> ''`, slug))
	if errors.Is(err, sql.ErrNoRows) {
		return App{}, fmt.Errorf("%w: %s", ErrNoApp, slug)
	}
	return a, err
}

// ForClass returns the App a class asked for.
func (r *Registry) ForClass(ctx context.Context, class string) (App, error) {
	a, err := scanApp(r.DB.Reader().QueryRowContext(ctx, `SELECT `+appColumns+` FROM github_apps WHERE role = 'class' AND class = ?`, class))
	if errors.Is(err, sql.ErrNoRows) {
		return App{}, fmt.Errorf("%w: class %s", ErrNoApp, class)
	}
	return a, err
}

// Fixed returns the App of a fixed role (RoleGrandMaster or RoleMestre).
func (r *Registry) Fixed(ctx context.Context, role string) (App, error) {
	a, err := scanApp(r.DB.Reader().QueryRowContext(ctx, `SELECT `+appColumns+` FROM github_apps WHERE role = ?`, role))
	if errors.Is(err, sql.ErrNoRows) {
		return App{}, fmt.Errorf("%w: %s", ErrNoApp, role)
	}
	return a, err
}

// List returns every App.
func (r *Registry) List(ctx context.Context) ([]App, error) {
	rows, err := r.DB.Reader().QueryContext(ctx, `SELECT `+appColumns+` FROM github_apps ORDER BY created_at, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []App
	for rows.Next() {
		a, err := scanApp(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// Taken says whether a name is used by an App of the Mestre.
func (r *Registry) Taken(ctx context.Context, name string) bool {
	var n int
	_ = r.DB.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM github_apps WHERE name = ?`, name).Scan(&n)
	return n > 0
}

// Repos are the repositories an App is installed in.
func (r *Registry) Repos(ctx context.Context, name string) ([]string, error) {
	rows, err := r.DB.Reader().QueryContext(ctx, `SELECT repo FROM app_installations WHERE app = ? ORDER BY repo`, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var repo string
		if err := rows.Scan(&repo); err != nil {
			return nil, err
		}
		out = append(out, repo)
	}
	return out, rows.Err()
}

// OpenRequest says whether the creation of an App has a request that is still
// open (a link that was not used or a code under exchange).
func (r *Registry) OpenRequest(ctx context.Context, name string) (bool, error) {
	var n int
	err := r.DB.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM github_app_requests WHERE app = ? AND status IN ('link', 'opened', 'exchanging')`, name).Scan(&n)
	return n > 0, err
}

// Others is how many Apps of the account are not the Mestre's, as the Owner
// informed (the Mestre does not read the page of Apps of the account).
func (r *Registry) Others(ctx context.Context) (int, error) {
	var v string
	err := r.DB.Reader().QueryRowContext(ctx, `SELECT value FROM github_settings WHERE key = 'others_count'`).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(v)
}

// SetOthers records the Apps of the account that are not the Mestre's.
func (r *Registry) SetOthers(ctx context.Context, n int) error {
	if n < 0 {
		return errors.New("github: a count of Apps is not negative")
	}
	return r.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO github_settings (key, value) VALUES ('others_count', ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value`, strconv.Itoa(n))
		return err
	})
}

// Count is how many Apps the account has: the Mestre's and the others.
func (r *Registry) Count(ctx context.Context) (int, error) {
	others, err := r.Others(ctx)
	if err != nil {
		return 0, err
	}
	var mine int
	if err := r.DB.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM github_apps`).Scan(&mine); err != nil {
		return 0, err
	}
	return mine + others, nil
}

// NearLimit says the account is at 90 Apps or more: the panel and the Owner are warned.
func (r *Registry) NearLimit(ctx context.Context) (bool, error) {
	n, err := r.Count(ctx)
	return n >= AppWarning, err
}

// Request registers an App that was asked for, in `aguardando_criacao`. At 100 Apps
// the creation is refused (the Grand Master binds the class to an existing App),
// and the name must be free.
func (r *Registry) Request(ctx context.Context, a App) (App, error) {
	a.State = classes.AppAwaitingCreation
	now := r.Clock.Now().UnixMilli()
	err := r.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		var mine, others int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM github_apps`).Scan(&mine); err != nil {
			return err
		}
		var v string
		switch err := tx.QueryRowContext(ctx, `SELECT value FROM github_settings WHERE key = 'others_count'`).Scan(&v); {
		case err == nil:
			others, _ = strconv.Atoi(v)
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}
		if mine+others >= AppLimit {
			return ErrAppLimit
		}
		wf := 0
		if a.Workflows {
			wf = 1
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO github_apps (name, role, class, state, workflows, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			a.Name, a.Role, a.Class, string(a.State), wf, now, now)
		if err != nil && isUnique(err) {
			return fmt.Errorf("%w: %s", ErrNameTaken, a.Name)
		}
		return err
	})
	if err != nil {
		return App{}, err
	}
	return r.Get(ctx, a.Name)
}

// Created records the App after the code of the manifest was exchanged: the
// slug, the ids, and the state `aguardando_instalacao` (the key went to the vault).
func (r *Registry) Created(ctx context.Context, name, slug string, appID int64, clientID string) error {
	return r.update(ctx, name, `slug = ?, app_id = ?, client_id = ?, state = ?`, slug, appID, clientID, string(classes.AppAwaitingInstall))
}

// BotID records the id of the bot of the App (`GET /users/<slug>[bot]`), read once.
func (r *Registry) BotID(ctx context.Context, name string, id int64) error {
	return r.update(ctx, name, `bot_user_id = ?`, id)
}

// SetState moves an App to another state. `pronto` is not set here: only
// Installed makes an App ready, after the installation was checked.
func (r *Registry) SetState(ctx context.Context, name string, s classes.AppState) error {
	if s == classes.AppReady {
		return errors.New("github: an App becomes ready by its installation (Installed)")
	}
	return r.update(ctx, name, `state = ?`, string(s))
}

func (r *Registry) update(ctx context.Context, name, set string, args ...any) error {
	args = append(args, r.Clock.Now().UnixMilli(), name)
	return r.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE github_apps SET `+set+`, updated_at = ? WHERE name = ?`, args...)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return fmt.Errorf("%w: %s", ErrNoApp, name)
		}
		return nil
	})
}

// Installed records the repositories the App is installed in and makes it
// `pronto`.
func (r *Registry) Installed(ctx context.Context, name string, installationID int64, repos []string) error {
	return r.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		var state string
		var appID int64
		if err := tx.QueryRowContext(ctx, `SELECT state, app_id FROM github_apps WHERE name = ?`, name).Scan(&state, &appID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("%w: %s", ErrNoApp, name)
			}
			return err
		}
		switch classes.AppState(state) {
		case classes.AppAwaitingInstall, classes.AppReady, classes.AppWithError:
		default: // an App that was not created has nothing to install
			return fmt.Errorf("github: the App %q is %s and can not be installed", name, state)
		}
		if appID == 0 {
			return fmt.Errorf("github: the App %q was not created yet", name)
		}
		// the installation is the list now: what it no longer covers stops counting
		if _, err := tx.ExecContext(ctx, `DELETE FROM app_installations WHERE app = ?`, name); err != nil {
			return err
		}
		for _, repo := range repos {
			repo = strings.ToLower(repo) // GitHub spells the name its way; the engine looks it up in lower case
			if _, err := tx.ExecContext(ctx, `INSERT INTO app_installations (app, repo, installation_id) VALUES (?, ?, ?)
				ON CONFLICT (app, repo) DO UPDATE SET installation_id = excluded.installation_id`, name, repo, installationID); err != nil {
				return err
			}
		}
		res, err := tx.ExecContext(ctx, `UPDATE github_apps SET state = ?, updated_at = ? WHERE name = ?`, string(classes.AppReady), r.Clock.Now().UnixMilli(), name)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return fmt.Errorf("%w: %s", ErrNoApp, name)
		}
		return nil
	})
}

// Installation is the installation of the App that covers the repository
// (`owner/name`, in any case: GitHub spells it one way and the requests are
// made in lower case); an App that is not installed there is ErrNotInstalled.
func (r *Registry) Installation(ctx context.Context, app, repo string) (int64, error) {
	var id int64
	err := r.DB.Reader().QueryRowContext(ctx, `SELECT installation_id FROM app_installations WHERE app = ? AND lower(repo) = lower(?)`, app, repo).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("%w: App `%s` não instalado em `%s`", ErrNotInstalled, app, repo)
	}
	return id, err
}

// AnyInstallation is an installation of the App, for what does not name a
// repository.
func (r *Registry) AnyInstallation(ctx context.Context, app string) (int64, error) {
	var id int64
	err := r.DB.Reader().QueryRowContext(ctx, `SELECT installation_id FROM app_installations WHERE app = ? ORDER BY repo LIMIT 1`, app).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("%w: App `%s` não está instalado em nenhum repositório", ErrNotInstalled, app)
	}
	return id, err
}

func isUnique(err error) bool {
	m := strings.ToLower(err.Error())
	return strings.Contains(m, "unique") || strings.Contains(m, "constraint")
}

// App implements classes.AppDirectory: the state of the App with that slug (or name).
func (r *Registry) App(ctx context.Context, slug string) (classes.AppState, bool, error) {
	a, err := r.BySlug(ctx, slug)
	if errors.Is(err, ErrNoApp) {
		if a, err = r.Get(ctx, slug); errors.Is(err, ErrNoApp) {
			return classes.AppUnknown, false, nil
		}
	}
	if err != nil {
		return classes.AppUnknown, false, err
	}
	return a.State, true, nil
}

// Requested implements classes.AppDirectory: the state of the App a class with
// `app: novo` asked for.
func (r *Registry) Requested(ctx context.Context, class string) (classes.AppState, bool, error) {
	a, err := r.ForClass(ctx, class)
	if errors.Is(err, ErrNoApp) {
		return classes.AppUnknown, false, nil
	}
	if err != nil {
		return classes.AppUnknown, false, err
	}
	return a.State, true, nil
}

var _ classes.AppDirectory = (*Registry)(nil)

// SetLair records the repository of the lock once the engine confirmed that the
// App "Grão-Mestre" reaches it.
func (r *Registry) SetLair(ctx context.Context, full string) error {
	return r.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO github_settings (key, value) VALUES ('lair_repo', ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value`, full)
		return err
	})
}

// LairRepo is the repository of the lock, if the engine already confirmed the
// access to it; without it no promotion of Citadel is possible.
func (r *Registry) LairRepo(ctx context.Context) (string, bool, error) {
	var v string
	err := r.DB.Reader().QueryRowContext(ctx, `SELECT value FROM github_settings WHERE key = 'lair_repo'`).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return v, err == nil, err
}
