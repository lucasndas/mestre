package github_test

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/github"
	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/internal/testkit/fakegithub"
	"github.com/lucasndas/mestre/internal/testkit/fakenet"
	"github.com/lucasndas/mestre/internal/testkit/guildtpl"
	"github.com/lucasndas/mestre/pkg/clock"
)

const repoFull = "lucas/meu-app"

type tables map[string]struct {
	epic   int
	branch string
}

func (t tables) Epic(_ context.Context, table string) (int, string, bool, error) {
	v, ok := t[table]
	return v.epic, v.branch, ok, nil
}

type actionsRig struct {
	t     *testing.T
	ctx   context.Context
	gh    *fakegithub.Server
	guard *github.Guard
	objs  *github.Objects
}

func newActionsRig(t *testing.T, tb tables) *actionsRig {
	t.Helper()
	ctx := t.Context()
	clk := clock.NewFake(time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC))
	db, err := guildtpl.Open(ctx, filepath.Join(t.TempDir(), "mestre.db"), store.Options{Synchronous: "OFF", Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	gh, err := fakegithub.New(clk, t)
	if err != nil {
		t.Fatal(err)
	}
	gh.AddRepo("lucas", "meu-app")
	gh.IssueToken("ghs_tok", "grand_master")
	rest := &github.REST{BaseURL: "https://api.github.com", HTTP: &http.Client{Transport: fakenet.HandlerTransport(gh)}, Token: func(context.Context, string) (string, error) { return "ghs_tok", nil }}
	objs := &github.Objects{DB: db, Clock: clk}
	return &actionsRig{t: t, ctx: ctx, gh: gh, objs: objs, guard: &github.Guard{Objects: objs, GH: rest, Tables: tb}}
}

func refusalCode(err error) string {
	var r *github.Refusal
	if errors.As(err, &r) {
		return r.Code
	}
	return ""
}

// CA-GH-ACOES-01
func TestATableCannotTouchAnIssueOfAnotherTable(t *testing.T) {
	t.Parallel()
	r := newActionsRig(t, tables{"40-a": {epic: 40}, "41-b": {epic: 41}})
	r.gh.AddIssue(repoFull, "épico A", github.EpicLabel)
	r.gh.AddIssue(repoFull, "épico B", github.EpicLabel)
	n := r.gh.AddIssue(repoFull, "uma missão")
	if err := r.objs.Register(r.ctx, github.Object{Repo: repoFull, Kind: github.KindIssue, Number: n, Table: "40-a"}); err != nil {
		t.Fatal(err)
	}
	a := github.Actor{Kind: github.ActorTable, Table: "41-b"}
	err := r.guard.Authorize(r.ctx, a, github.Target{Repo: repoFull, Kind: github.KindIssue, Number: n})
	var ref *github.Refusal
	if !errors.As(err, &ref) || ref.Code != github.CodeOtherTable || ref.Message == "" || !contains(ref.Message, "#40") {
		t.Fatalf("authorize = %v, want other_table citing the epic #40", err)
	}
	if err := r.guard.Authorize(r.ctx, github.Actor{Kind: github.ActorTable, Table: "40-a"}, github.Target{Repo: repoFull, Kind: github.KindIssue, Number: n}); err != nil {
		t.Fatalf("the owner table: %v", err)
	}
	if err := r.guard.Authorize(r.ctx, github.Actor{Kind: github.ActorGrandMaster}, github.Target{Repo: repoFull, Kind: github.KindIssue, Number: n}); err != nil {
		t.Fatalf("the Grão-Mestre: %v", err)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// CA-GH-ACOES-02
func TestAnIssueOfAPersonUnderTheEpicIsClimbedToAndRegistered(t *testing.T) {
	t.Parallel()
	r := newActionsRig(t, tables{"1-a": {epic: 1}})
	epic := r.gh.AddIssue(repoFull, "épico", github.EpicLabel)
	mid := r.gh.AddIssue(repoFull, "issue de uma pessoa")
	leaf := r.gh.AddIssue(repoFull, "filha")
	other := r.gh.AddIssue(repoFull, "solta")
	r.gh.SetParent(repoFull, mid, epic)
	r.gh.SetParent(repoFull, leaf, mid)
	a := github.Actor{Kind: github.ActorTable, Table: "1-a"}
	if err := r.guard.Authorize(r.ctx, a, github.Target{Repo: repoFull, Kind: github.KindIssue, Number: leaf}); err != nil {
		t.Fatalf("a grandchild of the epic: %v", err)
	}
	obj, found, err := r.objs.Get(r.ctx, repoFull, github.KindIssue, leaf)
	if err != nil || !found || obj.Table != "1-a" {
		t.Fatalf("registered = %+v, %v, %v; want the issue registered for the table", obj, found, err)
	}
	if err := r.guard.Authorize(r.ctx, a, github.Target{Repo: repoFull, Kind: github.KindIssue, Number: other}); refusalCode(err) != github.CodeNotInEpic {
		t.Fatalf("an issue outside the tree: %v, want not_in_epic", err)
	}
}

// CA-GH-ACOES-03
func TestATableCannotPutOrTakeTheEpicLabel(t *testing.T) {
	t.Parallel()
	a := github.Actor{Kind: github.ActorTable, Table: "1-a", MestreDaMesa: true}
	if err := github.CheckLabels(a, []string{"bug", github.EpicLabel}, nil); refusalCode(err) != github.CodeReservedLabel {
		t.Fatalf("adding epico = %v", err)
	}
	if err := github.CheckLabels(a, nil, []string{github.EpicLabel}); refusalCode(err) != github.CodeReservedLabel {
		t.Fatalf("removing epico = %v", err)
	}
	if err := github.CheckLabels(github.Actor{Kind: github.ActorGrandMaster}, []string{github.EpicLabel}, nil); err != nil {
		t.Fatalf("the Grão-Mestre puts epico: %v", err)
	}
}

// CA-GH-ACOES-04
func TestTheEpicLabelIsCreatedBeforeTheFirstEpic(t *testing.T) {
	t.Parallel()
	r := newActionsRig(t, tables{})
	if len(r.gh.Labels(repoFull)) != 0 {
		t.Fatal("the repository started with labels")
	}
	if err := r.guard.EnsureLabels(r.ctx, repoFull, []string{github.EpicLabel}); err != nil {
		t.Fatal(err)
	}
	if got := r.gh.Labels(repoFull); got[github.EpicLabel] != "Épico do Mestre: uma mesa" {
		t.Fatalf("labels = %v", got)
	}
	acts := len(r.gh.Actions())
	if err := r.guard.EnsureLabels(r.ctx, repoFull, []string{github.EpicLabel}); err != nil || len(r.gh.Actions()) != acts {
		t.Fatalf("an existing label is reused: %v", err)
	}
}

// CA-GH-ACOES-05
func TestAForcedPushIsOnlyOfTheMestreDaMesaOnTheBranchOfTheTable(t *testing.T) {
	t.Parallel()
	mestre := github.Actor{Kind: github.ActorTable, Table: "1-a", MestreDaMesa: true}
	dev := github.Actor{Kind: github.ActorTable, Table: "1-a"}
	if err := github.CheckForce(mestre, "epico/1-a", "epico/1-a", "main"); err != nil {
		t.Fatalf("the Mestre da mesa on the branch of the table: %v", err)
	}
	for name, c := range map[string]struct {
		a      github.Actor
		branch string
	}{
		"another character":     {dev, "epico/1-a"},
		"the main branch":       {mestre, "main"},
		"another branch":        {mestre, "epico/9-z"},
		"the Grão-Mestre":       {github.Actor{Kind: github.ActorGrandMaster}, "epico/1-a"},
		"a branch with no name": {mestre, ""},
	} {
		if err := github.CheckForce(c.a, c.branch, "epico/1-a", "main"); refusalCode(err) != github.CodeForce {
			t.Errorf("%s: %v, want force_not_allowed", name, err)
		}
	}
}

// CA-GH-ACOES-10
func TestAResentCommentFindsItsMarkerAndIsNotCreatedAgain(t *testing.T) {
	t.Parallel()
	r := newActionsRig(t, tables{"1-a": {epic: 1}})
	issue := r.gh.AddIssue(repoFull, "épico", github.EpicLabel)
	a := github.Actor{Kind: github.ActorTable, Table: "1-a"}
	op := github.NewOp()
	// the first sending reached GitHub and the engine fell before it recorded it
	rest := &github.REST{BaseURL: "https://api.github.com", HTTP: &http.Client{Transport: fakenet.HandlerTransport(r.gh)}, Token: func(context.Context, string) (string, error) { return "ghs_tok", nil }}
	if _, err := rest.PostComment(r.ctx, repoFull, issue, github.WithMarker("feito", op)); err != nil {
		t.Fatal(err)
	}
	c, err := r.guard.PostOnce(r.ctx, a, repoFull, issue, "feito", op)
	if err != nil || c.ID == 0 {
		t.Fatalf("resend = %+v, %v", c, err)
	}
	if got := r.gh.Comments(repoFull, issue); len(got) != 1 {
		t.Fatalf("%d comments on GitHub, want 1", len(got))
	}
	if obj, found, _ := r.objs.ByOp(r.ctx, op); !found || obj.GitHubID != c.ID || obj.Table != "1-a" {
		t.Fatalf("registered = %+v, %v", obj, found)
	}
	// and a sending again, now from the database
	if _, err := r.guard.PostOnce(r.ctx, a, repoFull, issue, "feito", op); err != nil || len(r.gh.Comments(repoFull, issue)) != 1 {
		t.Fatalf("a third sending created another comment: %v", err)
	}
}

// CA-GH-ACOES-14
func TestTheHumanLabelIsTakenOnlyByThePeople(t *testing.T) {
	t.Parallel()
	for name, a := range map[string]github.Actor{
		"a character of the table": {Kind: github.ActorTable, Table: "1-a", MestreDaMesa: true},
		"the Grão-Mestre":          {Kind: github.ActorGrandMaster},
	} {
		if err := github.CheckLabels(a, nil, []string{github.HumanLabel}); refusalCode(err) != github.CodeReservedLabel {
			t.Errorf("%s taking the label: %v, want reserved_label", name, err)
		}
	}
	if err := github.CheckLabels(github.Actor{Kind: github.ActorTable, Table: "1-a"}, []string{github.HumanLabel}, nil); refusalCode(err) != github.CodeReservedLabel {
		t.Errorf("a table putting the label: %v", err)
	}
	if err := github.CheckLabels(github.Actor{Kind: github.ActorGrandMaster}, []string{github.HumanLabel}, nil); err != nil {
		t.Errorf("the Grão-Mestre putting the label: %v", err)
	}
	if err := github.CheckLabels(github.Actor{Kind: github.ActorPerson}, nil, []string{github.HumanLabel}); err != nil {
		t.Errorf("a person taking the label: %v", err)
	}
	r := newActionsRig(t, tables{})
	if err := r.guard.EnsureLabels(r.ctx, repoFull, []string{github.HumanLabel}); err != nil {
		t.Fatal(err)
	}
	if r.gh.Labels(repoFull)[github.HumanLabel] != "Só uma pessoa aprova" {
		t.Fatalf("the label was not created: %v", r.gh.Labels(repoFull))
	}
}
