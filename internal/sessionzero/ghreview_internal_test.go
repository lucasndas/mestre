package sessionzero

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/github"
	"github.com/mestre-ia/mestre/internal/orchestration/workspaces"
	"github.com/mestre-ia/mestre/internal/testkit/fakegithub"
	"github.com/mestre-ia/mestre/internal/testkit/fakenet"
	"github.com/mestre-ia/mestre/pkg/clock"
)

type ghRig struct {
	gh     *fakegithub.Server
	review GitHubReview
	svc    *Service
	repo   workspaces.Repo
	grand  *github.REST
}

func newGHRig(t *testing.T, rules ...fakegithub.Rule) *ghRig {
	t.Helper()
	gh, err := fakegithub.New(clock.NewFake(time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)), t)
	if err != nil {
		t.Fatal(err)
	}
	gh.AddRepo("lucas", "meu-app")
	gh.IssueToken("ghs_mestre", "mestre")
	gh.IssueToken("ghs_grao", "mestre-grao-mestre")
	gh.SetBranchRules("lucas/meu-app", "main", rules...)
	client := func(tok string) *github.REST {
		return &github.REST{BaseURL: "https://api.github.com", HTTP: &http.Client{Transport: fakenet.HandlerTransport(gh)},
			Token: func(context.Context, string) (string, error) { return tok, nil }}
	}
	grand := client("ghs_grao")
	review := GitHubReview{Probe: github.ReviewProbe{Mestre: client("ghs_mestre"), GrandMaster: grand}}
	return &ghRig{gh: gh, review: review, grand: grand, repo: workspaces.Repo{Owner: "lucas", Name: "meu-app"},
		svc: &Service{o: Options{AppReview: review}}}
}

func oneApproval() fakegithub.Rule {
	return fakegithub.Rule{Type: "pull_request", Parameters: map[string]any{"required_approving_review_count": 1}}
}

// CA-GH-APROV-01
func TestTheApprovalTestCountsTheReviewOfTheAppAndLeavesNothingBehind(t *testing.T) {
	t.Parallel()
	r := newGHRig(t, oneApproval())
	w := workspaces.Workspace{Repo: r.repo, DefaultBranch: "main"}
	rules, err := r.review.ReadRules(t.Context(), r.repo, "main")
	if err != nil || !rules.RequiresReview {
		t.Fatalf("rules = %+v, %v; want a review required", rules, err)
	}
	result, err := r.svc.probeReview(t.Context(), w)
	if err != nil || result != ResultCounts {
		t.Fatalf("probeReview = %q, %v; want %q", result, err, ResultCounts)
	}
	pull, err := r.grand.PullData(t.Context(), "lucas/meu-app", 1)
	if err != nil || pull.State != "closed" || pull.Merged {
		t.Fatalf("the test pull request = %+v, %v; want closed and not merged", pull, err)
	}
	if _, err := r.grand.RefSHA(t.Context(), "lucas/meu-app", github.TestBranch); !errors.Is(err, github.ErrNotFound) {
		t.Fatalf("the test branch is still there: %v", err)
	}
	if _, ok := r.gh.File("lucas/meu-app", "main", github.TestFile); ok {
		t.Fatal("the test file reached the main branch")
	}
}

func TestTheApprovalTestSaysTheReviewDoesNotCountWhenTheRepositoryIgnoresIt(t *testing.T) {
	t.Parallel()
	r := newGHRig(t, oneApproval())
	r.gh.SetAppReviewsCount("lucas/meu-app", false)
	result, err := r.svc.probeReview(t.Context(), workspaces.Workspace{Repo: r.repo, DefaultBranch: "main"})
	if err != nil || result != ResultDoesNotCount {
		t.Fatalf("probeReview = %q, %v; want %q", result, err, ResultDoesNotCount)
	}
	if _, err := r.grand.RefSHA(t.Context(), "lucas/meu-app", github.TestBranch); !errors.Is(err, github.ErrNotFound) {
		t.Fatalf("the test branch is still there after nao_conta: %v", err)
	}
}

func TestTheApprovalTestIsNotRunWhenNoRuleAsksForAReview(t *testing.T) {
	t.Parallel()
	r := newGHRig(t)
	rules, err := r.review.ReadRules(t.Context(), r.repo, "main")
	if err != nil || rules.RequiresReview {
		t.Fatalf("rules = %+v, %v; want no requirement", rules, err)
	}
	if got := len(r.gh.Actions()); got != 0 {
		t.Fatalf("%d writes with no requirement", got)
	}
}

// A test branch left by a run that broke is the Mestre's own: the next run
// clears it (GitHub closes the pull request of a branch that is deleted)
// instead of failing for good.
func TestTheApprovalTestClearsABranchLeftByAnEarlierRun(t *testing.T) {
	t.Parallel()
	r := newGHRig(t, oneApproval())
	if _, err := r.review.OpenTestPR(t.Context(), r.repo, "main"); err != nil {
		t.Fatal(err)
	}
	result, err := r.svc.probeReview(t.Context(), workspaces.Workspace{Repo: r.repo, DefaultBranch: "main"})
	if err != nil || result != ResultCounts {
		t.Fatalf("probeReview = %q, %v; want %q", result, err, ResultCounts)
	}
	if old, err := r.grand.PullData(t.Context(), "lucas/meu-app", 1); err != nil || old.State != "closed" {
		t.Fatalf("the pull request of the earlier run = %+v, %v; want it closed", old, err)
	}
}
