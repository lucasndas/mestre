package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/lucasndas/mestre/internal/gitx"
	"github.com/lucasndas/mestre/internal/orchestration/accounts"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/providers"
	"github.com/lucasndas/mestre/internal/sessionzero"
)

// mainCloneChecker is the CheckStart of the session zero for the Grand Master,
// which runs in the main clone of the workspace (the table ""): a provider
// whose files were not generated there does not start it
// (docs/orquestracao/grao-mestre.md, CA-GM-17). The worktrees of the tables
// are prepared by the tables themselves (the Citadel when the table opens, the
// Tower when it gets the first character), so for them the check passes.
func (e *Engine) mainCloneChecker() (characters.TableChecker, error) {
	binary := e.deps.Binary
	if binary == "" {
		var err error
		if binary, err = os.Executable(); err != nil {
			return nil, fmt.Errorf("app: the path of the binary: %w", err)
		}
	}
	reg := providers.NewRegistry(providers.Options{NoFlush: e.cfg.Synchronous == "OFF"})
	table, err := sessionzero.NewTable(sessionzero.TableOptions{Worktree: sessionzero.Worktree{Providers: reg, Binary: binary, OS: runtime.GOOS}})
	if err != nil {
		return nil, err
	}
	git, err := gitx.New(gitx.Options{Clock: e.clk, Masker: e.deps.Masker, Suite: os.Getenv("MESTRE_TEST_SUITE")})
	if err != nil {
		return nil, err
	}
	accs := &accounts.Registry{DB: e.mdb, Clock: e.clk, Root: filepath.Join(e.paths.Maquina, "contas"), OS: runtime.GOOS, Folders: accounts.OSFolders{}}
	return mainCloneOnly{files: characters.TableFiles{Table: table, Requests: func(ctx context.Context, workspace, tbl, dir string) (sessionzero.TableRequest, error) {
		repo, err := git.Open(ctx, dir)
		if err != nil {
			return sessionzero.TableRequest{}, err
		}
		req := sessionzero.TableRequest{Workspace: workspace, Table: tbl, Dir: dir, Repo: repo, RepoRoot: dir, ExcludeMestre: true}
		ps, err := accs.Profiles(ctx)
		if err != nil {
			return sessionzero.TableRequest{}, err
		}
		for _, p := range ps {
			req.Profiles = append(req.Profiles, sessionzero.Profile{Provider: p.Provider, Dir: p.Dir})
		}
		return req, nil
	}}}, nil
}

// mainCloneOnly runs the check for the table "" and lets the tables pass.
type mainCloneOnly struct{ files characters.TableFiles }

func (m mainCloneOnly) CheckStart(ctx context.Context, workspace, table, dir string, provider providers.ID) error {
	if table != "" {
		return nil
	}
	return m.files.CheckStart(ctx, workspace, table, dir, provider)
}
