package app

import (
	"context"
	"io/fs"
	"net/http"
	"strconv"

	"github.com/mestre-ia/mestre/internal/github"
	"github.com/mestre-ia/mestre/internal/panel"
	"github.com/mestre-ia/mestre/internal/store"
	"github.com/mestre-ia/mestre/internal/users"
)

// PanelSource is what an IntegrationRun may also be: the users and the way to
// tell the Owner, which the panel of the period is made with. Without it the
// Citadel has no panel (the engine of a test that runs no integration).
type PanelSource interface {
	PanelPieces() (*users.Service, users.Alerts)
}

// PanelAdminSource is what an IntegrationRun may also be, for the screens of the
// administration: the device flow of the personal GitHub credentials ("Conectar
// GitHub") and the vault of the period (the screen of the secrets). Either may be
// nil: its routes then answer that they are not available.
type PanelAdminSource interface {
	PanelAdmin() (panel.PersonalGitHub, panel.SecretVault)
}

// PanelCreationSource is what an IntegrationRun may also be: the creation of the Apps
// by link, whose pages the panel serves (nil: no link, the Apps screen shows the
// state only).
type PanelCreationSource interface {
	PanelCreation() *github.Creation
}

// mountPanel puts the panel on the engine port, on every path no other route
// has: the machine always answers there, and enginePlacement decides what
// (docs/painel/login.md#acesso-por-https).
func (e *Engine) mountPanel() {
	e.tls.Handle("/", panel.Gate(enginePlacement{e}, http.HandlerFunc(e.servePanel)))
}

func (e *Engine) servePanel(w http.ResponseWriter, r *http.Request) {
	if p := e.panel.Load(); p != nil {
		p.ServeHTTP(w, r)
		return
	}
	// the Citadel that acts, before its panel is up or while its period ends
	panel.WriteError(w, panel.Err(panel.CodeUnavailable, "o painel não está disponível agora", nil))
}

// openPanel makes the panel of the period over the main database and returns
// what ends it. Nil source: no panel.
func (e *Engine) openPanel(ctx context.Context, db *store.DB, integ IntegrationRun, assets fs.FS, run func(func()), vaultChanged func(context.Context)) func() {
	src, ok := integ.(PanelSource)
	if !ok {
		return func() {}
	}
	svc, alerts := src.PanelPieces()
	opts := panel.Options{DB: db, Clock: e.clk, Users: svc, Alerts: alerts, Assets: assets, Machine: e.id.MachineID, Logger: e.log, VaultChanged: vaultChanged, Mask: e.deps.Masker.Mask}
	if adm, ok := integ.(PanelAdminSource); ok {
		opts.GitHub, opts.Vault = adm.PanelAdmin()
	}
	if a, ok := integ.(ApprovalPanelSource); ok {
		opts.Approvals, opts.Notices = a.PanelApprovals()
	}
	if c, ok := integ.(ChatPanelSource); ok {
		opts.Chat = c.PanelChat()
	}
	if e.deps.PanelChat != nil {
		opts.Chat = e.deps.PanelChat
	}
	if c, ok := integ.(PanelCreationSource); ok {
		opts.Creation = c.PanelCreation()
	}
	p, err := panel.New(opts)
	if err != nil {
		e.log.Error("app: the panel could not be made; this period has none", "err", err)
		return func() {}
	}
	if rs, ok := integ.(PanelRulesSource); ok {
		p.SetRules(rs.PanelRules())
	}
	rel := newEngineRelay(e)
	if e.deps.PanelTerminals != nil {
		p.SetTerminals(e.deps.PanelTerminals)
	} else {
		p.SetTerminals(rel)
	}
	if e.deps.PanelCanvas != nil {
		p.SetCanvas(e.deps.PanelCanvas)
	} else {
		p.SetCanvas(canvasHost{get: e.orc.Load, clk: e.clk})
	}
	if e.deps.PanelView != nil {
		p.SetWorkspaceView(e.deps.PanelView)
	} else {
		p.SetWorkspaceView(screenedViewHost{viewHost: viewHost{get: e.orc.Load, clk: e.clk}, mask: e.deps.Masker.Mask})
	}
	p.SetManage(func() panel.Manage { return e.panelManage(db) })
	if e.deps.PanelWorkspaces != nil {
		p.SetWorkspaces(e.deps.PanelWorkspaces)
	} else if w, ok := integ.(WorkspacePanelSource); ok {
		p.SetWorkspaces(w.PanelWorkspaces())
	}
	if e.deps.PanelGuild != nil {
		p.SetGuild(e.deps.PanelGuild)
	} else {
		p.SetGuild(newGuildHost(e))
	}
	e.relay.Store(rel)
	e.panel.Store(p)
	run(func() { p.Run(ctx) })
	return func() {
		e.panel.Store(nil)
		e.relay.Store(nil)
		p.Close()
	}
}

// enginePlacement is where this machine stands for the panel.
type enginePlacement struct{ e *Engine }

// Place implements panel.Placement: the Citadel that acts serves the panel; a
// Citadel that does not (suspended, or handing over) and a Tower redirect.
func (p enginePlacement) Place() panel.Place {
	p.e.mu.Lock()
	run := p.e.cit
	p.e.mu.Unlock()
	switch {
	case run == nil:
		return panel.PlaceTower
	case run.fence.Acting() && !run.gate.isDraining():
		return panel.PlaceCitadel
	}
	return panel.PlaceSuspended
}

// CitadelURL implements panel.Placement: the address of the Citadel of now, which
// the machine knows when its certificate of epoch names another machine.
func (p enginePlacement) CitadelURL() string {
	p.e.mu.Lock()
	unknown := p.e.unknown
	p.e.mu.Unlock()
	id := p.e.epochs.Citadel()
	if unknown || id == "" || id == p.e.id.MachineID {
		return ""
	}
	return "https://mestre.local:" + strconv.Itoa(p.e.cfg.EnginePort)
}
