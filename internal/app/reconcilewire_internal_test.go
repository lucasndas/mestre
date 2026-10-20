package app

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/guild"
	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/internal/testkit/fakelock"
	"github.com/lucasndas/mestre/internal/testkit/guildtpl"
	"github.com/lucasndas/mestre/pkg/clock"
)

type reconcileWorld struct {
	e   *Engine
	db  *store.DB
	clk *clock.Fake
	run *citadelRun
}

func newReconcileWorld(t *testing.T) *reconcileWorld {
	t.Helper()
	clk := clock.NewFake(time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC))
	db, err := guildtpl.Open(t.Context(), filepath.Join(t.TempDir(), "mestre.db"), store.Options{Synchronous: "OFF", Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	e := &Engine{clk: clk, log: slog.New(slog.DiscardHandler), statusCh: make(chan struct{}, 1), id: guild.Identity{MachineID: "b"}}
	// the epoch 2 was taken by this machine, by a promotion
	if _, err := db.Writer().ExecContext(t.Context(), `INSERT INTO epochs (epoch, citadel_machine_id, from_lsn, started_at) VALUES (2, 'b', 0, ?)`, clk.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	return &reconcileWorld{e: e, db: db, clk: clk, run: &citadelRun{epoch: 2}}
}

// A promoted Citadel that restarts before the reconciliation finished reconciles
// again; one that finished it does not.
func TestAPromotedCitadelThatRestartsBeforeReconcilingReconcilesAgain(t *testing.T) {
	t.Parallel()
	w := newReconcileWorld(t)
	if !w.e.reconcilePending(t.Context(), w.db, 2) {
		t.Fatal("the epoch taken by this machine and never reconciled is not pending")
	}
	if w.e.reconcilePending(t.Context(), w.db, 1) || w.e.reconcilePending(t.Context(), w.db, 9) {
		t.Error("the first epoch or an unknown one is pending")
	}
	other := &Engine{id: guild.Identity{MachineID: "c"}}
	if other.reconcilePending(t.Context(), w.db, 2) {
		t.Error("the epoch of another machine is pending here")
	}
	calls := 0
	rec := w.e.reconcileFor(w.run, w.db, func(context.Context) error { calls++; return nil })
	if err := rec(t.Context()); err != nil || calls != 1 {
		t.Fatalf("reconcile = %v after %d calls", err, calls)
	}
	if w.e.reconcilePending(t.Context(), w.db, 2) {
		t.Error("the epoch is pending after it was reconciled")
	}
}

// A failing reconciliation is logged as an error, kept for the status (the fence
// stays waiting) and cleared by the next one that works.
func TestAFailingReconciliationIsKeptForTheStatusAndTheEpochStaysPending(t *testing.T) {
	t.Parallel()
	w := newReconcileWorld(t)
	boom := errors.New("github: 502")
	fail := true
	rec := w.e.reconcileFor(w.run, w.db, func(context.Context) error {
		if fail {
			return boom
		}
		return nil
	})
	if err := rec(t.Context()); !errors.Is(err, boom) {
		t.Fatalf("reconcile = %v", err)
	}
	w.clk.Advance(time.Hour)
	if err := rec(t.Context()); !errors.Is(err, boom) {
		t.Fatalf("reconcile = %v", err)
	}
	f := w.run.reconcileFail.Load()
	if f == nil || f.since.Format("15:04") != "12:00" || !strings.Contains(f.err, "502") {
		t.Fatalf("kept %+v; want the first failure (12:00) and its reason", f)
	}
	if !w.e.reconcilePending(t.Context(), w.db, 2) {
		t.Error("a failed reconciliation marked the epoch")
	}
	fail = false
	if err := rec(t.Context()); err != nil || w.run.reconcileFail.Load() != nil {
		t.Fatalf("after it worked: %v, kept %+v", err, w.run.reconcileFail.Load())
	}
}

// The fence of the period gets the reconciliation: the line of citadel.go that
// makes it from the integration (reconcilerOf) and gives it to the fence is there,
// and the configuration carries what it is given.
func TestTheFenceOfThePeriodIsGivenTheReconciliationOfTheIntegration(t *testing.T) {
	t.Parallel()
	src, err := os.ReadFile("citadel.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"e.reconcileFor(run, db, reconcilerOf(integ, hosted))",
		"guild.NewFence(e.fenceConfig(run, integ, reconcile, promoted, wait))",
	} {
		if !strings.Contains(string(src), want) {
			t.Errorf("citadel.go lost the line %q: the fence would not reconcile GitHub", want)
		}
	}
	w := newReconcileWorld(t)
	called := false
	cfg := w.e.fenceConfig(w.run, nil, func(context.Context) error { called = true; return nil }, true, time.Second)
	if cfg.Reconcile == nil || !cfg.Promoted {
		t.Fatalf("config = %+v", cfg)
	}
	_ = cfg.Reconcile(t.Context())
	if !called {
		t.Error("the reconciliation of the config is not the one given")
	}
}

// The status shows why a promoted Citadel waits.
func TestTheStatusSaysWhyAPromotedCitadelWaitsForTheReconciliation(t *testing.T) {
	t.Parallel()
	w := newReconcileWorld(t)
	w.run.reconcileFail.Store(&reconcileFailure{since: w.clk.Now(), err: "github: 502"})
	w.run.lock = fakelock.New(2, "b").For("b")
	w.run.fence = guild.NewFence(w.e.fenceConfig(w.run, nil, nil, true, time.Second))
	got := w.e.waitingOf(w.run)
	if got != "Cidadela promovida aguardando reconciliação desde 12:00: github: 502" {
		t.Fatalf("waiting = %q", got)
	}
}
