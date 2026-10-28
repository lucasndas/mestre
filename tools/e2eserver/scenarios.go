package main

import (
	"time"

	"github.com/mestre-ia/mestre/internal/panel"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// The Dev-1 of each table of `loja` (`<table>-dev`, with the terminal `term:<table>`) is in the state its table is in
// (the same fake agent behind it), so the terminal and the board can be seen in the states the
// smoke test cannot reach: working (typing asks to confirm), waiting for a decision, stuck,
// without signal, ended. The table of the smoke test keeps its Dev-1 (`e2e-dev`).
type scenario struct {
	// state and reason are the ones of the character on the canvas and in the terminal.
	state, reason string
	// busy asks the confirmation to type; noSignal puts the machine out of reach.
	busy, noSignal bool
}

var scenarios = map[string]scenario{
	"loja-trabalhando": {state: "busy", busy: true},
	"loja-esperando":   {state: "blocked", reason: "quota"},
	"loja-decisao":     {state: "waiting", reason: "approval"},
	"loja-travada":     {state: "stuck"},
	"loja-parada":      {state: "busy", busy: true, noSignal: true},
	"loja-revisao":     {state: "idle"},
	"loja-entregue":    {state: "stopped"},
	"loja-pausada":     {state: "idle"},
}

// apply puts the scenario in what the terminal says of itself.
func (sc scenario) apply(info *panel.TerminalInfo) {
	info.State, info.Busy, info.Signal = sc.state, sc.busy, !sc.noSignal
	if sc.noSignal {
		since := clock.Real{}.Now().Add(-12 * time.Minute)
		info.NoSignalSince = &since
	}
}
