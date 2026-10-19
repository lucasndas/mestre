package ghcli_test

import (
	"context"
	"testing"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/decrees"
	"github.com/lucasndas/mestre/internal/providers"
	"github.com/lucasndas/mestre/internal/users"
)

// CA-DEC-09
func TestADecreeThatFreesThePushIsFulfilledByTheEngineAndRegisteredInTheDecree(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	svc, err := users.New(users.Options{DB: r.db, Clock: r.clk, PIN: users.PINCost{MemoryKiB: 8, Time: 1, Threads: 1}})
	if err != nil {
		t.Fatal(err)
	}
	owner, err := svc.CreateOwner(r.ctx, "Lucas")
	if err != nil {
		t.Fatal(err)
	}
	ana, err := svc.Create(r.ctx, owner, users.NewUser{Name: "Ana", Level: users.Administrator, Workspaces: []string{"meu-app"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.SetPIN(r.ctx, ana.ID, "482913"); err != nil {
		t.Fatal(err)
	}
	ds, err := decrees.New(decrees.Options{DB: r.db, Clock: r.clk, PINs: svc})
	if err != nil {
		t.Fatal(err)
	}
	r.h.Executed = ds.Fulfilled

	// "o Dev pode enviar a branch da mesa": kept, with the PIN of the author
	p := decrees.Proposal{Author: ana.ID, Kind: decrees.KindRule, Workspace: "meu-app", Target: decrees.Target{Kind: decrees.TargetClass, Name: "qa"},
		Effect: decrees.Effect{Kind: decrees.EffectPermission, Allow: &classes.Grant{Commands: []string{"git push *"}}}, Text: "o dev pode enviar a branch da mesa"}
	pend, err := ds.Propose(r.ctx, p)
	if err != nil || !pend.PIN {
		t.Fatalf("%+v, %v", pend, err)
	}
	req, err := ds.BeginPIN(r.ctx, pend.ID, ana.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.CheckPIN(r.ctx, req.ID, "482913"); err != nil {
		t.Fatal(err)
	}
	d, err := ds.Confirm(r.ctx, pend.ID, ana.ID, req.ID)
	if err != nil {
		t.Fatal(err)
	}

	// the hook still denies the git push of the agent
	perms, err := classes.ParsePermissions([]byte("version: 1\nfiles: {read: [\"**\"], write: [\"**\"]}\ncommands: {allow: [\"git *\"], default: deny}\ntools: {default: deny}\n"))
	if err != nil {
		t.Fatal(err)
	}
	decs, err := ds.Decrees(r.ctx, r.chars["qa"])
	if err != nil || len(decs) != 1 {
		t.Fatalf("%+v, %v", decs, err)
	}
	rules, err := classes.Build(classes.Layers{AgentID: "qa", Class: "qa", Permissions: perms, Decrees: decs, Now: r.clk.Now()})
	if err != nil {
		t.Fatal(err)
	}
	ev := providers.Event{Version: providers.EventVersion, Event: providers.EventPreTool, AgentID: "qa",
		Tool: &providers.Tool{NativeName: "Bash", Kind: providers.KindShell, Command: "git push origin 42-login"}}
	if got := classes.Decide(context.Background(), classes.Env{AgentID: "qa", Worktree: t.TempDir()}, rules, ev); got.Verdict != classes.Deny {
		t.Fatalf("the push of the agent: %+v", got)
	}

	// the engine sends the branch when the character asks, and registers it in the decree
	pr := prOfTheTable(t, r)
	_ = pr
	if got := r.run("qa", "pr update", map[string]any{"title": "Novo título"}); !got.OK {
		t.Fatalf("pr update: %+v", got)
	}
	if len(r.pushes()) != 1 {
		t.Fatalf("pushes = %q", r.pushes())
	}
	uses, err := ds.Uses(r.ctx, d.Number)
	if err != nil || len(uses) != 1 || uses[0].Character != "qa" || uses[0].Action != "mestre pr update" {
		t.Fatalf("uses %+v, %v", uses, err)
	}
	// a character of another class has no decree to register in
	if got := r.run("rev", "pr update", map[string]any{"title": "Outro"}); !got.OK {
		t.Fatalf("pr update: %+v", got)
	}
	if uses, _ := ds.Uses(r.ctx, d.Number); len(uses) != 1 {
		t.Fatalf("a decree of another class registered a use: %+v", uses)
	}

	// what nobody can do is refused before the confirmation
	bad := decrees.Proposal{Author: ana.ID, Kind: decrees.KindRule, Workspace: "meu-app", Target: decrees.Target{Kind: decrees.TargetClass, Name: "qa"},
		Effect: decrees.Effect{Kind: decrees.EffectPolicy, Setting: "revisao_obrigatoria", Loosens: true}, Text: "o dev pode ignorar a revisão obrigatória"}
	if _, err := ds.Propose(r.ctx, bad); err == nil {
		t.Fatal("a decree to ignore the required review was proposed")
	}
}
