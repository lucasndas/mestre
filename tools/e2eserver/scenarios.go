package main

import (
	"strings"
	"time"

	"github.com/mestre-ia/mestre/internal/panel"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// The Dev-1 of each table of `loja` is in the state its table is in, with a terminal of its own
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

const (
	devPrefix  = "dev-"
	termPrefix = "term-"
)

// devOf is the id of the Dev-1 on the canvas of a table.
func devOf(ws, table string) string {
	if _, ok := scenarios[table]; ok && ws == lojaWorkspace {
		return devPrefix + table
	}
	return fakeCharacter
}

// scenarioOf finds the scenario of a character id (the table is what follows the prefix).
func scenarioOf(id string) (string, scenario, bool) {
	table, ok := strings.CutPrefix(id, devPrefix)
	if !ok {
		return "", scenario{}, false
	}
	sc, ok := scenarios[table]
	return table, sc, ok
}

// infoOf is what the terminal of a scenario says of itself.
func infoOf(table string, sc scenario) panel.TerminalInfo {
	info := panel.TerminalInfo{
		TerminalID: termPrefix + table, Character: devPrefix + table, Name: "Dev-1", Machine: "e2e",
		Workspace: lojaWorkspace, Table: table, State: sc.state, Busy: sc.busy, Signal: !sc.noSignal,
	}
	if sc.noSignal {
		since := clock.Real{}.Now().Add(-12 * time.Minute)
		info.NoSignalSince = &since
	}
	return info
}
