package app

import (
	"log/slog"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/guild"
	"github.com/mestre-ia/mestre/internal/logging"
	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/protocol"
	"github.com/mestre-ia/mestre/internal/store"
	"github.com/mestre-ia/mestre/internal/testkit/guildtpl"
	"github.com/mestre-ia/mestre/pkg/clock"
)

type resyncRig struct {
	e   *Engine
	orc *Orchestration
	clk *clock.Fake
	db  *store.DB
	t   *testing.T
}

func newResyncRig(t *testing.T) *resyncRig {
	t.Helper()
	root := t.TempDir()
	clk := clock.NewFake(time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC))
	db, err := guildtpl.Open(t.Context(), filepath.Join(root, "mestre.db"), store.Options{Synchronous: "OFF", Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	e := &Engine{clk: clk, log: slog.New(slog.DiscardHandler), id: guild.Identity{MachineID: "citadel"},
		paths: Paths{Workspaces: filepath.Join(root, "workspaces"), Maquina: filepath.Join(root, "maquina")},
		deps:  Deps{Masker: logging.NewMasker(), Binary: filepath.Join(root, "mestre")}}
	orc, err := e.hostOrchestration(db, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Writer().ExecContext(t.Context(), `INSERT INTO workspaces (slug, repo, repo_key, clone_url, default_branch, state, stage, created_at, updated_at)
		VALUES ('meu-app', 'lucas/meu-app', 'lucas/meu-app', 'https://example.test/x.git', 'main', 'active', '', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	return &resyncRig{e: e, orc: orc, clk: clk, db: db, t: t}
}

func (r *resyncRig) add(id, machine string, st characters.State, created, signal time.Time, started ...time.Time) {
	r.t.Helper()
	c := characters.Character{ID: id, Name: id, Kind: characters.KindCharacter, Role: characters.RoleCharacter, Workspace: "meu-app", Table: "t1", Machine: machine,
		Sheet: "S", Base: "claude-code", State: st, TerminalID: "t-" + id, CreatedBy: characters.Creator{Kind: characters.CreatedByEngine},
		Lifecycle: characters.Ephemeral, CreatedAt: created, StartedAt: created, LastSignalAt: signal}
	if len(started) > 0 {
		c.StartedAt = started[0]
	}
	if err := r.orc.Registry.Insert(r.t.Context(), c); err != nil {
		r.t.Fatal(err)
	}
}

func (r *resyncRig) state(id string) characters.State {
	r.t.Helper()
	c, err := r.orc.Registry.Get(r.t.Context(), id)
	if err != nil {
		r.t.Fatal(err)
	}
	return c.State
}

func (r *resyncRig) resync(at time.Time, list ...protocol.TerminalState) []string {
	r.t.Helper()
	end, err := r.e.resyncFromTower(r.t.Context(), r.orc, "torre", list, at)
	if err != nil {
		r.t.Fatal(err)
	}
	return end
}

// Part of CA-CIDADELA-11: what a Tower tells at each connection is the truth about
// its processes. The character the registry has alive there that the list lacks is
// interrupted, but not a start on its way, nor one that changed on the Citadel after
// the picture was taken; one the list has keeps its state; another machine's is not touched.
func TestTheResyncOfATowerInterruptsWhatItLacksAndOnlyThat(t *testing.T) {
	t.Parallel()
	r := newResyncRig(t)
	now := r.clk.Now()
	old := now.Add(-10 * time.Minute)
	r.add("gone", "torre", characters.Busy, old, old)
	r.add("kept", "torre", characters.Idle, old, old)
	r.add("fresh", "torre", characters.Starting, now.Add(-time.Second), now.Add(-time.Second))
	r.add("lost", "torre", characters.Starting, old, old)
	r.add("changed", "torre", characters.Busy, old, now)
	r.add("elsewhere", "outra", characters.Busy, old, old)
	r.resync(now.Add(-time.Minute), protocol.TerminalState{TerminalID: "t-kept", AgentID: "kept", State: "idle"})
	for id, want := range map[string]characters.State{
		"gone": characters.Interrupted, "kept": characters.Idle, "fresh": characters.Starting, "lost": characters.Interrupted,
		"changed": characters.Busy, "elsewhere": characters.Busy,
	} {
		if got := r.state(id); got != want {
			t.Errorf("%s is %s after the resync; want %s", id, got, want)
		}
	}
}

// Part of CA-CIDADELA-11: the grace of a start counts from when it entered starting, not from
// when the character was created: a resumed character is old but its restart is recent.
func TestTheGraceOfAResumedStartCountsFromItsStartNotItsCreation(t *testing.T) {
	t.Parallel()
	r := newResyncRig(t)
	now := r.clk.Now()
	old := now.Add(-10 * time.Minute)
	r.add("resumed", "torre", characters.Starting, old, old, now.Add(-time.Second))
	r.resync(now)
	if got := r.state("resumed"); got != characters.Starting {
		t.Errorf("a restart of a second ago is %s after the resync; want starting", got)
	}
}

// Part of CA-CIDADELA-11: a start the Citadel has in flight is not one the Tower lacks, however old its row.
func TestAStartInFlightIsNotInterruptedByTheResync(t *testing.T) {
	t.Parallel()
	r := newResyncRig(t)
	old := r.clk.Now().Add(-10 * time.Minute)
	r.add("c1", "torre", characters.Starting, old, old)
	r.e.mach.Load().markInFlight("c1", 1)
	r.resync(r.clk.Now())
	if got := r.state("c1"); got != characters.Starting {
		t.Errorf("a start in flight is %s; want starting", got)
	}
	r.e.mach.Load().markInFlight("c1", -1)
	r.resync(r.clk.Now())
	if got := r.state("c1"); got != characters.Interrupted {
		t.Errorf("a start that is no longer in flight is %s; want interrupted", got)
	}
}

// Part of CA-CIDADELA-11: the Tower is told to end the terminals the registry says are over or
// elsewhere, never one it does not know (a replica that is behind), nor one with no character.
func TestTheResyncAnswersWhichTerminalsTheTowerMustEnd(t *testing.T) {
	t.Parallel()
	r := newResyncRig(t)
	old := r.clk.Now().Add(-10 * time.Minute)
	r.add("alive", "torre", characters.Idle, old, old)
	r.add("over", "torre", characters.Stopped, old, old)
	r.add("moved", "outra", characters.Idle, old, old)
	end := r.resync(r.clk.Now(),
		protocol.TerminalState{TerminalID: "t1", AgentID: "alive", State: "idle"},
		protocol.TerminalState{TerminalID: "t2", AgentID: "over", State: "idle"},
		protocol.TerminalState{TerminalID: "t3", AgentID: "moved", State: "idle"},
		protocol.TerminalState{TerminalID: "t4", AgentID: "unknown", State: "idle"},
		protocol.TerminalState{TerminalID: "t5", AgentID: "", State: "idle"},
	)
	slices.Sort(end)
	if !slices.Equal(end, []string{"moved", "over"}) {
		t.Errorf("the terminals to end = %v; want over and moved", end)
	}
}

// Part of CA-CIDADELA-16: the idle of a list is waiting for a character with an active wait.
func TestTheResyncMakesAnIdleCharacterWithAWaitWaiting(t *testing.T) {
	t.Parallel()
	r := newResyncRig(t)
	old := r.clk.Now().Add(-10 * time.Minute)
	r.add("c1", "torre", characters.Idle, old, old)
	c, err := r.orc.Registry.Get(t.Context(), "c1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.orc.Waits.WaitUntil(t.Context(), c, r.clk.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	// the wait itself may have moved it; the registry says idle again as the Tower would tell it
	if _, err := r.db.Writer().ExecContext(t.Context(), `UPDATE characters SET state = 'idle' WHERE id = 'c1'`); err != nil {
		t.Fatal(err)
	}
	r.resync(r.clk.Now(), protocol.TerminalState{TerminalID: "t-c1", AgentID: "c1", State: "idle"})
	if got := r.state("c1"); got != characters.Waiting {
		t.Errorf("a character with a wait is %s after the resync that lists it idle; want waiting", got)
	}
}
