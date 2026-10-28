package main

import (
	"context"
	"sync"
	"time"

	"github.com/mestre-ia/mestre/internal/panel"
	"github.com/mestre-ia/mestre/internal/users"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// The workspace view the smoke test of the browser opens: the Grand Master of the
// double, whose terminal is the fake agent's, and the table of the canvas double with a
// mission, a party and a wait. The engine behind it keeps its state in memory, so the
// panel, the socket and the screen are the real ones.
type fakeView struct {
	mu     sync.Mutex
	layout map[string]panel.CanvasItem
}

var _ panel.ViewSource = (*fakeView)(nil)

func newFakeView() *fakeView { return &fakeView{layout: map[string]panel.CanvasItem{}} }

func (f *fakeView) View(_ context.Context, ws string) (panel.ViewData, error) {
	if ws == lojaWorkspace {
		v := panel.ViewData{
			GrandMaster: &panel.ViewGrandMaster{ID: fakeCharacter, Name: "Grão-Mestre", State: "idle", Machine: "e2e", MachineName: "e2e", HasTerminal: true},
			Tables:      []panel.TableBlock{}, Layout: []panel.CanvasItem{},
		}
		for _, t := range lojaTables(clock.Real{}.Now()) {
			v.Tables = append(v.Tables, t.block)
		}
		return v, nil
	}
	if ws != fakeWorkspace {
		return panel.ViewData{Tables: []panel.TableBlock{}, Layout: []panel.CanvasItem{}}, nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	delivered := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	v := panel.ViewData{
		GrandMaster: &panel.ViewGrandMaster{ID: fakeCharacter, Name: "Grão-Mestre", State: "idle", Machine: "e2e", MachineName: "e2e", HasTerminal: true},
		Tables: []panel.TableBlock{{
			ID: fakeTable, Epic: 7, EpicTitle: "Login com Google", Branch: "epico/7-login", State: "working",
			Quest:        &panel.ViewQuest{ID: "q1", Name: "entrega", Issue: 8, State: "running", Step: "Dev"},
			Party:        []panel.ViewMachine{{Machine: "e2e", MachineName: "e2e", Characters: 3}},
			PartySize:    3,
			Waits:        []panel.ViewWait{},
			Criteria:     &panel.ViewCriteria{Total: 3, Proven: 2, Verified: 1},
			LastDelivery: &delivered,
		}, {
			// a table closed with its PR merged: the "Entregas recentes" of the Home
			ID: "mesa-entregue", Epic: 5, EpicTitle: "Página de contato", Branch: "epico/5-contato", State: "closed",
			Party: []panel.ViewMachine{}, Waits: []panel.ViewWait{}, LastDelivery: &delivered, PR: 10,
		}},
		Layout: []panel.CanvasItem{},
	}
	for _, it := range f.layout {
		v.Layout = append(v.Layout, it)
	}
	return v, nil
}

func (f *fakeView) SaveLayout(_ context.Context, _ string, items []panel.CanvasItem) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, it := range items {
		f.layout[it.Key] = it
	}
	return nil
}

func (f *fakeView) PauseTable(context.Context, users.User, string, string, string) (bool, error) {
	return true, nil
}
