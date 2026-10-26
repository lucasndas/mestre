package workspaces_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/orchestration/workspaces"
)

// A stage that waits for something outside the engine (the Apps installed in
// the repository) leaves the workspace at the stage with no failure, is asked
// again after the wait and, when it stops waiting, the workspace goes on by
// itself through the mirror to active. The stages after it do not run while
// it waits (docs/sessao-zero/workspace.md#início).
func TestWaitingStageHoldsTheWorkspaceWithoutAFailureAndGoesOnByItself(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	var ready atomic.Bool
	var runs atomic.Int32
	log := &stageLog{}
	r.registerStages(r.svc, log, nil)
	if err := r.svc.Register(workspaces.StageApps, func(_ context.Context, w workspaces.Workspace) error {
		runs.Add(1)
		if !ready.Load() {
			return &workspaces.WaitError{Reason: "Apps ainda não instalados", Poll: 30 * time.Second}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	w, err := r.svc.Create(r.ctx, workspaces.CreateRequest{Repo: "lucas/Meu_App"})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.svc.Prepare(r.ctx, w.Slug); !errors.Is(err, workspaces.ErrWaiting) {
		t.Fatalf("Prepare = %v; want ErrWaiting", err)
	}
	got := r.get(w.Slug)
	if got.State != workspaces.StatePreparing || got.Stage != workspaces.StageApps || got.StageFailure != nil {
		t.Fatalf("workspace = %s/%s failure=%+v; want preparing at apps, no failure", got.State, got.Stage, got.StageFailure)
	}
	if reason, ok := r.svc.Waiting(w.Slug); !ok || reason != "Apps ainda não instalados" {
		t.Fatalf("Waiting = %q, %v", reason, ok)
	}
	if r.git.Handle(r.svc.Layout().Mirror(w.Slug)) != nil || len(log.list()) != 0 {
		t.Fatal("a later stage ran while the stage waited")
	}
	// Tick does not run it before the wait is over, and schedules the next look.
	next, ok, err := r.svc.Tick(r.ctx)
	if err != nil || !ok || !next.Equal(t0.Add(30*time.Second)) || runs.Load() != 1 {
		t.Fatalf("Tick = %v, %v, %v after %d runs; want to look again at +30 s", next, ok, err, runs.Load())
	}
	r.clk.Advance(30 * time.Second)
	if _, _, err := r.svc.Tick(r.ctx); err != nil || runs.Load() != 2 {
		t.Fatalf("after the wait: %v, %d runs; want the stage run again", err, runs.Load())
	}
	if got := r.get(w.Slug); got.StageFailure != nil || got.Stage != workspaces.StageApps {
		t.Fatalf("workspace = %s failure=%+v", got.Stage, got.StageFailure)
	}
	ready.Store(true)
	r.clk.Advance(30 * time.Second)
	if _, _, err := r.svc.Tick(r.ctx); err != nil {
		t.Fatal(err)
	}
	if got := r.get(w.Slug); got.State != workspaces.StateActive {
		t.Fatalf("workspace = %s/%s; want it to go on to active by itself", got.State, got.Stage)
	}
	if _, ok := r.svc.Waiting(w.Slug); ok {
		t.Fatal("still waiting after the stage was done")
	}
}

// A stage that failed and then waits is not failing any more.
func TestWaitingAfterAFailureClearsTheFailure(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.registerStages(r.svc, &stageLog{}, nil)
	var calls atomic.Int32
	if err := r.svc.Register(workspaces.StageApps, func(context.Context, workspaces.Workspace) error {
		if calls.Add(1) == 1 {
			return errors.New("GitHub fora do ar")
		}
		return &workspaces.WaitError{Reason: "esperando"}
	}); err != nil {
		t.Fatal(err)
	}
	w, err := r.svc.Create(r.ctx, workspaces.CreateRequest{Repo: "lucas/Meu_App"})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.svc.Prepare(r.ctx, w.Slug); err == nil || errors.Is(err, workspaces.ErrWaiting) {
		t.Fatalf("Prepare = %v; want the failure", err)
	}
	if r.get(w.Slug).StageFailure == nil {
		t.Fatal("the failure was not recorded")
	}
	if err := r.svc.Prepare(r.ctx, w.Slug); !errors.Is(err, workspaces.ErrWaiting) {
		t.Fatalf("Prepare = %v; want it waiting", err)
	}
	if got := r.get(w.Slug); got.StageFailure != nil {
		t.Fatalf("failure = %+v; waiting is not an error", got.StageFailure)
	}
}
