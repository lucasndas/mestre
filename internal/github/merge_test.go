package github_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/github"
	"github.com/mestre-ia/mestre/internal/testkit/fakegithub"
	"github.com/mestre-ia/mestre/internal/testkit/fakenet"
	"github.com/mestre-ia/mestre/pkg/clock"
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

// The merge goes with the commit that was approved, with the method
// asked, and GitHub's answer is the merge commit; the PR then shows as merged.
func TestMergePullSendsTheApprovedCommitAndTheMethodAndReturnsTheMergeCommit(t *testing.T) {
	t.Parallel()
	rest, gh, n, repo := mergeRig(t)
	head := approved
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
	// a repetition finds the PR merged at the approved commit: success without a second merge
	if res, err := rest.MergePull(t.Context(), repo, n, head, github.MergeMethodSquash); err != nil || !res.Recovered || len(gh.Merges(repo)) != 1 {
		t.Errorf("merging twice: %+v, %v", res, err)
	}
}

const (
	approved = "1111111111111111111111111111111111111111"
	moved    = "2222222222222222222222222222222222222222"
)

// A PR that moved since the approval is not merged, a rule of the
// branch is told apart from a conflict, an outdated branch, a base that moved and a
// method the repository does not allow, and a merge without the full hash of the commit,
// with a method that does not exist or for no PR is refused before anything is sent.
func TestMergePullTellsApartTheReasonsGitHubRefusesAndSendsNothingWhenTheCallIsBad(t *testing.T) {
	t.Parallel()
	rest, gh, n, repo := mergeRig(t)
	merge := func(sha, method string) error {
		_, err := rest.MergePull(t.Context(), repo, n, sha, method)
		return err
	}
	gh.SetPull(repo, n, moved, false, "clean")
	if err := merge(approved, ""); !errors.Is(err, github.ErrHeadChanged) {
		t.Errorf("head moved: %v", err)
	}
	for _, c := range []struct {
		state string
		draft bool
		want  error
	}{{"blocked", false, github.ErrMergeProtected}, {"dirty", false, github.ErrNotMergeable}, {"behind", false, github.ErrBranchOutdated}, {"clean", true, github.ErrNotMergeable}} {
		gh.SetPull(repo, n, approved, c.draft, c.state)
		if err := merge(approved, ""); !errors.Is(err, c.want) || (c.want == github.ErrNotMergeable && errors.Is(err, github.ErrMergeProtected)) {
			t.Errorf("state %q draft %v: %v; want %v", c.state, c.draft, err, c.want)
		}
	}
	gh.SetPull(repo, n, approved, false, "clean")
	gh.DisallowMergeMethod(repo, "merge")
	if err := merge(approved, ""); !errors.Is(err, github.ErrMethodNotAllowed) {
		t.Errorf("method not allowed: %v", err)
	}
	for _, c := range []struct {
		status int
		msg    string
		hdr    map[string]string
		want   error
	}{
		{405, "Base branch was modified. Review and try the merge again.", nil, github.ErrBaseChanged},
		{409, "Base branch was modified. Review and try the merge again.", nil, github.ErrBaseChanged},
		{405, "Waiting on code owner review from @ana.", nil, github.ErrMergeProtected},
		{403, "Resource not accessible by integration", nil, github.ErrForbidden},
		{403, "Repository rule violations found", nil, github.ErrMergeProtected},
	} {
		gh.FailMerges(repo, c.status, c.msg, c.hdr)
		if err := merge(approved, "squash"); !errors.Is(err, c.want) {
			t.Errorf("%d %q: %v; want %v", c.status, c.msg, err, c.want)
		}
	}
	gh.FailMerges(repo, 403, "You have exceeded a secondary rate limit.", map[string]string{"X-RateLimit-Remaining": "0"})
	var limit *github.LimitError
	if err := merge(approved, ""); !errors.As(err, &limit) || limit.RetryAfter < time.Second {
		t.Errorf("rate limit without Retry-After: %v; want a limit to wait for", err)
	}
	gh.FailMerges(repo, 0, "", nil)
	// bad calls: nothing is sent
	for _, bad := range []string{"", "main", "1111111", "ABCDEF0123456789ABCDEF0123456789ABCDEF01", approved + " ", "1111111111111111111111111111111111111111;"} {
		if merge(bad, "") == nil {
			t.Errorf("the commit %q was sent", bad)
		}
	}
	if merge(approved, "fast-forward") == nil {
		t.Error("a method of merge that does not exist was sent")
	}
	if _, err := rest.MergePull(t.Context(), repo, 0, approved, ""); err == nil {
		t.Error("a merge of the PR 0 was sent")
	}
	if got := gh.Merges(repo); len(got) != 0 {
		t.Errorf("GitHub merged %+v although every call was refused", got)
	}
	if _, err := rest.MergePull(t.Context(), repo, 999, approved, ""); !errors.Is(err, github.ErrNotFound) {
		t.Errorf("a PR that does not exist: %v", err)
	}
}

// A merge whose answer was lost (a 502 after GitHub merged) is
// recognized when the call is repeated, and the PR is merged at the approved commit:
// success, not a refusal; a PR merged at another commit is not ours.
func TestMergePullRecognizesAMergeThatHappenedWhoseAnswerWasLost(t *testing.T) {
	t.Parallel()
	rest, gh, n, repo := mergeRig(t)
	gh.SetPull(repo, n, approved, false, "clean")
	gh.LoseNextMerge(repo)
	res, err := rest.MergePull(t.Context(), repo, n, approved, "")
	if err != nil || !res.Merged || !res.Recovered {
		t.Fatalf("the answer was lost: %+v, %v; want the merge recognized", res, err)
	}
	// the call is repeated after the merge: still success, and nothing is merged twice
	if res, err = rest.MergePull(t.Context(), repo, n, approved, ""); err != nil || !res.Recovered {
		t.Errorf("repeating a merge that happened: %+v, %v", res, err)
	}
	if got := gh.Merges(repo); len(got) != 1 {
		t.Errorf("GitHub merged %d times; want once", len(got))
	}
	// merged at another commit: not the merge that was approved
	if _, err := rest.MergePull(t.Context(), repo, n, moved, ""); !errors.Is(err, github.ErrNotMergeable) {
		t.Errorf("a PR merged at another commit: %v", err)
	}
}

// CA-CLI-GM-13: the sub-issues of an epic are read page after page (GitHub allows
// 100 per parent, so the second page is empty, and it is asked for all the same).
func TestSubIssuesAreReadPageAfterPage(t *testing.T) {
	t.Parallel()
	rest, gh, _, repo := mergeRig(t)
	epic := gh.AddIssue(repo, "Epic", "epico")
	for i := range 100 {
		n := gh.AddIssue(repo, fmt.Sprintf("Sub %d", i))
		data, err := rest.Issue(t.Context(), repo, n)
		if err != nil {
			t.Fatal(err)
		}
		if err := rest.AddSubIssue(t.Context(), repo, epic, data.ID); err != nil {
			t.Fatal(err)
		}
	}
	subs, err := rest.SubIssues(t.Context(), repo, epic)
	if err != nil || len(subs) != 100 {
		t.Fatalf("sub-issues = %d, %v; want 100", len(subs), err)
	}
}

// CA-CLI-GM-13: the legacy commit statuses are read, to count in the CI of a
// pull request that reports by them.
func TestCommitStatusesAreRead(t *testing.T) {
	t.Parallel()
	rest, gh, _, repo := mergeRig(t)
	gh.AddStatus(repo, approved, "ci/legacy", "failure")
	gh.AddStatus(repo, approved, "ci/other", "success")
	got, err := rest.CommitStatuses(t.Context(), repo, approved)
	if err != nil || len(got) != 2 || got[0] != (github.Check{Name: "ci/legacy", State: "failure"}) {
		t.Fatalf("statuses = %+v, %v", got, err)
	}
}
