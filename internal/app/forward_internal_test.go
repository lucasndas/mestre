package app

import (
	"context"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/guild"
	"github.com/lucasndas/mestre/pkg/clock"

	"github.com/lucasndas/mestre/internal/localchan"
)

// The CLI is told why the Citadel cannot answer: away, or another machine
// taking its place (docs/arquitetura/canal-local.md#encaminhamento-à-cidadela).
func TestTheCLIIsToldTheCitadelIsAwayOrInFailover(t *testing.T) {
	t.Parallel()
	e := &Engine{local: localchan.NewLocalForwarder()}
	call := &localchan.Call{Request: localchan.Request{Cmd: "list"}}
	if r := (forwarder{e}).Forward(t.Context(), call); r.Error == nil || r.Error.Code != localchan.CodeCitadelUnavailable {
		t.Fatalf("with no Citadel: %+v", r.Error)
	}
	e.failover = true
	if r := (forwarder{e}).Forward(t.Context(), call); r.Error == nil || r.Error.Code != localchan.CodeCitadelInFailover || r.ExitCode() != localchan.ExitUnavailable {
		t.Fatalf("during a promotion: %+v", r.Error)
	}
}

// The lock is checked where every call meets, the Citadel: a Tower does not
// know it, and its call does not go through the gate of the Citadel's machine.
func TestTheCitadelRefusesTheCallsTheLockRefusesWhateverTheMachineTheyCameFrom(t *testing.T) {
	t.Parallel()
	e := &Engine{local: localchan.NewLocalForwarder()}
	locked := true
	e.setLocked(func(context.Context) bool { return locked })
	for _, tc := range []struct {
		cmd     string
		refused bool
	}{{"recruit", true}, {"quest run", true}, {"pr comment", true}, {"list", false}, {"done", false}, {"nonsense", false}} {
		_, ok := e.lockRefusal(t.Context(), tc.cmd)
		if ok == tc.refused {
			t.Errorf("%q: refused %v, want %v", tc.cmd, !ok, tc.refused)
		}
	}
	locked = false
	if _, ok := e.lockRefusal(t.Context(), "recruit"); !ok {
		t.Error("refused with the Mestre unlocked")
	}
}

func TestAForwardedCallToALockedCitadelIsRefusedBeforeItRuns(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake(time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC))
	fence := guild.NewFence(guild.FenceConfig{Clock: clk, Epoch: func() uint64 { return 1 }})
	e := &Engine{local: localchan.NewLocalForwarder(), cit: &citadelRun{fence: fence}}
	e.setLocked(func(context.Context) bool { return true })
	r := (forwarder{e}).Forward(t.Context(), &localchan.Call{Request: localchan.Request{Cmd: "recruit"}})
	if r.Error == nil || r.Error.Code != "locked" {
		t.Fatalf("recruit while locked: %+v", r.Error)
	}
	e.setLocked(func(context.Context) bool { return false })
	if r := (forwarder{e}).Forward(t.Context(), &localchan.Call{Request: localchan.Request{Cmd: "recruit"}}); r.Error != nil && r.Error.Code == "locked" {
		t.Fatal("refused as locked while unlocked")
	}
}
