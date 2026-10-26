package github_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/github"
	"github.com/mestre-ia/mestre/pkg/clock"
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
	// another bot of the repository, with the marker at the end, is not the Guild; a
	// bot of the Guild that only quotes a marker in the middle does not make one
	r.gh.AddIssueComment(repoFull, epic, "dependabot[bot]", github.WithMarker("de outro bot", "op-other-bot"))
	r.gh.AddIssueComment(repoFull, epic, "grand_master[bot]", "cita <!-- mestre:op=op-quoted --> no meio\ne segue")
	rec := &github.Reconciler{Objects: r.objs, API: r.rest, Clock: clk,
		Repos:   func(context.Context) ([]string, error) { return []string{repoFull}, nil },
		Authors: func(context.Context) (map[string]bool, error) { return map[string]bool{"grand_master[bot]": true}, nil }}
	if err := rec.Reconcile(r.ctx); err != nil {
		t.Fatal(err)
	}
	obj, found, _ := r.objs.ByOp(r.ctx, "op-lost")
	if !found || obj.GitHubID != made.ID || obj.Table != "" || obj.ParentNumber != epic || obj.Kind != github.KindComment {
		t.Fatalf("the comment of the old Citadel is recorded as %+v (%v)", obj, found)
	}
	for _, op := range []string{"op-old", "op-forged", "op-other-bot", "op-quoted"} {
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
	// the Grão-Mestre sending the same operation again takes it as well
	if c, err := r.guard.PostOnce(r.ctx, github.Actor{Kind: github.ActorGrandMaster}, repoFull, epic, "feito", "op-lost"); err != nil || c.ID != made.ID {
		t.Fatalf("the resending by the Grão-Mestre = %+v, %v", c, err)
	}
	// again: nothing changes
	if err := rec.Reconcile(r.ctx); err != nil {
		t.Fatal(err)
	}
	if after := len(r.gh.Comments(repoFull, epic)); after != before {
		t.Fatalf("%d comments, was %d", after, before)
	}
}

func TestTheReconciliationFailsClosed(t *testing.T) {
	t.Parallel()
	r := newActionsRig(t, tables{})
	rec := &github.Reconciler{Objects: r.objs, API: r.rest, Clock: r.objs.Clock,
		Repos:   func(context.Context) ([]string, error) { return []string{repoFull}, nil },
		Authors: func(context.Context) (map[string]bool, error) { return map[string]bool{"grand_master[bot]": true}, nil }}
	r.gh.FailNextRead(http.StatusBadGateway, nil)
	if err := rec.Reconcile(r.ctx); err == nil {
		t.Fatal("a failed reading was taken as reconciled")
	}
	if err := rec.Reconcile(r.ctx); err != nil {
		t.Fatalf("the next try: %v", err)
	}
}

// A try after failures reads from the first moment, not from 30 minutes before
// the new try: what the old Citadel made does not leave the window while the
// reading keeps failing.
func TestTheWindowOfTheReconciliationIsAnchoredAtTheFirstTry(t *testing.T) {
	t.Parallel()
	r := newActionsRig(t, tables{})
	clk := r.objs.Clock.(*clock.Fake)
	epic := r.gh.AddIssue(repoFull, "épico", github.EpicLabel)
	rec := &github.Reconciler{Objects: r.objs, API: r.rest, Clock: clk,
		Repos:   func(context.Context) ([]string, error) { return []string{repoFull}, nil },
		Authors: func(context.Context) (map[string]bool, error) { return map[string]bool{"grand_master[bot]": true}, nil }}
	r.gh.FailNextRead(http.StatusBadGateway, nil)
	if err := rec.Reconcile(r.ctx); err == nil {
		t.Fatal("a failed reading was taken as reconciled")
	}
	if _, err := r.rest.PostComment(r.ctx, repoFull, epic, github.WithMarker("feito", "op-early")); err != nil {
		t.Fatal(err)
	}
	clk.Advance(github.ReconcileWindow + time.Minute) // the next try, long after, still reads from the first window
	if err := rec.Reconcile(r.ctx); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := r.objs.ByOp(r.ctx, "op-early"); !found {
		t.Fatal("the comment left the window because the try came later")
	}
}

// A Citadel that restarts long after the promotion reads the window of the
// epoch, not the last 30 minutes before the restart.
func TestTheWindowOfTheReconciliationEndsAtTheAnchorOfTheEpoch(t *testing.T) {
	t.Parallel()
	r := newActionsRig(t, tables{})
	clk := r.objs.Clock.(*clock.Fake)
	epic := r.gh.AddIssue(repoFull, "épico", github.EpicLabel)
	started := clk.Now()
	if _, err := r.rest.PostComment(r.ctx, repoFull, epic, github.WithMarker("feito", "op-before-the-swap")); err != nil {
		t.Fatal(err)
	}
	clk.Advance(45 * time.Minute) // the restart, past the 30 minutes
	rec := &github.Reconciler{Objects: r.objs, API: r.rest, Clock: clk,
		Anchor:  func(context.Context) (time.Time, error) { return started, nil },
		Repos:   func(context.Context) ([]string, error) { return []string{repoFull}, nil },
		Authors: func(context.Context) (map[string]bool, error) { return map[string]bool{"grand_master[bot]": true}, nil }}
	if err := rec.Reconcile(r.ctx); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := r.objs.ByOp(r.ctx, "op-before-the-swap"); !found {
		t.Fatal("the comment of the old Citadel was not read 45 minutes after the epoch began")
	}
}
