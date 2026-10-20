package github_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/github"
	"github.com/lucasndas/mestre/internal/testkit/fakegithub"
	"github.com/lucasndas/mestre/internal/testkit/fakenet"
	"github.com/lucasndas/mestre/pkg/clock"
)

// mergeRig is a PR of the double and the REST client of the Grão-Mestre.
func mergeRig(t *testing.T) (*github.REST, *fakegithub.Server, int, string) {
	t.Helper()
	gh, err := fakegithub.New(clock.NewFake(time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)), t)
	if err != nil {
		t.Fatal(err)
	}
	gh.AddRepo("lucas", "meu-app")
	gh.IssueToken("ghs_tok", "grand_master")
	rest := &github.REST{BaseURL: "https://api.github.com", HTTP: &http.Client{Transport: fakenet.HandlerTransport(gh)},
		Token: func(context.Context, string) (string, error) { return "ghs_tok", nil }}
	n := gh.AddPR("lucas/meu-app", "Login", "mestre/1-login", "lucas/meu-app")
	return rest, gh, n, "lucas/meu-app"
}

// Part of CA-CLI-GM-04: the merge goes with the commit that was approved, with the method
// asked, and GitHub's answer is the merge commit; the PR then shows as merged.
func TestMergePullSendsTheApprovedCommitAndTheMethodAndReturnsTheMergeCommit(t *testing.T) {
	t.Parallel()
	rest, gh, n, repo := mergeRig(t)
	head := "1111111111111111111111111111111111111111"
	gh.SetPull(repo, n, head, false, "clean")
	res, err := rest.MergePull(t.Context(), repo, n, head, "")
	if err != nil || !res.Merged || len(res.SHA) != 40 {
		t.Fatalf("merge = %+v, %v", res, err)
	}
	if got := gh.Merges(repo); len(got) != 1 || got[0].Number != n || got[0].SHA != head || got[0].Method != "merge" {
		t.Errorf("GitHub got %+v; want the approved commit and the method merge", got)
	}
	pr, err := rest.PullData(t.Context(), repo, n)
	if err != nil || !pr.Merged {
		t.Errorf("the PR after the merge = %+v, %v", pr, err)
	}
	// a PR that is merged already is not mergeable
	if _, err := rest.MergePull(t.Context(), repo, n, head, github.MergeMethodSquash); !errors.Is(err, github.ErrNotMergeable) {
		t.Errorf("merging twice: %v; want not mergeable", err)
	}
}

// Part of CA-CLI-GM-04: a PR that moved since the approval is not merged, a rule of the
// branch is told apart from a PR that can not be merged, and a method or a commit
// that is not given is refused before anything is sent.
func TestMergePullRefusesAMovedHeadARuleAConflictAndABadCall(t *testing.T) {
	t.Parallel()
	rest, gh, n, repo := mergeRig(t)
	approved, moved := "1111111111111111111111111111111111111111", "2222222222222222222222222222222222222222"
	gh.SetPull(repo, n, moved, false, "clean")
	if _, err := rest.MergePull(t.Context(), repo, n, approved, ""); !errors.Is(err, github.ErrHeadChanged) {
		t.Errorf("head moved: %v", err)
	}
	gh.SetPull(repo, n, approved, false, "blocked")
	if _, err := rest.MergePull(t.Context(), repo, n, approved, ""); !errors.Is(err, github.ErrMergeProtected) {
		t.Errorf("blocked by a rule: %v", err)
	}
	gh.SetPull(repo, n, approved, false, "dirty")
	if _, err := rest.MergePull(t.Context(), repo, n, approved, ""); !errors.Is(err, github.ErrNotMergeable) || errors.Is(err, github.ErrMergeProtected) {
		t.Errorf("conflict: %v", err)
	}
	gh.SetPull(repo, n, approved, true, "clean")
	if _, err := rest.MergePull(t.Context(), repo, n, approved, ""); !errors.Is(err, github.ErrNotMergeable) {
		t.Errorf("draft: %v", err)
	}
	if _, err := rest.MergePull(t.Context(), repo, n, "", ""); err == nil {
		t.Error("a merge without the approved commit was sent")
	}
	if _, err := rest.MergePull(t.Context(), repo, n, approved, "fast-forward"); err == nil {
		t.Error("a method of merge that does not exist was sent")
	}
	if got := gh.Merges(repo); len(got) != 0 {
		t.Errorf("GitHub merged %+v although every call was refused", got)
	}
	if _, err := rest.MergePull(t.Context(), repo, 999, approved, ""); !errors.Is(err, github.ErrNotFound) {
		t.Errorf("a PR that does not exist: %v", err)
	}
}
