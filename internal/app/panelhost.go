package app

import (
	"context"
	"io/fs"
	"net/http"
	"strconv"

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
func (e *Engine) openPanel(ctx context.Context, db *store.DB, integ IntegrationRun, assets fs.FS, run func(func())) func() {
	src, ok := integ.(PanelSource)
	if !ok {
		return func() {}
	}
	svc, alerts := src.PanelPieces()
	p, err := panel.New(panel.Options{DB: db, Clock: e.clk, Users: svc, Alerts: alerts, Assets: assets, Machine: e.id.MachineID, Logger: e.log})
	if err != nil {
		e.log.Error("app: the panel could not be made; this period has none", "err", err)
		return func() {}
	}
	e.panel.Store(p)
	run(func() { p.Run(ctx) })
	return func() {
		e.panel.Store(nil)
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
