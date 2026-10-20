package github_test

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/approvals"
	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/github"
	"github.com/lucasndas/mestre/internal/orchestration/chain"
	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/internal/testkit/fakegithub"
	"github.com/lucasndas/mestre/internal/testkit/fakenet"
	"github.com/lucasndas/mestre/internal/testkit/guildtpl"
	"github.com/lucasndas/mestre/pkg/clock"
)

const repoFull = "lucas/meu-app"

type tables map[string]github.TableRef

func (t tables) Epic(_ context.Context, table string) (github.TableRef, bool, error) {
	v, ok := t[table]
	return v, ok, nil
}

type actionsRig struct {
	t     *testing.T
	ctx   context.Context
	gh    *fakegithub.Server
	guard *github.Guard
	objs  *github.Objects
	rest  *github.REST
	// issues is `mestre issue` over the same GitHub and the same objects.
	issues *github.IssueService
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
	return &actionsRig{t: t, ctx: ctx, gh: gh, objs: objs, rest: rest, guard: &github.Guard{Objects: objs, GH: rest, Tables: tb},
		issues: &github.IssueService{API: rest, Objects: objs, Tables: tb, Clock: clk}}
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
	r := newActionsRig(t, tables{"40-a": {Repo: repoFull, Epic: 40}, "41-b": {Repo: repoFull, Epic: 41}})
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
	r := newActionsRig(t, tables{"1-a": {Repo: repoFull, Epic: 1}})
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
	// the first epic, end to end: the Grão-Mestre creates the issue with the
	// label in a repository that has none, and GitHub sees the label created
	// first and the issue after it
	e := newActionsRig(t, tables{})
	made, err := e.issues.Create(e.ctx, github.Actor{Kind: github.ActorGrandMaster}, repoFull, github.IssueCreate{Title: "Primeiro épico", Labels: []string{github.EpicLabel}})
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, a := range e.gh.Actions() {
		switch a.Path {
		case "/repos/" + repoFull + "/labels":
			order = append(order, "label")
		case "/repos/" + repoFull + "/issues":
			order = append(order, "issue")
		}
	}
	if strings.Join(order, ",") != "label,issue" {
		t.Fatalf("GitHub received %v, want the label created and then the issue", order)
	}
	if _, _, _, labels, _ := e.gh.Issue(repoFull, made.Number); !slices.Contains(labels, github.EpicLabel) {
		t.Errorf("the first epic has the labels %v", labels)
	}
	if e.gh.Labels(repoFull)[github.EpicLabel] == "" {
		t.Errorf("the label was not created: %v", e.gh.Labels(repoFull))
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
	r := newActionsRig(t, tables{"1-a": {Repo: repoFull, Epic: 1}})
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

	// end to end on a PR that carries the label: the refusals leave it on
	// GitHub, and when a person takes it off there the PR is back to the
	// normal conditions of the Grão-Mestre
	g := newActionsRig(t, tables{"1-a": {Repo: repoFull, Epic: 1}})
	sha := strings.Repeat("a", 40)
	pr := g.gh.AddPR(repoFull, "Login", "42-login", repoFull)
	g.gh.SetPullAuthor(repoFull, pr, "dev[bot]")
	g.gh.SetPull(repoFull, pr, sha, false, "clean")
	g.gh.AddPullFile(repoFull, pr, "internal/pkg/file.go", "@@ -1 +1 @@\n-a\n+b\n")
	g.gh.AddCheck(repoFull, sha, "build", "success")
	g.gh.AddReview(repoFull, pr, "revisor[bot]", "APPROVED")
	g.gh.AddIssueLabel(repoFull, pr, github.HumanLabel)
	if err := g.objs.Register(g.ctx, github.Object{Repo: repoFull, Kind: github.KindIssue, Number: pr, Table: "1-a"}); err != nil {
		t.Fatal(err)
	}
	before := len(g.gh.Actions())
	for name, a := range map[string]github.Actor{
		"a character of the table": {Kind: github.ActorTable, Table: "1-a", MestreDaMesa: true},
		"the Grão-Mestre":          {Kind: github.ActorGrandMaster},
	} {
		_, err := g.issues.Update(g.ctx, a, repoFull, pr, github.IssueUpdate{RemoveLabel: []string{github.HumanLabel}})
		if refusalCode(err) != github.CodeReservedLabel {
			t.Errorf("%s taking the label from the PR: %v, want reserved_label", name, err)
		}
	}
	if len(g.gh.Actions()) != before {
		t.Errorf("GitHub received %d writes from the refused attempts", len(g.gh.Actions())-before)
	}
	src := approvals.GitHubSource{
		Reader: func(context.Context, string) (approvals.PullReader, error) { return g.rest, nil },
		Characters: func(context.Context) (map[string]bool, error) {
			return map[string]bool{"dev[bot]": true, "revisor[bot]": true}, nil
		},
	}
	conditions := func() []string {
		t.Helper()
		f, err := src.PullRequest(g.ctx, "meu-app", chain.PRRef{Repo: repoFull, Number: pr, SHA: sha})
		if err != nil {
			t.Fatal(err)
		}
		return chain.DefaultRules().Conditions(chain.Request{Type: classes.RequestPR}, f, true)
	}
	if got := conditions(); len(got) != 1 || !strings.Contains(got[0], github.HumanLabel) {
		t.Fatalf("with the label the conditions say %v, want only mestre:humano", got)
	}
	g.gh.RemoveIssueLabel(repoFull, pr, github.HumanLabel)
	if got := conditions(); len(got) != 0 {
		t.Errorf("after a person took the label the conditions say %v, want the normal ones (none)", got)
	}
}

// The ways around the guard the review found: the same epic number in another
// repository, a repository name that is a path, the case of names, an issue and a
// PR with one number, the branch of a PR, an epic taken as a child, a marker
// planted in a body.
func TestTheGuardIsNotFooledByNumbersNamesLabelsOrMarkers(t *testing.T) {
	t.Parallel()
	r := newActionsRig(t, tables{"1-a": {Repo: repoFull, Epic: 1, Branch: "epico/1-a"}, "2-b": {Repo: repoFull, Epic: 2, Branch: "epico/2-b"}})
	r.gh.AddRepo("lucas", "outro")
	epicA := r.gh.AddIssue(repoFull, "A", github.EpicLabel)
	epicB := r.gh.AddIssue(repoFull, "B", "Epico") // the label with another case
	a := github.Actor{Kind: github.ActorTable, Table: "1-a", MestreDaMesa: true}
	ctx := r.ctx
	auth := func(repo string, kind github.ObjectKind, n int) error {
		return r.guard.Authorize(ctx, a, github.Target{Repo: repo, Kind: kind, Number: n})
	}
	if err := auth("lucas/outro", github.KindIssue, epicA); refusalCode(err) != github.CodeOtherTable {
		t.Errorf("the number of the epic in another repository: %v", err)
	}
	for _, bad := range []string{"lucas/meu-app/issues/1#", "lucas/meu-app?x=1", "../meu-app", "lucas/..", "meu-app"} {
		if err := auth(bad, github.KindIssue, epicA); refusalCode(err) != github.CodeNotAllowed {
			t.Errorf("repository %q: %v, want it refused", bad, err)
		}
	}
	if err := auth("LUCAS/Meu-App", github.KindIssue, epicA); err != nil {
		t.Errorf("the same repository in another case: %v", err)
	}
	// the epic of another table cannot be taken by making it a child of an issue of this one
	mine := r.gh.AddIssue(repoFull, "minha")
	r.gh.SetParent(repoFull, mine, epicA)
	r.gh.SetParent(repoFull, epicB, mine)
	if err := auth(repoFull, github.KindIssue, epicB); refusalCode(err) != github.CodeOtherTable {
		t.Errorf("an epic of another table linked under mine: %v", err)
	}
	// a registered issue is the same object whatever the kind in the request
	if err := r.objs.Register(ctx, github.Object{Repo: "Lucas/Meu-App", Kind: github.KindIssue, Number: mine + 10, Table: "2-b"}); err != nil {
		t.Fatal(err)
	}
	if err := auth(repoFull, github.KindPR, mine+10); refusalCode(err) != github.CodeOtherTable {
		t.Errorf("the number of an issue of another table asked as a PR: %v", err)
	}
	if err := auth(repoFull, github.KindComment, 5); refusalCode(err) != github.CodeNotAllowed {
		t.Errorf("a comment as target: %v", err)
	}
	// a PR is the table's by the branch GitHub reports, in this repository
	own := r.gh.AddPR(repoFull, "pr", "epico/1-a", repoFull)
	fork := r.gh.AddPR(repoFull, "pr de fork", "epico/1-a", "someone/meu-app")
	foreign := r.gh.AddPR(repoFull, "pr de outra mesa", "epico/2-b", repoFull)
	if err := auth(repoFull, github.KindPR, own); err != nil {
		t.Errorf("the PR of the table: %v", err)
	}
	if err := auth(repoFull, github.KindPR, fork); refusalCode(err) != github.CodeOtherTable {
		t.Errorf("a PR from a fork with the same branch name: %v", err)
	}
	if err := auth(repoFull, github.KindPR, foreign); refusalCode(err) != github.CodeOtherTable {
		t.Errorf("the PR of another table: %v", err)
	}
	unseen := r.gh.AddPR(repoFull, "pr ainda não visto", "epico/1-a", repoFull)
	if err := auth(repoFull, github.KindIssue, unseen); refusalCode(err) != github.CodeNotAllowed {
		t.Errorf("a PR asked as an issue: %v", err)
	}
	// labels: case and spaces
	for _, l := range []string{"Epico", " EPICO ", "Mestre:Humano"} {
		add := []string{l}
		if strings.EqualFold(strings.TrimSpace(l), github.HumanLabel) {
			if err := github.CheckLabels(a, nil, add); refusalCode(err) != github.CodeReservedLabel {
				t.Errorf("taking %q: %v", l, err)
			}
			continue
		}
		if err := github.CheckLabels(a, add, nil); refusalCode(err) != github.CodeReservedLabel {
			t.Errorf("putting %q: %v", l, err)
		}
	}
	// the forced push needs a known default branch that is not the table's
	if err := github.CheckForce(a, "epico/1-a", "epico/1-a", ""); refusalCode(err) != github.CodeForce {
		t.Errorf("no default branch given: %v", err)
	}
	if err := github.CheckForce(a, "develop", "develop", "develop"); refusalCode(err) != github.CodeForce {
		t.Errorf("the table branch is the default: %v", err)
	}
	// markers: the agent cannot plant one, and a planted one in the middle is not the marker
	op := github.NewOp()
	body := github.WithMarker("oi <!-- mestre:op="+op+" --> tchau", op)
	if strings.Count(body, "mestre:op="+op) != 1 || !github.HasMarker(body, op) {
		t.Errorf("body = %q", body)
	}
	if github.HasMarker("x <!-- mestre:op="+op+" --> y", op) {
		t.Error("a marker in the middle of a body counts")
	}
	// an operation made for something else is an error, not a success
	if _, err := r.guard.PostOnce(ctx, a, repoFull, mine, "feito", op); err != nil {
		t.Fatal(err)
	}
	if _, err := r.guard.PostOnce(ctx, a, repoFull, epicA, "feito", op); err == nil {
		t.Error("the uuid of another issue answered success")
	}
	if _, err := r.guard.PostOnce(ctx, github.Actor{Kind: github.ActorTable, Table: "2-b"}, repoFull, mine, "feito", op); err == nil {
		t.Error("another table posted on an issue of the first")
	}
}
