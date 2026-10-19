package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/lucasndas/mestre/internal/gitx"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/presets"
	"github.com/lucasndas/mestre/internal/orchestration/tables"
	"github.com/lucasndas/mestre/internal/orchestration/workspaces"
	"github.com/lucasndas/mestre/internal/privdir"
	"github.com/lucasndas/mestre/internal/providers"
	"github.com/lucasndas/mestre/internal/sessionzero"
	"github.com/lucasndas/mestre/internal/store"
)

// The Citadel hosts the orchestration (docs/desenvolvimento/ordem-de-implementacao.md,
// "M5, antes da etapa 1"): the services of the workspaces, the tables, the
// characters, the inbox, the waits, the scrolls, the quests and the rituals
// are built over the main database of each period of the Citadel, their
// commands are put in the local channel and their loops run while the fence
// lets the Citadel act. What still depends on the stages of milestone 5 (the
// GitHub, the session zero of a character with its identities and decrees,
// the bot) is behind interfaces that refuse with the reason, never behind a
// double that pretends.

// errMilestone5 says why a piece refuses: its stage of milestone 5 is not built.
func errMilestone5(what string) error {
	return fmt.Errorf("%s is not available yet: it arrives with its stage of milestone 5 (docs/desenvolvimento/ordem-de-implementacao.md)", what)
}

// unavailableGitHub is the GitHub of a Citadel before its stage: every call
// refuses with the reason.
type unavailableGitHub struct{}

func (unavailableGitHub) Repository(context.Context, workspaces.Repo) (workspaces.RepoInfo, error) {
	return workspaces.RepoInfo{}, errMilestone5("the access to GitHub")
}

// unavailableEpics are the epics and pull requests of GitHub before their stage.
type unavailableEpics struct{}

func (unavailableEpics) Epic(context.Context, string, int) (tables.Epic, error) {
	return tables.Epic{}, errMilestone5("the access to the epics of GitHub")
}
func (unavailableEpics) CloseEpic(context.Context, string, int) error {
	return errMilestone5("the access to the epics of GitHub")
}
func (unavailableEpics) ClosePR(context.Context, string, int) error {
	return errMilestone5("the access to the pull requests of GitHub")
}

// unavailableBuilder is the session zero of a character before the stage of
// the identities and the decrees: no character is born through it.
type unavailableBuilder struct{}

func (unavailableBuilder) Build(context.Context, characters.BuildInput) (sessionzero.CharacterRequest, error) {
	return sessionzero.CharacterRequest{}, errMilestone5("the session zero of a character (identities and decrees)")
}

// logAlerts are the operational alerts before the bot: they go to the log, where
// the Owner reads them, and are not lost.
type logAlerts struct{ log *slog.Logger }

func (a logAlerts) Operational(_ context.Context, al workspaces.Alert) error {
	a.log.Warn("app: operational alert", "workspace", al.Workspace, "kind", string(al.Kind), "detail", al.Detail, "since", al.Since)
	return nil
}

// hostOrchestration builds the orchestration of a period of the Citadel over its
// main database.
func (e *Engine) hostOrchestration(db *store.DB) (*Orchestration, error) {
	if e.paths.Workspaces == "" || e.paths.Maquina == "" {
		return nil, errors.New("app: no folder for the workspaces and the machine")
	}
	git, err := gitx.New(gitx.Options{Clock: e.clk, Masker: e.deps.Masker, Suite: os.Getenv("MESTRE_TEST_SUITE")})
	if err != nil {
		return nil, err
	}
	reg := providers.NewRegistry(providers.Options{})
	binary := e.deps.Binary
	if binary == "" {
		if binary, err = os.Executable(); err != nil {
			return nil, fmt.Errorf("app: the path of the binary: %w", err)
		}
	}
	files, err := sessionzero.NewTable(sessionzero.TableOptions{Worktree: sessionzero.Worktree{Providers: reg, Binary: binary}})
	if err != nil {
		return nil, err
	}
	workspacesDir, err := filepath.Abs(e.paths.Workspaces)
	if err != nil {
		return nil, err
	}
	tmp := filepath.Join(e.paths.Maquina, "tmp")
	if err := privdir.Ensure(tmp); err != nil {
		return nil, err
	}
	return NewOrchestration(OrchestrationOptions{
		Guild: db, Machine: e.id.MachineID, Clock: e.clk, Logger: e.log, WorkspacesDir: workspacesDir, TempDir: tmp,
		Git: git, Machines: characters.MachineSet{}, Presets: characters.CatalogPresets{Catalog: &presets.Catalog{DB: db, Clock: e.clk}},
		Builder: unavailableBuilder{}, GitHub: unavailableGitHub{}, Epics: unavailableEpics{}, Alerts: logAlerts{e.log}, Files: files,
		Limits: characters.DefaultLimits(),
	})
}
