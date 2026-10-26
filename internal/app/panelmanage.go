package app

import (
	"context"
	"path/filepath"
	"runtime"
	"slices"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/machinesheet"
	"github.com/mestre-ia/mestre/internal/orchestration/accounts"
	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/panel"
	"github.com/mestre-ia/mestre/internal/providers"
	"github.com/mestre-ia/mestre/internal/store"
	"github.com/mestre-ia/mestre/internal/users"
)

// panelManage is what the screens of classes, bases and sheets, account profiles and usage
// read and change: the orchestration of the period, asked at each request (the panel
// is made before it).
func (e *Engine) panelManage(db *store.DB) panel.Manage {
	var m panel.Manage
	if e.mdb != nil {
		m.Profiles = profilePanel{reg: &accounts.Registry{DB: e.mdb, Clock: e.clk, Root: filepath.Join(e.paths.Maquina, "contas"), OS: runtime.GOOS, Folders: accounts.OSFolders{}}}
	}
	orc := e.orc.Load()
	if orc == nil {
		return m
	}
	if orc.Libraries != nil {
		m.Classes = classPanel{orc: orc, db: db}
	}
	if cp, ok := orc.Presets.(characters.CatalogPresets); ok && cp.Catalog != nil {
		m.Presets = cp.Catalog
	}
	if orc.Usage != nil {
		m.Usage = orc.Usage
	}
	return m
}

// classPanel is panel.ClassSource over the libraries of the classes.
type classPanel struct {
	orc *Orchestration
	db  *store.DB
}

var _ panel.ClassSource = classPanel{}

// workspaces are the slugs the user sees.
func (c classPanel) workspaces(ctx context.Context, list []string, all bool) ([]string, error) {
	if !all {
		return slices.Clone(list), nil
	}
	ws, err := c.orc.Workspaces.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(ws))
	for _, w := range ws {
		out = append(out, w.Slug)
	}
	return out, nil
}

func scopeName(s classes.ClassScope) string { return string(s) }

// Classes lists the global classes once, then the workspace and table classes of each
// workspace, scanning the folders (a change made outside becomes a proposal, which is
// how the panel learns of it), and the proposals that wait.
func (c classPanel) Classes(ctx context.Context, list []string, all bool) ([]panel.ClassRow, []panel.ProposalRow, error) {
	slugs, err := c.workspaces(ctx, list, all)
	if err != nil {
		return nil, nil, err
	}
	var (
		rows  []panel.ClassRow
		props []panel.ProposalRow
		seen  = map[classes.ClassKey]bool{}
	)
	global := false
	for _, ws := range slugs {
		lib := c.orc.Libraries(ws)
		if lib == nil {
			continue
		}
		// the tables with classes of their own: the scan of a table adds its folder
		tbls := []string{""}
		if c.orc.Tables != nil {
			if ts, err := c.orc.Tables.List(ctx, ws); err == nil {
				for _, t := range ts {
					tbls = append(tbls, t.ID)
				}
			}
		}
		for _, tb := range tbls {
			entries, err := lib.Scan(ctx, tb)
			if err != nil {
				c.orc.log.Warn("app: the classes of a workspace were not scanned", "workspace", ws, "table", tb, "err", err)
				continue
			}
			for _, e := range entries {
				if seen[e.Key] || (e.Key.Scope == classes.ClassGlobal && global) || (tb != "" && e.Key.Scope != classes.ClassTable) {
					continue
				}
				seen[e.Key] = true
				row := panel.ClassRow{Name: e.Key.Name, Scope: scopeName(e.Key.Scope), Table: e.Key.Table, State: string(e.State), Version: e.Version,
					App: e.App, Problems: orStrings(e.Problems)}
				if e.Key.Scope != classes.ClassGlobal {
					row.Workspace = ws
				}
				if rep, err := lib.Check(ctx, e.Key.Name, e.Key.Table); err == nil && rep.App.Link != "" {
					row.App, row.AppState = rep.App.Link, rep.App.Label
				}
				rows = append(rows, row)
			}
		}
		pending, err := lib.Records.Pending(ctx, ws)
		if err != nil {
			return nil, nil, err
		}
		props = append(props, proposalRows(ws, pending)...)
		if !global {
			g, err := lib.Records.Pending(ctx, "")
			if err != nil {
				return nil, nil, err
			}
			props = append(props, proposalRows("", g)...)
			global = true
		}
	}
	return rows, props, nil
}

func orStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func proposalRows(ws string, ps []classes.Proposal) []panel.ProposalRow {
	var out []panel.ProposalRow
	for _, p := range ps {
		out = append(out, panel.ProposalRow{ID: p.ID, Workspace: ws, Name: p.Key.Name, Scope: scopeName(p.Key.Scope), Table: p.Key.Table, Origin: string(p.Origin),
			By: p.By, Hash: p.Hash, Problems: orStrings(p.Problems), Tested: false, CreatedAt: p.CreatedAt})
	}
	return out
}

// Approve is the "Aprovar" of an Administrator or above; the library checks the level,
// the hash, the problems of the package and the ceiling again. The proposal of a global
// class (workspace "") is approved through the library of any workspace.
func (c classPanel) Approve(ctx context.Context, workspace string, proposal int64, hash string, by users.User) error {
	lib := c.orc.Libraries(workspace)
	if lib == nil && workspace == "" && c.orc.Workspaces != nil {
		if ws, err := c.orc.Workspaces.List(ctx); err == nil {
			for _, w := range ws {
				if lib = c.orc.Libraries(w.Slug); lib != nil {
					break
				}
			}
		}
	}
	if lib == nil {
		return classes.ErrClassChanged
	}
	return lib.Approve(ctx, proposal, hash, classes.Author{Level: classLevelOf(by.Level), ID: by.ID})
}

// profilePanel is panel.ProfileSource over the account profiles of this machine. The
// guided setup needs the installer of the CLIs, the login terminal and the conformance
// test of the machine, which come with milestone 7: until then Advance says so.
type profilePanel struct{ reg *accounts.Registry }

var _ panel.ProfileSource = profilePanel{}

func (p profilePanel) Profiles(ctx context.Context) ([]accounts.Profile, error) {
	return p.reg.Profiles(ctx)
}

func (p profilePanel) Add(ctx context.Context, name string, provider providers.ID) (accounts.Profile, error) {
	return p.reg.Add(ctx, name, provider)
}
func (p profilePanel) Remove(ctx context.Context, name string) error { return p.reg.Remove(ctx, name) }
func (p profilePanel) SetDefault(ctx context.Context, name string) error {
	return p.reg.SetDefault(ctx, name)
}
func (profilePanel) SetupAvailable() bool { return false }
func (profilePanel) Advance(context.Context, string) (accounts.Status, error) {
	return accounts.Status{}, accounts.ErrNoProfile
}
func (profilePanel) Providers() []string { return machinesheet.KnownProviders() }
