package app

import (
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/github"
)

// Part of CA-CIDADELA-12: the reconciliation the standard integration gives to the fence
// of a promoted Citadel reads the comment the old one made with its marker and
// did not replicate, and records it, so the sending again makes no second one.
func TestTheStandardIntegrationReconcilesWhatTheOldCitadelMadeOnGitHub(t *testing.T) {
	t.Parallel()
	c := newGHCompose(t)
	const repo, op = "lucas/meu_app", "op-lost"
	rest := c.run.gmRest(c.run.gmApp(c.ctx), issuePermissions)
	c.clk.Advance(time.Minute)
	made, err := rest.PostComment(c.ctx, repo, c.pr, github.WithMarker("feito", op))
	if err != nil {
		t.Fatal(err)
	}
	objs := &github.Objects{DB: c.db, Clock: c.clk}
	if _, found, _ := objs.ByOp(c.ctx, op); found {
		t.Fatal("the operation is known before the reconciliation")
	}
	if err := reconcilerOf(c.run, c.orc, nil)(c.ctx); err != nil {
		t.Fatal(err)
	}
	obj, found, _ := objs.ByOp(c.ctx, op)
	if !found || obj.GitHubID != made.ID || obj.Kind != github.KindComment || obj.ParentNumber != c.pr {
		t.Fatalf("recorded as %+v (%v)", obj, found)
	}
	// the sending again finds it and makes no second comment
	g := &github.Guard{Objects: objs, GH: rest, Tables: tableRefs{c.orc}}
	before := len(c.gh.Comments(repo, c.pr))
	if got, err := g.PostOnce(c.ctx, github.Actor{Kind: github.ActorGrandMaster}, repo, c.pr, "feito", op); err != nil || got.ID != made.ID {
		t.Fatalf("the resending = %+v, %v", got, err)
	}
	if after := len(c.gh.Comments(repo, c.pr)); after != before {
		t.Fatalf("the resending made %d comments", after-before)
	}
}

// The fence of `mestre run` gets the reconciliation only with an integration that
// has one and an orchestration to reconcile.
func TestTheFenceOfTheRunGetsTheReconciliationOfTheIntegration(t *testing.T) {
	t.Parallel()
	c := newGHCompose(t)
	if reconcilerOf(c.run, c.orc, nil) == nil {
		t.Fatal("the standard integration gave the fence no reconciliation")
	}
	if reconcilerOf(c.run, nil, nil) != nil || reconcilerOf(nil, c.orc, nil) != nil {
		t.Error("a reconciliation without an orchestration or an integration")
	}
}
