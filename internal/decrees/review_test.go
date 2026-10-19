package decrees_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/decrees"
	"github.com/lucasndas/mestre/internal/providers"
	"github.com/lucasndas/mestre/internal/users"
)

// The confirmation shows what will be stored: the workspace, the table and the scope.
func TestTheConfirmationNamesTheScopeThatWillBeStored(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	ana := w.person("Ana", users.Administrator, "loja", "blog")
	base := denyCmd(ana, "dev", "curl *")
	texts := map[string]bool{}
	for _, mod := range []func(*decrees.Proposal){
		func(p *decrees.Proposal) {},
		func(p *decrees.Proposal) { p.Mesa = "42-login" },
		func(p *decrees.Proposal) { p.Workspace = "blog" },
	} {
		p := base
		mod(&p)
		pend := mustPropose(t, w, p)
		if pend.Workspace != p.Workspace || pend.Mesa != p.Mesa {
			t.Fatalf("%+v", pend)
		}
		texts[pend.Text] = true
	}
	if len(texts) != 3 {
		t.Fatalf("proposals of different scopes read the same: %v", texts)
	}
	del := decrees.Proposal{Author: w.owner.ID, Kind: decrees.KindRule, Workspace: "loja", Scope: "padrao:x", Target: decrees.Target{Kind: decrees.TargetWorkspace, Name: "loja"},
		Effect: decrees.Effect{Kind: decrees.EffectDelegation, Setting: "configuracao_repositorio"}, Text: "delegue"}
	if ref := w.refusal(del); ref.Code != decrees.CodeDelegation {
		t.Fatalf("a scope the engine does not understand: %+v", ref)
	}
}

// A user who loses the workspace loses the decrees there.
func TestAnAuthorWhoLosesTheWorkspaceLosesTheDecreesThere(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	ana := w.person("Ana", users.Administrator, "loja")
	d := w.confirm(allowCmd(ana, "dev", "make a"))
	if _, err := w.users.Edit(w.ctx, w.owner, ana.ID, users.Edit{Workspaces: ptr([]string{"blog"})}); err != nil {
		t.Fatal(err)
	}
	if decs, _ := w.svc.Decrees(w.ctx, dev("d1")); len(decs) != 0 {
		t.Fatalf("a decree of an author who no longer acts there counts: %+v", decs)
	}
	if err := w.svc.InvalidateBy(w.ctx, ana.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := w.svc.Get(w.ctx, d.Number); got.State != decrees.Suspended {
		t.Fatalf("%s", got.State)
	}
}

// A locked Mestre confirms, revokes and reactivates nothing.
func TestALockedMestreConfirmsNothing(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	locked := false
	svc, err := decrees.New(decrees.Options{DB: w.db, Clock: w.clk, PINs: w.users, Locked: func(context.Context) bool { return locked }})
	if err != nil {
		t.Fatal(err)
	}
	ana := w.person("Ana", users.Administrator, "loja")
	pend, err := svc.Propose(w.ctx, denyCmd(ana, "dev", "curl *"))
	if err != nil {
		t.Fatal(err)
	}
	locked = true
	if _, err := svc.Confirm(w.ctx, pend.ID, ana.ID, ""); !errors.Is(err, decrees.ErrLocked) {
		t.Fatalf("%v", err)
	}
	locked = false
	d, err := svc.Confirm(w.ctx, pend.ID, ana.ID, "")
	if err != nil {
		t.Fatalf("the proposal stayed open: %v", err)
	}
	locked = true
	if err := svc.Revoke(w.ctx, d.Number, ana.ID, ""); !errors.Is(err, decrees.ErrLocked) {
		t.Fatalf("%v", err)
	}
	if err := svc.Reactivate(w.ctx, d.Number, w.owner.ID); !errors.Is(err, decrees.ErrLocked) {
		t.Fatalf("%v", err)
	}
}

func TestADecreeBoundToATargetIsOnlySpentByTheEngine(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	bia := w.person("Bia", users.Approver, "loja")
	w.heads.set("lucas/loja", 42, "abc")
	p := decrees.Proposal{Author: bia.ID, Kind: decrees.KindException, Workspace: "loja", Target: decrees.Target{Kind: decrees.TargetCharacter, Name: "d1"},
		Action: &classes.Action{Kind: providers.KindShell, Tool: "Bash", Command: "gh pr merge 42"}, Text: "aprovo o merge", RequestType: "merge",
		TargetRef: json.RawMessage(`{"repo":"lucas/loja","pr":42,"head":"abc","base":"main"}`)}
	w.confirm(p)
	if decs, _ := w.svc.Decrees(w.ctx, dev("d1")); len(decs) != 0 {
		t.Fatalf("an exception bound to a target reached the hook: %+v", decs)
	}
	p.TargetRef = json.RawMessage(`{"pr":1,"pr":2}`)
	if ref := w.refusal(p); ref.Code != decrees.CodeInvalid {
		t.Fatalf("%+v", ref)
	}
}

// The forms of the same thing are the same thing.
func TestTheInvariantsAndThePINReadTheFormsOfACommand(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	ana := w.person("Ana", users.Administrator, "loja")
	for _, c := range []string{"claude*", "*", "c*", "env claude", "sh -c claude", "/usr/bin/claude", "./claude", "npx claude", `"claude"`, "FOO=1 codex run", "mestre   vault get x"} {
		if ref := w.refusal(allowCmd(ana, "dev", c)); ref.Code != decrees.CodeInvariant {
			t.Errorf("%q: %+v", c, ref)
		}
	}
	for _, f := range []string{"**", ".claude/*", ".codex", "**/.mestre*", ".MESTRE/x"} {
		p := allowCmd(ana, "dev", "x")
		p.Effect = decrees.Effect{Kind: decrees.EffectPermission, Allow: &classes.Grant{Files: []string{f}}}
		if ref := w.refusal(p); ref.Code != decrees.CodeInvariant {
			t.Errorf("%q: %+v", f, ref)
		}
	}
	bia := w.person("Bia", users.Approver, "loja")
	for _, c := range []string{"env rm --recursive x", "/usr/bin/git push", "git  push", "GIT_DIR=x git push --force-with-lease", "find . -delete"} {
		pend, err := w.svc.Propose(w.ctx, decrees.Proposal{Author: bia.ID, Kind: decrees.KindException, Workspace: "loja", Target: decrees.Target{Kind: decrees.TargetCharacter, Name: "d1"},
			Action: &classes.Action{Kind: providers.KindShell, Tool: "Bash", Command: c}, Text: "libera"})
		if err != nil || !pend.PIN {
			t.Errorf("%q: %+v, %v", c, pend, err)
		}
	}
	// a rule with no allow and no deny does nothing: refused
	noop := allowCmd(ana, "dev", "x")
	noop.Effect = decrees.Effect{Kind: decrees.EffectPermission}
	noop.Action = &classes.Action{Kind: providers.KindShell, Command: "x"}
	if ref := w.refusal(noop); ref.Code != decrees.CodeInvalid {
		t.Fatalf("%+v", ref)
	}
}
