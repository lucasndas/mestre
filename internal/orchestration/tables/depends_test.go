package tables_test

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/orchestration/tables"
)

// fakeDeps is the dependencies between epics (internal/orchestration/epicdeps has
// the real one): the epics in on wait for the ones it lists.
type fakeDeps struct {
	mu        sync.Mutex
	on        map[int][]int
	opened    []string
	merged    []string
	cancelled []string
}

func (f *fakeDeps) Opened(_ context.Context, _ string, t tables.Table) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.opened = append(f.opened, t.ID)
	return nil
}

func (f *fakeDeps) Held(_ context.Context, _ string, epic int) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.on[epic]) > 0, nil
}

func (f *fakeDeps) Waiting(_ context.Context, _ string, epic int) ([]int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.on[epic]), nil
}

func (f *fakeDeps) Merged(_ context.Context, _ string, t tables.Table) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.merged = append(f.merged, t.ID)
	return nil
}

func (f *fakeDeps) Dropped(context.Context, string, tables.Table) error { return nil }

func (f *fakeDeps) Cancelled(_ context.Context, _ string, t tables.Table) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancelled = append(f.cancelled, t.ID)
	return nil
}

func (f *fakeDeps) release(epic int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.on, epic)
}

// CA-DEPMESA-01
func TestATableWhoseEpicWaitsForAnotherOpensWithTheRequestHeldAndTheListSaysWhy(t *testing.T) {
	t.Parallel()
	deps := &fakeDeps{on: map[int][]int{15: {12}}}
	w := newWorld(t, worldOpts{deps: deps})
	w.activate()
	w.gh.add(12, "Login", "epico")
	w.gh.add(15, "Checkout", "epico")
	t12 := w.open(12).Table
	before := len(w.deliv.typed())

	res := w.open(15)
	if !slices.Equal(deps.opened, []string{t12.ID, res.Table.ID}) {
		t.Fatalf("the dependencies were told of %v, want both tables", deps.opened)
	}
	// The master is recruited and the request is in its queue, but nothing is
	// handed to it and the table stays open: no step is recruited.
	if res.Table.State != tables.StateOpen || res.Master.ID == "" {
		t.Fatalf("the table is %s with master %q, want open and recruited", res.Table.State, res.Master.ID)
	}
	for _, d := range w.deliv.typed()[before:] {
		if d.TerminalID == res.Master.TerminalID {
			t.Fatalf("the request was handed to the master of the held table: %+v", d)
		}
	}
	alive, err := w.reg.Alive(w.ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range alive {
		if c.Table == res.Table.ID && c.Role != characters.RoleTableMaster {
			t.Errorf("%s (%s) was recruited for the held table; want only its master", c.Name, c.Role)
		}
	}
	if held, _ := w.svc.Paused(w.ctx, w.slug(), res.Table.ID); !held {
		t.Fatal("the held table is not held for the inbox")
	}
	if held, _ := w.svc.Held(w.ctx, w.slug(), res.Table.ID); !held {
		t.Fatal("Held says the table is free")
	}
	if p, _ := w.svc.Paused(w.ctx, w.slug(), t12.ID); p {
		t.Fatal("the table without dependency is held")
	}
	if got, _ := w.svc.Get(w.ctx, w.slug(), res.Table.ID); got.Paused {
		t.Fatal("a table that waits is marked paused: it is not a pause")
	}

	r := w.run(w.gm.ID, "table list", nil)
	if !r.OK || !strings.Contains(dataOf(t, r)["text"].(string), "waiting for table #12") {
		t.Fatalf("table list = %s, want the table 'waiting for table #12'", r.Data)
	}
	rows := dataOf(t, r)["tables"].([]any)
	for _, row := range rows {
		m := row.(map[string]any)
		want := []any{}
		if m["epic"].(float64) == 15 {
			want = []any{float64(12)}
		}
		if got := m["waiting_for"].([]any); len(got) != len(want) || (len(want) == 1 && got[0] != want[0]) {
			t.Fatalf("waiting_for of %v = %v, want %v", m["name"], got, want)
		}
	}
	if r := w.run(w.gm.ID, "table status", nil, res.Table.ID); !strings.Contains(dataOf(t, r)["text"].(string), "waiting for table #12") {
		t.Fatalf("table status = %s, want the wait", r.Data)
	}

	// The release delivers what waited.
	deps.release(15)
	if err := w.inb.PumpAll(w.ctx); err != nil {
		t.Fatal(err)
	}
	handed := false
	for _, d := range w.deliv.typed()[before:] {
		handed = handed || d.TerminalID == res.Master.TerminalID
	}
	if !handed {
		t.Fatal("the request was not handed to the master after the release")
	}
}

// Part of CA-DEPMESA-02 (the tables tell the dependencies of the merge that was
// confirmed, once, and of a table that was cancelled)
func TestTheDependenciesAreToldOfTheMergeThatWasConfirmedAndOfACancelledTable(t *testing.T) {
	t.Parallel()
	deps := &fakeDeps{on: map[int][]int{}}
	w := newWorld(t, worldOpts{deps: deps})
	w.activate()
	w.gh.add(12, "Login", "epico")
	t12 := w.open(12).Table
	if _, err := w.svc.PROpened(w.ctx, w.slug(), t12.ID, 7, "abc"); err != nil {
		t.Fatal(err)
	}
	if len(deps.merged) != 0 {
		t.Fatalf("told of a merge before it happened: %v", deps.merged)
	}
	if _, err := w.svc.MergeConfirmed(w.ctx, w.slug(), t12.ID, "abc"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(deps.merged, []string{t12.ID}) {
		t.Fatalf("merged = %v, want %v", deps.merged, t12.ID)
	}
}

// Part of CA-DEPMESA-05 (a cancelled table is told to the dependencies when the
// cancellation ends, not before)
func TestTheDependenciesAreToldWhenACancellationEnds(t *testing.T) {
	t.Parallel()
	deps := &fakeDeps{on: map[int][]int{}}
	w := newWorld(t, worldOpts{deps: deps})
	w.activate()
	w.gh.add(12, "Login", "epico")
	tbl := w.open(12).Table
	if _, err := w.svc.Close(w.ctx, tables.CloseRequest{By: w.gm, Table: tbl.ID, Cancel: true, Reason: "desistiu"}); err == nil && len(deps.cancelled) != 0 {
		t.Fatalf("told of a cancellation that was not allowed: %v", deps.cancelled)
	}
	w.dest.allowed = true
	if _, err := w.svc.Close(w.ctx, tables.CloseRequest{By: w.gm, Table: tbl.ID, Cancel: true, Reason: "desistiu"}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(deps.cancelled, []string{tbl.ID}) {
		t.Fatalf("cancelled = %v, want %s", deps.cancelled, tbl.ID)
	}
}
