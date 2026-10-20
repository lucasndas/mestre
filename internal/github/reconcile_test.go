package github_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/github"
	"github.com/lucasndas/mestre/pkg/clock"
)

// Part of CA-CIDADELA-12: after the swap the new Citadel reads what the Apps of the
// Guild made in the last 30 minutes and records the markers it does not know, so
// the same operation in its queue, sent again, makes no second comment.
func TestTheReconciliationRecordsWhatTheOldCitadelMadeAndTheResendingMakesNoSecondComment(t *testing.T) {
	t.Parallel()
	r := newActionsRig(t, tables{"1-a": {Repo: repoFull, Epic: 1}})
	clk := r.objs.Clock.(*clock.Fake)
	epic := r.gh.AddIssue(repoFull, "épico", github.EpicLabel)
	if err := r.objs.Register(r.ctx, github.Object{Repo: repoFull, Kind: github.KindIssue, Number: epic, Table: "1-a"}); err != nil {
		t.Fatal(err)
	}
	// older than the window: not read
	if _, err := r.rest.PostComment(r.ctx, repoFull, epic, github.WithMarker("antigo", "op-old")); err != nil {
		t.Fatal(err)
	}
	clk.Advance(github.ReconcileWindow + time.Minute)
	// the old Citadel created this one with its marker and fell before replicating it
	made, err := r.rest.PostComment(r.ctx, repoFull, epic, github.WithMarker("feito", "op-lost"))
	if err != nil {
		t.Fatal(err)
	}
	// a person who writes the marker of an operation is not the Guild
	r.gh.AddIssueComment(repoFull, epic, "ana", github.WithMarker("forjado", "op-forged"))
	rec := &github.Reconciler{Objects: r.objs, API: r.rest, Clock: clk,
		Repos: func(context.Context) ([]string, error) { return []string{repoFull}, nil }}
	if err := rec.Reconcile(r.ctx); err != nil {
		t.Fatal(err)
	}
	obj, found, _ := r.objs.ByOp(r.ctx, "op-lost")
	if !found || obj.GitHubID != made.ID || obj.Table != "1-a" || obj.ParentNumber != epic || obj.Kind != github.KindComment {
		t.Fatalf("the comment of the old Citadel is recorded as %+v (%v)", obj, found)
	}
	for _, op := range []string{"op-old", "op-forged"} {
		if _, found, _ := r.objs.ByOp(r.ctx, op); found {
			t.Errorf("%s was recorded", op)
		}
	}
	// the queue of the new Citadel sends the same operation again: one comment only
	before := len(r.gh.Comments(repoFull, epic))
	a := github.Actor{Kind: github.ActorTable, Table: "1-a"}
	c, err := r.guard.PostOnce(r.ctx, a, repoFull, epic, "feito", "op-lost")
	if err != nil || c.ID != made.ID {
		t.Fatalf("the resending = %+v, %v; want the comment that exists", c, err)
	}
	if after := len(r.gh.Comments(repoFull, epic)); after != before {
		t.Fatalf("the resending made %d comments", after-before)
	}
	// again: nothing changes
	if err := rec.Reconcile(r.ctx); err != nil {
		t.Fatal(err)
	}
}

func TestTheReconciliationFailsClosed(t *testing.T) {
	t.Parallel()
	r := newActionsRig(t, tables{})
	rec := &github.Reconciler{Objects: r.objs, API: r.rest, Clock: r.objs.Clock,
		Repos: func(context.Context) ([]string, error) { return []string{repoFull}, nil }}
	r.gh.FailNextRead(http.StatusBadGateway, nil)
	if err := rec.Reconcile(r.ctx); err == nil {
		t.Fatal("a failed reading was taken as reconciled")
	}
	if err := rec.Reconcile(r.ctx); err != nil {
		t.Fatalf("the next try: %v", err)
	}
}
