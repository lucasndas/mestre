package main

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/mestre-ia/mestre/internal/panel"
	"github.com/mestre-ia/mestre/internal/users"
)

// The table the smoke test of the browser opens: a Master, the Dev-1 whose terminal is
// the fake agent's, and a Reviewer, with a scroll, a mission under way and a wait. The
// engine behind it is a double that keeps its state in memory and refuses what the real
// one refuses (the automatic connection), so the panel, the socket and the canvas are the real ones.
const (
	fakeWorkspace = "e2e"
	fakeTable     = "mesa-e2e"
)

type fakeCanvas struct {
	mu     sync.Mutex
	layout map[string]panel.CanvasItem
	conns  []panel.CanvasConnection
	pinned map[string]bool
	scroll panel.CanvasScroll
}

var _ panel.CanvasSource = (*fakeCanvas)(nil)

func newFakeCanvas() *fakeCanvas {
	c := func(id string) panel.CanvasRef { return panel.CanvasRef{Kind: "character", ID: id} }
	return &fakeCanvas{
		layout: map[string]panel.CanvasItem{},
		pinned: map[string]bool{},
		conns: []panel.CanvasConnection{
			{A: c("e2e-master"), B: c("e2e-dev"), Reasons: []string{"mesa"}},
			{A: c("e2e-master"), B: c("e2e-rev"), Reasons: []string{"mesa"}},
			{A: c("e2e-dev"), B: c("e2e-rev"), Reasons: []string{"manual"}, Manual: true},
		},
		scroll: panel.CanvasScroll{ID: "e2e/p0001", Name: "Plano", Content: "# Plano\n\n- fazer o login\n", Revision: 1, Author: "Mestre"},
	}
}

func (f *fakeCanvas) State(_ context.Context, ws, table string) (panel.CanvasState, error) {
	tableState := "working"
	switch {
	case ws == fakeWorkspace && table == fakeTable:
	case ws == lojaWorkspace && lojaState(table) != "":
		tableState = lojaState(table)
	default:
		return panel.CanvasState{}, panel.ErrCanvasNotFound
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	char := func(id, name, class, role string, terminal bool) panel.CanvasCharacter {
		return panel.CanvasCharacter{ID: id, Name: name, Class: class, Kind: "character", Role: role, Machine: "e2e", MachineName: "e2e", State: "idle", Pinned: f.pinned[id], HasTerminal: terminal}
	}
	dev := char("e2e-dev", "Dev-1", "Dev", "character", true)
	dev.State = "busy"
	rev := char("e2e-rev", "Revisor", "Revisor", "character", false)
	rev.State, rev.Reason = "waiting", "Dev-1"
	st := panel.CanvasState{
		Workspace: ws, Table: table, TableState: tableState,
		Characters:  []panel.CanvasCharacter{char("e2e-master", "Mestre", "", "table_master", false), dev, rev},
		Scrolls:     []panel.CanvasScroll{f.scroll},
		Tomes:       []panel.CanvasTome{},
		Connections: slices.Clone(f.conns),
		Quests: []panel.CanvasQuest{{ID: "q1", Name: "entrega", State: "running", Round: 1, Steps: []panel.CanvasStep{
			{ID: "dev", Title: "Dev", State: "running", Character: "e2e-dev", Current: true},
			{ID: "rev", Title: "Revisão", State: "pending", Character: "e2e-rev", After: []string{"dev"}, ReturnTo: "dev"},
		}}},
		Waits:     []panel.CanvasWait{{ID: "w1", Waiter: "e2e-rev", Target: "e2e-dev", Kind: "step", Reason: "Dev-1"}},
		Deadlocks: []panel.CanvasDeadlock{},
	}
	// the places: the ones the person set, else a row
	for i, k := range []string{"character:e2e-master", "character:e2e-dev", "character:e2e-rev", "scroll:e2e/p0001"} {
		it, ok := f.layout[k]
		if !ok {
			it = panel.CanvasItem{Key: k, X: float64(i * 820), Y: 0}
		}
		st.Layout = append(st.Layout, it)
	}
	for _, it := range f.layout {
		if it.Key[:6] == "group:" {
			st.Layout = append(st.Layout, it)
		}
	}
	return st, nil
}

func (f *fakeCanvas) SaveLayout(_ context.Context, _, _ string, req panel.LayoutRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, it := range req.Items {
		f.layout[it.Key] = it
	}
	for _, k := range req.Remove {
		delete(f.layout, k)
	}
	return nil
}

func same(a, b panel.CanvasRef, c panel.CanvasConnection) bool {
	return c.A == a && c.B == b || c.A == b && c.B == a
}

func (f *fakeCanvas) Connect(_ context.Context, _ users.User, _, _ string, a, b panel.CanvasRef) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if a == b {
		return &panel.Refusal{Reason: "same_item", Message: "um item não se conecta a ele mesmo"}
	}
	for _, c := range f.conns {
		if same(a, b, c) {
			return nil
		}
	}
	f.conns = append(f.conns, panel.CanvasConnection{A: a, B: b, Reasons: []string{"manual"}, Manual: true})
	return nil
}

func (f *fakeCanvas) Disconnect(_ context.Context, _ users.User, _, _ string, a, b panel.CanvasRef) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, c := range f.conns {
		if !same(a, b, c) {
			continue
		}
		if !c.Manual {
			return &panel.Refusal{Reason: "automatic_connection", Message: "a conexão é automática", Params: map[string]any{"reasons": c.Reasons}}
		}
		f.conns = slices.Delete(f.conns, i, i+1)
		return nil
	}
	return &panel.Refusal{Reason: "no_connection", Message: "não há conexão"}
}

func (f *fakeCanvas) Pin(_ context.Context, _ users.User, _, _, id string, pinned bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pinned[id] = pinned
	return nil
}

func (f *fakeCanvas) WriteScroll(_ context.Context, _ users.User, _, _, _ string, req panel.ScrollWriteRequest) (panel.CanvasScroll, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if req.Base != f.scroll.Revision {
		return panel.CanvasScroll{}, panel.ErrCanvasConflict
	}
	f.scroll.Content, f.scroll.Revision = req.Content, f.scroll.Revision+1
	return f.scroll, nil
}

func (*fakeCanvas) CreateQuest(context.Context, users.User, string, string, panel.QuestRequest) (panel.CanvasQuest, error) {
	return panel.CanvasQuest{}, &panel.Refusal{Reason: "invalid_state", Message: "não nesta demonstração"}
}

func (*fakeCanvas) EditQuest(context.Context, users.User, string, string, string, panel.QuestRequest) (panel.CanvasQuest, error) {
	return panel.CanvasQuest{}, &panel.Refusal{Reason: "invalid_state", Message: "não nesta demonstração"}
}
func (*fakeCanvas) Grimoires(context.Context, users.User) ([]panel.GrimoireInfo, error) {
	return []panel.GrimoireInfo{}, nil
}
func (*fakeCanvas) SaveGrimoire(context.Context, users.User, string, string, panel.GrimoireSaveRequest) (panel.GrimoireInfo, error) {
	return panel.GrimoireInfo{}, &panel.Refusal{Reason: "invalid_state", Message: "não nesta demonstração"}
}
func (*fakeCanvas) RestoreGrimoire(context.Context, users.User, string, string, panel.GrimoireRestoreRequest) (panel.GrimoirePlan, error) {
	return panel.GrimoirePlan{}, &panel.Refusal{Reason: "invalid_state", Message: "não nesta demonstração"}
}
func (*fakeCanvas) Rituals(context.Context, string, string) ([]panel.RitualInfo, error) {
	next := time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)
	return []panel.RitualInfo{{ID: "r1", Name: "bom-dia", Target: "Mestre", Action: "prompt", Content: "Resuma o dia.", Schedule: "todo dia 09:00", Enabled: true, NextRun: &next}}, nil
}
func (*fakeCanvas) EditRitual(context.Context, users.User, string, string, string, panel.RitualEditRequest) (panel.RitualInfo, error) {
	return panel.RitualInfo{}, &panel.Refusal{Reason: "invalid_state", Message: "não nesta demonstração"}
}
