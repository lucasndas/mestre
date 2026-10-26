package sessionzero

import (
	"context"
	"errors"

	"github.com/mestre-ia/mestre/internal/github"
	"github.com/mestre-ia/mestre/internal/orchestration/workspaces"
)

// InstallationSource is GitHub's side of the stage apps (*github.Installs).
type InstallationSource interface {
	Missing(ctx context.Context, repo string, apps []string) ([]string, error)
	AddRepository(ctx context.Context, repo string, apps []string) error
	InstallLink(ctx context.Context, repo, app string) (string, error)
}

var _ InstallationSource = (*github.Installs)(nil)

// GitHubInstallations is the Installations of the stage apps over GitHub: the
// two fixed Apps, the Owner's personal credential for the API and the link of
// the installation when there is none.
type GitHubInstallations struct{ Source InstallationSource }

var _ Installations = GitHubInstallations{}

// the fixed Apps are asked for by role: their names carry the prefix of the Owner
var fixedApps = []string{github.RoleGrandMaster, github.RoleMestre}

// Missing implements Installations.
func (g GitHubInstallations) Missing(ctx context.Context, repo workspaces.Repo) ([]string, error) {
	return g.Source.Missing(ctx, repo.String(), fixedApps)
}

// AddRepository implements Installations: an Owner without a credential in use
// is ErrNoPersonalCredential, and the stage sends the link.
func (g GitHubInstallations) AddRepository(ctx context.Context, repo workspaces.Repo, apps []string) error {
	err := g.Source.AddRepository(ctx, repo.String(), apps)
	if errors.Is(err, github.ErrNoCredential) {
		return ErrNoPersonalCredential
	}
	return err
}

// InstallLink implements Installations.
func (g GitHubInstallations) InstallLink(ctx context.Context, repo workspaces.Repo, app string) (string, error) {
	return g.Source.InstallLink(ctx, repo.String(), app)
}
