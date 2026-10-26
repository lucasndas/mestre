package panel

import (
	"context"

	"github.com/mestre-ia/mestre/internal/orchestration/accounts"
	"github.com/mestre-ia/mestre/internal/orchestration/presets"
	"github.com/mestre-ia/mestre/internal/orchestration/usage"
	"github.com/mestre-ia/mestre/internal/providers"
	"github.com/mestre-ia/mestre/internal/users"
)

// ClassSource is what the classes screen needs of the libraries of classes of the
// Citadel. Classes lists the classes and the proposals of the workspaces the user
// acts in (all: every workspace); Approve is the "Aprovar" of a proposal, which the
// library checks again (the level, the hash, the ceiling) and whose errors are those
// of internal/classes.
type ClassSource interface {
	Classes(ctx context.Context, workspaces []string, all bool) ([]ClassRow, []ProposalRow, error)
	Approve(ctx context.Context, workspace string, proposal int64, hash string, by users.User) error
}

// PresetSource is the catalog of the bases and sheets (*presets.Catalog).
type PresetSource interface {
	Bases(ctx context.Context) ([]presets.Base, error)
	Sheets(ctx context.Context) ([]presets.Sheet, error)
	SaveBase(ctx context.Context, b presets.Base) error
	SaveSheet(ctx context.Context, s presets.Sheet) ([]string, error)
	DeleteBase(ctx context.Context, name string) error
	DeleteSheet(ctx context.Context, name string) error
	Export(ctx context.Context, sel presets.Selection) (*presets.Document, error)
	PlanImport(ctx context.Context, d *presets.Document) (*presets.Plan, error)
	ApplyImport(ctx context.Context, plan *presets.Plan, choices map[presets.Item]presets.Choice) (presets.Applied, error)
}

// ProfileSource is the account profiles of this machine (*accounts.Registry) and,
// its guided setup when the machine has the steps for it.
type ProfileSource interface {
	Profiles(ctx context.Context) ([]accounts.Profile, error)
	Add(ctx context.Context, name string, provider providers.ID) (accounts.Profile, error)
	Remove(ctx context.Context, name string) error
	SetDefault(ctx context.Context, name string) error
	// SetupAvailable says this machine can run the guided setup (it has the steps:
	// the installer of the CLI, the login terminal, the conformance test).
	SetupAvailable() bool
	// Advance goes as far as the setup of the profile can.
	Advance(ctx context.Context, name string) (accounts.Status, error)
	// Providers are the providers a profile may be made for.
	Providers() []string
}

// UsageLimits is the limits of the usage (*usage.Service).
type UsageLimits interface {
	Limits(ctx context.Context) ([]usage.Limit, error)
	View(ctx context.Context, l usage.Limit, workspace string) (usage.LimitView, error)
	Today(ctx context.Context) string
}

// Manage is the engine side of the screens of the administration that need the
// orchestration of the period (nil member: that screen answers it is not available).
type Manage struct {
	Classes  ClassSource
	Presets  PresetSource
	Profiles ProfileSource
	Usage    UsageLimits
}

// ManageProvider gives the Manage of now; the panel is made before the
// orchestration of the period, so it asks at each request.
type ManageProvider func() Manage

type manageHolder struct{ get ManageProvider }

// SetManage sets where the screens of the administration find the engine.
func (p *Panel) SetManage(get ManageProvider) { p.manage.Store(&manageHolder{get}) }

func (p *Panel) mg() Manage {
	if h := p.manage.Load(); h != nil && h.get != nil {
		return h.get()
	}
	return Manage{}
}
