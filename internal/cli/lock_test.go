package cli_test

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/lucasndas/mestre/internal/cli"
	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/pkg/clock"
)

// Part of CA-CANAL-04: with the lock on, the commands that start work or act on
// GitHub are refused at the gate; the others go on.
func TestTheLockRefusesWhatStartsWorkAndLetsTheRestGoOn(t *testing.T) {
	t.Parallel()
	var locked atomic.Bool
	var ran atomic.Int32
	fwd := localchan.NewLocalForwarder()
	for _, name := range []string{"recruit", "quest run", "issue comment", "issue list", "list", "inbox", "note read"} {
		fwd.Handle(name, localchan.HandlerFunc(func(context.Context, *localchan.Call) localchan.Response {
			ran.Add(1)
			return localchan.OK(nil)
		}))
	}
	gate := cli.NewGate(cli.GateConfig{
		Profiles: cli.ProfilesFunc(func(context.Context, string) cli.Profile { return cli.Profile{Role: cli.RoleTableMaster} }),
		Next:     fwd, Locked: func(context.Context) bool { return locked.Load() },
	})
	call := func(cmd string) localchan.Response {
		id, _ := localchan.NewID(clock.Real{})
		return gate.Forward(t.Context(), &localchan.Call{Request: localchan.Request{V: 1, ID: id, AgentID: "gm", Cmd: cmd, Flags: map[string]any{}},
			Caller: localchan.Caller{Identified: true, AgentID: "gm"}})
	}
	for _, cmd := range []string{"recruit", "quest run", "issue comment", "issue list", "list", "inbox", "note read"} {
		if got := call(cmd); !got.OK {
			t.Fatalf("%s unlocked: %+v", cmd, got.Error)
		}
	}
	locked.Store(true)
	before := ran.Load()
	for _, cmd := range []string{"recruit", "quest run", "issue comment", "issue list"} {
		got := call(cmd)
		if got.OK || got.Error == nil || got.Error.Code != "locked" || got.ExitCode() != 3 {
			t.Errorf("%s locked: %+v, want exit 3 locked", cmd, got.Error)
		}
	}
	if ran.Load() != before {
		t.Error("a command that the lock refuses ran")
	}
	for _, cmd := range []string{"list", "inbox", "note read"} {
		if got := call(cmd); !got.OK {
			t.Errorf("%s locked: %+v, want it to go on", cmd, got.Error)
		}
	}
}
