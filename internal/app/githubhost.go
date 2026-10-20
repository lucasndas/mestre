package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/lucasndas/mestre/internal/github"
	"github.com/lucasndas/mestre/internal/gitx"
	"github.com/lucasndas/mestre/internal/orchestration/workspaces"
)

// The GitHub of the workspaces with the identity of the App "Grão-Mestre"
// (docs/github/identidades-e-apps.md): the lookup of a repository when a
// workspace is made and the credentials of its fetches (the epics and the pull
// requests of the tables are stdEpics, with the `gh epic`). Each call asks for
// a token of the installation limited to the one repository and to the
// permissions it uses.

// Permissions of each use of the App.
var (
	repoReadPermissions = map[string]string{"metadata": "read"}
	fetchPermissions    = map[string]string{"contents": "read", "metadata": "read"}
)

// grandMasterGitHub is workspaces.GitHub and workspaces.Credentials over the REST
// API of GitHub.
type grandMasterGitHub struct{ r *stdRun }

var (
	_ workspaces.GitHub      = grandMasterGitHub{}
	_ workspaces.Credentials = grandMasterGitHub{}
)

// api is the client of the App "Grão-Mestre", asking for tokens with the
// permissions given.
func (g grandMasterGitHub) api(ctx context.Context, perms map[string]string) (*github.REST, error) {
	id, err := github.Identities{Apps: &github.Registry{DB: g.r.env.DB}}.For(ctx, github.Who{Role: github.RoleGrandMaster})
	if err != nil {
		return nil, fmt.Errorf("the App Grão-Mestre: %w", err)
	}
	return &github.REST{BaseURL: g.r.s.GitHubBase, HTTP: g.r.s.HTTP, Quota: g.r.quota, App: id.App, Token: func(ctx context.Context, repo string) (string, error) {
		if g.r.closed.Load() {
			return "", errNoFence
		}
		tok, err := g.r.tokens.Installation(ctx, github.Request{App: id.App, Repo: repo, Permissions: perms})
		if err != nil {
			return "", err
		}
		return tok.Secret(), nil
	}}, nil
}

// Repository implements workspaces.GitHub.
func (g grandMasterGitHub) Repository(ctx context.Context, repo workspaces.Repo) (workspaces.RepoInfo, error) {
	api, err := g.api(ctx, repoReadPermissions)
	if err != nil {
		return workspaces.RepoInfo{}, err
	}
	full := repo.Owner + "/" + repo.Name
	// the token first: the REST client hides why it had none, and the reason matters (not installed,
	// not ready, the vault)
	if _, err := api.Token(ctx, full); errors.Is(err, github.ErrNotInstalled) {
		return workspaces.RepoInfo{}, fmt.Errorf("%w: %s (the App Grão-Mestre is not installed there)", workspaces.ErrInvalidRepo, full)
	} else if err != nil {
		return workspaces.RepoInfo{}, fmt.Errorf("the token of the App Grão-Mestre for %s: %w", full, err)
	}
	d, err := api.Repository(ctx, full)
	if errors.Is(err, github.ErrNotFound) {
		return workspaces.RepoInfo{}, fmt.Errorf("%w: %s/%s (it does not exist, or the App Grão-Mestre is not installed there)", workspaces.ErrInvalidRepo, repo.Owner, repo.Name)
	}
	if err != nil {
		return workspaces.RepoInfo{}, err
	}
	if d.CloneURL == "" { // GitHub always says it; the double of a test may not
		d.CloneURL = "https://github.com/" + d.FullName + ".git"
	}
	return workspaces.RepoInfo{FullName: d.FullName, CloneURL: d.CloneURL, DefaultBranch: d.DefaultBranch}, nil
}

// Auth implements workspaces.Credentials: the header git sends to GitHub.
func (g grandMasterGitHub) Auth(ctx context.Context, repo workspaces.Repo) (*gitx.Auth, error) {
	api, err := g.api(ctx, fetchPermissions)
	if err != nil {
		return nil, err
	}
	tok, err := api.Token(ctx, repo.Owner+"/"+repo.Name)
	if err != nil {
		return nil, err
	}
	a := gitx.BasicAuth("https://github.com/", "x-access-token", tok)
	return &a, nil
}
