package usage_test

import (
	"errors"
	"testing"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/orchestration/usage"
)

func TestAnApprovedExtraResumesOnlyWhatTheLimitPaused(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	for _, l := range []usage.Limit{
		{Scope: usage.ScopeWorkspace, Key: "loja", Unit: classes.UnitUSD, Value: 4},
		{Scope: usage.ScopeDay, Key: "loja", Unit: classes.UnitUSD, Value: 6},
	} {
		if err := w.svc.SetLimit(w.ctx, classes.LevelAdmin, "loja", l); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.svc.Record(w.ctx, stepRecord(w, 4)); err != nil { // workspace at 100%
		t.Fatal(err)
	}
	if err := w.svc.Record(w.ctx, stepRecord(w, 2)); err != nil { // day at 100%
		t.Fatal(err)
	}
	// the first extra is approved but the day limit still holds the workspace
	if err := w.svc.Decided(w.ctx, usage.Decision{Request: 1, Approved: true}); err != nil {
		t.Fatal(err)
	}
	for _, e := range w.pause.got() {
		if e == "resume workspace loja" {
			t.Fatal("resumed while another limit is still at 100%")
		}
	}
	if err := w.svc.Decided(w.ctx, usage.Decision{Request: 2, Approved: true}); err != nil {
		t.Fatal(err)
	}
	if got := w.pause.got(); got[len(got)-1] != "resume workspace loja" {
		t.Errorf("after both approvals: %v", got)
	}
}

func TestARaisedLimitSpeaksAgain(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	l := usage.Limit{Scope: usage.ScopeTable, Key: "loja/t1", Unit: classes.UnitUSD, Value: 5}
	if err := w.svc.SetLimit(w.ctx, classes.LevelAdmin, "loja", l); err != nil {
		t.Fatal(err)
	}
	if err := w.svc.Record(w.ctx, stepRecord(w, 5)); err != nil {
		t.Fatal(err)
	}
	l.Value = 8
	if err := w.svc.SetLimit(w.ctx, classes.LevelAdmin, "loja", l); err != nil {
		t.Fatal(err)
	}
	if err := w.svc.Record(w.ctx, stepRecord(w, 3)); err != nil { // 8 of 8
		t.Fatal(err)
	}
	if n := len(w.extra.all()); n != 2 {
		t.Errorf("%d requests, want one for each time the limit was reached", n)
	}
	// a limit for the table of another workspace is refused
	if err := w.svc.SetLimit(w.ctx, classes.LevelAdmin, "outra", l); !errors.Is(err, usage.ErrInvalid) {
		t.Errorf("a table of another workspace = %v, want invalid", err)
	}
}
