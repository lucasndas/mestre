package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/mestre-ia/mestre/internal/classes"
)

// PersonalClients gives the client of a person acting as herself
// (*Personal): ErrNoCredential for one without a credential in use.
type PersonalClients interface {
	Client(ctx context.Context, userID string) (*REST, error)
}

var _ PersonalClients = (*Personal)(nil)

// Installs is GitHub's side of the stage `apps` of the session zero
// (docs/github/criacao-de-apps.md#repositórios-novos-e-permissões-novas): which
// repositories the installations of the fixed Apps cover, adding one with the
// personal credential of the Owner, or the link for the Owner to do it.
type Installs struct {
	Apps   *Registry
	Tokens *Tokens
	// Personal is the credentials of the users; Owner gives the id of the Owner.
	Personal PersonalClients
	Owner    func(ctx context.Context) (string, error)
	// API is the REST API, Web is github.com.
	API, Web string
	HTTP     *http.Client
}

// refresh reads the repositories of the installation of an App from GitHub and
// keeps them: the Owner may have ticked one on github.com. An App that is not
// installed yet has nothing to read.
func (i *Installs) refresh(ctx context.Context, app App) error {
	if !app.Ready() {
		return nil
	}
	inst, err := i.Apps.AnyInstallation(ctx, app.Name)
	if errors.Is(err, ErrNotInstalled) {
		return nil
	}
	if err != nil {
		return err
	}
	tok, err := i.Tokens.mint(ctx, app, inst, "", map[string]string{"metadata": "read"}, i.Tokens.Clock.Now())
	if err != nil {
		return fmt.Errorf("github: reading the installation of %q: %w", app.Name, err)
	}
	rest := &REST{BaseURL: i.API, HTTP: i.HTTP, Token: func(context.Context, string) (string, error) { return tok.Secret(), nil }}
	repos, err := rest.InstallationRepos(ctx)
	if err != nil {
		return fmt.Errorf("github: reading the repositories of %q: %w", app.Name, err)
	}
	return i.Apps.Installed(ctx, app.Name, inst, repos)
}

// Missing lists the Apps (by name) that do not cover the repository, after
// reading their installations again. An App is asked for by its name or, for the
// fixed ones, by its role.
func (i *Installs) Missing(ctx context.Context, repo string, apps []string) ([]string, error) {
	repo, err := NormalizeRepo(repo)
	if err != nil {
		return nil, err
	}
	var missing []string
	for _, name := range apps {
		var app App
		var err error
		if name == RoleGrandMaster || name == RoleMestre {
			app, err = i.Apps.Fixed(ctx, name) // a fixed App is asked for by its role
		} else {
			app, err = i.Apps.Get(ctx, name)
		}
		if errors.Is(err, ErrNoApp) {
			missing = append(missing, roleLabel(name))
			continue
		}
		if err != nil {
			return nil, err
		}
		if err := i.refresh(ctx, app); err != nil {
			return nil, err
		}
		if _, err := i.Apps.Installation(ctx, app.Name, repo); err != nil {
			if !errors.Is(err, ErrNotInstalled) {
				return nil, err
			}
			missing = append(missing, app.Name)
		}
	}
	return missing, nil
}

// roleLabel is how an App that was not made yet is named to the people.
func roleLabel(name string) string {
	switch name {
	case RoleGrandMaster:
		return "Grão-Mestre"
	case RoleMestre:
		return "Mestre"
	}
	return name
}

// AddRepository adds the repository to the installation of each App through
// the API, as the Owner (PUT /user/installations/{installation}/repositories/{repository}).
// The Owner without a credential in use is ErrNoCredential: the link is the way then.
func (i *Installs) AddRepository(ctx context.Context, repo string, apps []string) error {
	owner, err := i.Owner(ctx)
	if err != nil {
		return err
	}
	cl, err := i.Personal.Client(ctx, owner)
	if err != nil {
		return err
	}
	info, err := cl.Repo(ctx, repo)
	if err != nil {
		return fmt.Errorf("github: reading the repository as the Owner: %w", err)
	}
	var errs []error
	for _, name := range apps {
		inst, err := i.Apps.AnyInstallation(ctx, name)
		if err != nil { // never installed: only the link helps
			errs = append(errs, err)
			continue
		}
		if err := cl.AddRepoToInstallation(ctx, inst, info.ID); err != nil {
			errs = append(errs, fmt.Errorf("github: adding the repository to the installation of %q: %w", name, err))
		}
	}
	return errors.Join(errs...)
}

// InstallLink is the link the Owner opens: the settings of the installation
// when the App is installed somewhere (tick the repository), otherwise the page
// of the first installation.
func (i *Installs) InstallLink(ctx context.Context, _ string, app string) (string, error) {
	a, err := i.Apps.Get(ctx, app)
	if err != nil {
		return "", err
	}
	if a.State != classes.AppReady && a.State != classes.AppAwaitingInstall || a.Slug == "" {
		return "", fmt.Errorf("%w: %s (%s)", ErrAppNotReady, a.Name, a.State.Label())
	}
	web := strings.TrimRight(i.Web, "/")
	if inst, err := i.Apps.AnyInstallation(ctx, app); err == nil {
		return web + "/settings/installations/" + strconv.FormatInt(inst, 10), nil
	}
	return web + "/apps/" + url.PathEscape(a.Slug) + "/installations/new", nil
}

// AddRepoToInstallation adds a repository to an installation of an App, as the
// person whose token the client has.
func (c *REST) AddRepoToInstallation(ctx context.Context, installationID, repoID int64) error {
	path := fmt.Sprintf("/user/installations/%d/repositories/%d", installationID, repoID)
	st, hdr, raw, err := c.doAbs(ctx, http.MethodPut, "", path, nil)
	if err != nil {
		return err
	}
	if st != http.StatusNoContent && st != http.StatusOK {
		return failed("PUT "+path, st, raw, hdr)
	}
	return nil
}
