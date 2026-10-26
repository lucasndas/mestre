//go:build full

package app

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/cli"
	"github.com/mestre-ia/mestre/internal/gitx"
	"github.com/mestre-ia/mestre/internal/localchan"
	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/orchestration/inbox"
	"github.com/mestre-ia/mestre/internal/orchestration/tables"
	"github.com/mestre-ia/mestre/internal/orchestration/workspaces"
	"github.com/mestre-ia/mestre/internal/store"
	"github.com/mestre-ia/mestre/internal/testkit/fakegit"
	"github.com/mestre-ia/mestre/internal/testkit/guildtpl"
	"github.com/mestre-ia/mestre/internal/testkit/orcfakes"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// doneStack is the orchestration of the Citadel over its fakes, with a table
// and a character that runs on the Tower "torre" and has a task: what the real
// `done` handler of the quests needs to answer.
type doneStack struct {
	orc *Orchestration
	dev characters.Character
}

func newDoneStack(t *testing.T) *doneStack {
	t.Helper()
	ctx := t.Context()
	clk := clock.NewFake(time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC))
	root := t.TempDir()
	db, err := guildtpl.Open(ctx, filepath.Join(root, "stack.db"), store.Options{Synchronous: "OFF", Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	git := fakegit.NewGit(clk)
	remote, err := git.Init(ctx, filepath.Join(root, "remote", "meu-app"), gitx.InitOptions{})
	if err != nil {
		t.Fatal(err)
	}
	git.Handle(remote.Dir()).Data().Commit("main", "chore: initial commit", map[string]string{"README.md": "# Meu App\n"})
	machines := orcfakes.NewMachines(clk)
	var orc *Orchestration
	machines.Chars = func() *characters.Service { return orc.Characters }
	orc, err = NewOrchestration(OrchestrationOptions{Guild: db, Machine: "cidadela", Clock: clk, WorkspacesDir: filepath.Join(root, "workspaces"), TempDir: filepath.Join(root, "tmp"),
		Git: git, Machines: machines, Presets: orcfakes.Presets{}, Builder: orcfakes.Builder{}, GitHub: orcfakes.Hub{URL: remote.Dir()}, Epics: orcfakes.Epics{}, Alerts: orcfakes.Alerts{},
		Files: orcfakes.Files{}, Messages: orcfakes.Messages{}, Peers: &orcfakes.Peers{Root: filepath.Join(root, "towers")}})
	if err != nil {
		t.Fatal(err)
	}
	machines.Confirm = orc.Inbox.Confirmed
	ws, err := orc.Workspaces.Create(ctx, workspaces.CreateRequest{Repo: "lucas/Meu_App"})
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range workspaces.Stages {
		if st != workspaces.StageMirror {
			if err := orc.Workspaces.Register(st, func(context.Context, workspaces.Workspace) error { return nil }); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := orc.Workspaces.Prepare(ctx, ws.Slug); err != nil {
		t.Fatal(err)
	}
	gm, err := orc.Characters.Create(ctx, characters.CreateRequest{Sheet: "Grão-Mestre", Workspace: ws.Slug, Role: characters.RoleGrandMaster, Creator: characters.Creator{Kind: characters.CreatedByEngine}})
	if err != nil {
		t.Fatal(err)
	}
	opened, err := orc.Tables.Open(ctx, tables.OpenRequest{By: gm.Character, Epic: 42})
	if err != nil {
		t.Fatal(err)
	}
	res, err := orc.Characters.Create(ctx, characters.CreateRequest{Name: "Dev", Sheet: "Dev Claude", Workspace: ws.Slug, Table: opened.Table.ID, Machine: "torre",
		Role: characters.RoleCharacter, Creator: characters.Creator{Kind: characters.CreatedByCharacter, ID: opened.Master.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Character.Machine != "torre" {
		t.Fatalf("the character runs on %s; want the Tower", res.Character.Machine)
	}
	if err := orc.Characters.TaskAssigned(ctx, res.Character.ID); err != nil {
		t.Fatal(err)
	}
	return &doneStack{orc: orc, dev: res.Character}
}

// CA-CIDADELA-10 (the events of the engine of the Tower are in queue_events_full_test.go;
// here the `done` and the `list`), and
// CA-CLI-CONV-05, with the real `done` of the quests: with the Citadel away, a
// character of a Tower runs `mestre done`; the CLI answers 0 and queued: true;
// when the Citadel is back, the done is registered once (the Tower sends the
// queue twice) and the result goes, with the op_id, to the inbox of the
// character.
func TestADoneOfACharacterOfATowerWaitsForTheCitadelAndIsRegisteredOnce(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	a := r.node("a")
	b := r.node("b", a)
	st := newDoneStack(t)
	a.setup = func(e *Engine) {
		e.deps.AgentMachine = func(context.Context, string) (string, bool) { return b.id, true }
		e.Commands().Handle("done", st.orc.Quests.Handlers()["done"])
	}
	var mu sync.Mutex
	var results []QueuedResult
	a.onQueued = func(q QueuedResult) {
		if err := st.orc.Inbox.QueuedResult(t.Context(), q.OpID, q.AgentID, q.Cmd, q.Response); err != nil {
			t.Errorf("the result was not put in the inbox: %v", err)
		}
		mu.Lock()
		results = append(results, q)
		mu.Unlock()
	}
	r.found(a, clock.Real{})
	r.start(a, clock.Real{})
	r.waitFor("a to act", a.acting)
	r.join(b, a, clock.Real{})
	r.start(b, clock.Real{})
	r.waitFor("b to connect", func() bool { return b.connected(1) })

	gate := cli.NewGate(cli.GateConfig{Commands: cli.Default(), Next: forwarder{b.e}})
	id, err := localchan.NewID(clock.Real{})
	if err != nil {
		t.Fatal(err)
	}
	call := &localchan.Call{
		Request: localchan.Request{V: 1, ID: id, AgentID: st.dev.ID, Cmd: "done", Args: localchan.TextArgs("login implementado"), Flags: map[string]any{}},
		Caller:  localchan.Caller{Identified: true, AgentID: st.dev.ID, TerminalID: st.dev.TerminalID},
	}

	a.stop()
	r.waitFor("b to see the Citadel away", func() bool { return !b.e.Status().Connected })
	resp := gate.Forward(t.Context(), call)
	if !resp.OK || resp.ExitCode() != 0 || !queuedOf(resp) || opIDOf(resp) != id {
		t.Fatalf("with the Citadel away: %+v", resp)
	}
	if again := gate.Forward(t.Context(), call); opIDOf(again) != id { // the CLI sends it again
		t.Fatalf("the same request queued again: %+v", again)
	}
	if dones, _ := st.orc.Registry.Dones(t.Context(), st.dev.ID); len(dones) != 0 {
		t.Fatalf("the done was registered while the Citadel was away: %+v", dones)
	}
	// A command that needs the answer now is not queued: `mestre list` says
	// the Citadel is away, and that another machine is taking its place
	// while the Tower sees a promotion.
	list := func() localchan.Response {
		return gate.Forward(t.Context(), &localchan.Call{Request: localchan.Request{V: 1, ID: "list-1", Cmd: "list"}, Caller: call.Caller})
	}
	if resp := list(); resp.Error == nil || resp.Error.Code != localchan.CodeCitadelUnavailable || resp.ExitCode() != localchan.ExitUnavailable {
		t.Fatalf("list with the Citadel away: %+v", resp)
	}
	b.e.setFailover(true)
	if resp := list(); resp.Error == nil || resp.Error.Code != localchan.CodeCitadelInFailover || resp.ExitCode() != localchan.ExitUnavailable {
		t.Fatalf("list during the promotion: %+v", resp)
	}
	b.e.setFailover(false)

	r.start(a, clock.Real{})
	r.waitFor("the result of the done", func() bool { mu.Lock(); defer mu.Unlock(); return len(results) == 1 })
	r.waitFor("b to connect to the Citadel", func() bool { return b.connected(1) })
	r.waitFor("the outbox of b to be empty", func() bool {
		rows, err := protocolPending(t, b)
		return err == nil && rows == 0
	})
	dones, err := st.orc.Registry.Dones(t.Context(), st.dev.ID)
	if err != nil || len(dones) != 1 || dones[0].Text != "login implementado" {
		t.Fatalf("dones of the character: %+v, %v; want one", dones, err)
	}
	mu.Lock()
	if q := results[0]; q.OpID != id || !q.Response.OK || q.Cmd != "done" || q.AgentID != st.dev.ID {
		t.Errorf("result: %+v", q)
	}
	mu.Unlock()
	items, err := st.orc.Inbox.Store().All(t.Context(), st.dev.ID)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, it := range items {
		found = found || it.Kind == inbox.KindSystem && it.Refs.OpID == id
	}
	if !found {
		t.Errorf("the character got no item with the result of op %s: %+v", id, items)
	}
}
