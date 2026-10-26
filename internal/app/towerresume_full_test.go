//go:build full

package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/localchan"
	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/protocol"
	"github.com/mestre-ia/mestre/internal/terminal"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// recruitOnTower is `mestre recruit` of the table master, with the Tower as the machine, and
// waits for the character to be idle.
func (x *channelRig) recruitOnTower(t *testing.T, master characters.Character, name string) characters.Character {
	t.Helper()
	ctx := t.Context()
	resp := forwarder{x.a.e}.Forward(ctx, &localchan.Call{
		Request: localchan.Request{V: 1, ID: "recruit-" + name, AgentID: master.ID, Cmd: "recruit", Args: localchan.TextArgs(name), Flags: map[string]any{"sheet": "Dev Claude", "machine": x.b.id}},
		Caller:  localchan.Caller{Identified: true, AgentID: master.ID},
	})
	if !resp.OK {
		t.Fatalf("recruit %s: %+v", name, resp.Error)
	}
	var out characters.Character
	x.r.waitFor(name+" idle on the Tower", func() bool {
		c, err := x.orc.Registry.ByName(ctx, "meu-app", master.Table, name)
		if err != nil || c.State != characters.Idle {
			return false
		}
		out = c
		return true
	})
	return out
}

// statesOf are the states a character went through, in order.
func (x *channelRig) statesOf(t *testing.T, id string) []string {
	t.Helper()
	rows, err := orcDB(t, x.a).Reader().QueryContext(t.Context(), `SELECT to_state FROM character_state_log WHERE character_id = ? ORDER BY rowid`, id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	return out
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// CA-PERS-07, the part of a Tower: the engine of the Tower is killed with a busy and a
// waiting character, and the mestre-pty of a third one is killed too. When the Tower comes back
// and tells its terminals, the busy and the waiting keep their states and their
// mestre-pty without passing through interrupted; the third is interrupted by the resync and the
// Citadel resumes it on the Tower, keeping the saved session, its task and its wait.
func TestAfterTheEngineOfATowerIsKilledTheBusyAndTheWaitingKeepTheirStatesAndTheDeadOneIsResumedByTheCitadel(t *testing.T) {
	t.Parallel()
	resumed := &resumeLog{}
	x := newChannelRigWith(t, channelOpts{builder: true, resumed: resumed})
	ctx := t.Context()
	// a turn that does not end, for the busy one
	script := "prompts:\n  - match: \"trabalho longo\"\n    steps:\n      - say: \"comecei\"\n      - hang: true\n  - match: \".*\"\n    steps:\n      - say: \"feito\"\n"
	if err := os.WriteFile(filepath.Join(x.root, "home", "quick.yaml"), []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	const table = "4-busca"
	x.seedTable(t, table, "mestre/4-busca", 4)
	master := characters.Character{ID: "m4", Name: "Mestre", Kind: characters.KindCharacter, Role: characters.RoleTableMaster, Workspace: "meu-app", Table: table, Machine: x.a.id,
		Sheet: "Dev Claude", Base: "claude-code", Class: "Dev", State: characters.Idle, CreatedBy: characters.Creator{Kind: characters.CreatedByEngine}, Lifecycle: characters.Persistent, CreatedAt: clock.Real{}.Now()}
	if err := x.orc.Registry.Insert(ctx, master); err != nil {
		t.Fatal(err)
	}
	busy := x.recruitOnTower(t, master, "Ocupado")
	waiting := x.recruitOnTower(t, master, "Espera")
	victim := x.recruitOnTower(t, master, "Vitima")

	// the busy one is in a turn that does not end
	if err := x.a.e.mach.Load().Input(ctx, x.b.id, busy.ID, []byte("trabalho longo\r")); err != nil {
		t.Fatal(err)
	}
	x.r.waitFor("Ocupado busy", func() bool { return x.state(t, busy.ID) == characters.Busy })
	// the waiting one ended its turn with a wait (the Citadel turns the idle into waiting)
	cur, err := x.orc.Registry.Get(ctx, waiting.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := x.orc.Waits.WaitUntil(ctx, cur, clock.Real{}.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	report := func(c characters.Character, state string) {
		t.Helper()
		if res, err := x.b.e.towerLink().Call(ctx, protocol.Outgoing{Type: protocol.TypeTerminalStatus, Payload: protocol.TerminalStatus{TerminalID: c.TerminalID, State: state, AgentID: c.ID}}); err != nil || res.Status != protocol.AckOK {
			t.Fatalf("report %s: %+v %v", state, res, err)
		}
	}
	report(waiting, "busy")
	report(waiting, "idle")
	x.r.waitFor("Espera waiting", func() bool { return x.state(t, waiting.ID) == characters.Waiting })
	// the third has a task under way (an ephemeral one with none is not resumed) and a wait of its own
	if err := x.orc.Characters.TaskAssigned(ctx, victim.ID); err != nil {
		t.Fatal(err)
	}
	if cur, err = x.orc.Registry.Get(ctx, victim.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := x.orc.Waits.WaitUntil(ctx, cur, clock.Real{}.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	busy, waiting, victim = x.get(t, busy.ID), x.get(t, waiting.ID), x.get(t, victim.ID)
	if victim.ProviderSessionID == "" || victim.PTY.PID == 0 {
		t.Fatalf("the character to lose has session %q and mestre-pty %+v", victim.ProviderSessionID, victim.PTY)
	}

	// the engine of the Tower is killed; the mestre-pty of the third dies too
	x.b.stop()
	sys := terminal.NewSystem()
	if err := sys.KillTree(ctx, terminal.ProcID{PID: victim.PTY.PID, Start: victim.PTY.Started}, nil); err != nil {
		t.Fatal(err)
	}
	x.r.start(x.b, clock.Real{})

	// the Tower tells its terminals when it connects: the third is interrupted by the resync and
	// resumed by the Citadel, on the Tower, with a terminal of its own
	x.r.waitFor("the dead one to be resumed on the Tower", func() bool {
		c := x.get(t, victim.ID)
		// back with its wait kept: its turn ends waiting, as the Citadel turns it
		return c.State == characters.Waiting && c.TerminalID != victim.TerminalID && x.aliveOnTower(victim.ID)
	})
	if log := x.statesOf(t, victim.ID); !contains(log, string(characters.Interrupted)) {
		t.Errorf("the character whose mestre-pty died never was interrupted: %v", log)
	}
	if got := x.get(t, victim.ID); !got.TaskOpen {
		t.Errorf("after the resumption the task is open %v; want it kept", got.TaskOpen)
	}
	// it was started again with the session it saved (the first build is the recruitment, with none)
	if builds := resumed.of(victim.ID); len(builds) != 2 || builds[0] != "" || builds[1] != victim.ProviderSessionID {
		t.Errorf("the sessions the character was built with = %q; want a new one, then %q to resume", builds, victim.ProviderSessionID)
	}
	if _, active, err := x.orc.Waits.Active(ctx, victim.ID); err != nil || !active {
		t.Errorf("the wait of the resumed character: active %v, %v; want it kept", active, err)
	}
	// the two survivors are as they were, with the same mestre-pty, and never were interrupted
	for _, w := range []struct {
		c    characters.Character
		want characters.State
	}{{busy, characters.Busy}, {waiting, characters.Waiting}} {
		got := x.get(t, w.c.ID)
		if got.State != w.want || got.PTY.PID != w.c.PTY.PID {
			t.Errorf("%s is %s with the mestre-pty %d; want %s and %d", w.c.Name, got.State, got.PTY.PID, w.want, w.c.PTY.PID)
		}
		if log := x.statesOf(t, w.c.ID); contains(log, string(characters.Interrupted)) {
			t.Errorf("%s passed through interrupted: %v", w.c.Name, log)
		}
	}
	if _, active, err := x.orc.Waits.Active(ctx, waiting.ID); err != nil || !active {
		t.Errorf("the wait of the waiting survivor: active %v, %v; want it kept", active, err)
	}
}

func (x *channelRig) get(t *testing.T, id string) characters.Character {
	t.Helper()
	c, err := x.orc.Registry.Get(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	return c
}
