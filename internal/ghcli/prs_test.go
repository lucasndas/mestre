package ghcli_test

import (
	"strings"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/github"
)

const patchLogin = "@@ -10,3 +10,5 @@ func login() {\n \tctx := x\n-\told()\n+\tnew1()\n+\tnew2()\n \treturn\n"

// prOfTheTable makes the PR of the table 42-login, with a file in its diff.
func prOfTheTable(t *testing.T, r *rig) int {
	t.Helper()
	pr := r.gh.AddPR(repoFull, "Login", "42-login", repoFull)
	if err := r.objs.Register(r.ctx, github.Object{Repo: repoFull, Kind: github.KindPR, Number: pr, Table: "meu-app/42-login"}); err != nil {
		t.Fatal(err)
	}
	r.prs["meu-app/42-login"] = pr
	r.gh.AddPullFile(repoFull, pr, "src/login.ts", patchLogin)
	r.gh.SetPull(repoFull, pr, strings.Repeat("a", 40), false, "clean")
	return pr
}

// CA-CLI-GH-09
func TestANoteGoesOnALineOfTheDiffAtTheHeadOfThePR(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	pr := prOfTheTable(t, r)
	got := r.run("tm", "pr note", nil, "src/login.ts:12", "por que o timeout é 30 s")
	if !got.OK {
		t.Fatalf("note: %+v", got)
	}
	cs := r.gh.ReviewComments(repoFull, pr)
	if len(cs) != 1 || cs[0]["path"] != "src/login.ts" || cs[0]["line"] != 12 || cs[0]["commit_id"] != strings.Repeat("a", 40) || cs[0]["body"] != "por que o timeout é 30 s" {
		t.Errorf("comments = %+v", cs)
	}
	// the new side: an added line (11, 12), a line of context (10, 13); not the removed one, not outside the hunk, not another file
	for _, ok := range []string{"src/login.ts:10", "src/login.ts:11", "src/login.ts:13"} {
		if got := r.run("tm", "pr note", nil, ok, "x"); !got.OK {
			t.Errorf("%s: %+v", ok, got)
		}
	}
	for _, bad := range []string{"src/login.ts:9", "src/login.ts:14", "src/login.ts:0", "README.md:1", "src/login.ts"} {
		if got := r.run("tm", "pr note", nil, bad, "x"); got.OK || got.Exit != 2 {
			t.Errorf("%s: %+v, want exit 2", bad, got)
		}
	}
}

func TestStatusShowsTheChecksTheReviewsAndWhyItCannotBeMerged(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	pr := prOfTheTable(t, r)
	sha := strings.Repeat("a", 40)
	r.gh.AddCheck(repoFull, sha, "build", "success")
	r.gh.AddCheck(repoFull, sha, "lint", "")
	r.gh.AddReview(repoFull, pr, "ana", "APPROVED")
	status := func() map[string]any {
		got := r.run("tm", "pr status", nil)
		if !got.OK {
			t.Fatalf("status: %+v", got)
		}
		return got.Data
	}
	st := status()
	if st["mergeable"] != false || st["blocked_by"] != "check lint has not finished" || int(st["number"].(float64)) != pr || st["head_sha"] != sha || st["state"] != "open" {
		t.Fatalf("status = %+v", st)
	}
	if len(st["checks"].([]any)) != 2 || len(st["reviews"].([]any)) != 1 {
		t.Errorf("checks/reviews = %+v", st)
	}
	r.gh.AddCheck(repoFull, sha, "e2e", "failure")
	if st := status(); st["blocked_by"] != "check lint has not finished" {
		t.Errorf("the first reason: %+v", st["blocked_by"])
	}
	// a clean PR
	r2 := newRig(t)
	pr2 := prOfTheTable(t, r2)
	r2.gh.AddCheck(repoFull, strings.Repeat("a", 40), "build", "success")
	r2.gh.AddReview(repoFull, pr2, "ana", "APPROVED")
	got := r2.run("tm", "pr status", nil)
	if got.Data["mergeable"] != true || got.Data["blocked_by"] != "" {
		t.Errorf("clean: %+v", got.Data)
	}
	// changes requested, then approved by the same person: the last counts
	r2.gh.AddReview(repoFull, pr2, "bia", "CHANGES_REQUESTED")
	if got := r2.run("tm", "pr status", nil); got.Data["blocked_by"] != "changes requested by bia" {
		t.Errorf("changes requested: %+v", got.Data["blocked_by"])
	}
	r2.gh.AddReview(repoFull, pr2, "bia", "APPROVED")
	if got := r2.run("tm", "pr status", nil); got.Data["mergeable"] != true {
		t.Errorf("approved after: %+v", got.Data)
	}
	// conflict and draft
	r2.gh.SetPull(repoFull, pr2, strings.Repeat("a", 40), false, "dirty")
	if got := r2.run("tm", "pr status", nil); got.Data["blocked_by"] != "conflict with the base" {
		t.Errorf("dirty: %+v", got.Data["blocked_by"])
	}
	r2.gh.SetPull(repoFull, pr2, strings.Repeat("a", 40), true, "clean")
	if got := r2.run("tm", "pr status", nil); got.Data["blocked_by"] != "the PR is a draft" {
		t.Errorf("draft: %+v", got.Data["blocked_by"])
	}
}

func TestCommentReplyAndTheCommentsNobodyAnswered(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	pr := prOfTheTable(t, r)
	if got := r.run("tm", "pr comment", nil, "Rebase feito"); !got.OK {
		t.Fatalf("comment: %+v", got)
	}
	if cs := r.gh.Comments(repoFull, pr); len(cs) != 1 || !strings.HasPrefix(cs[0], "Rebase feito") {
		t.Errorf("comments = %q", cs)
	}
	r.run("tm", "pr note", nil, "src/login.ts:11", "primeira")
	r.run("tm", "pr note", nil, "src/login.ts:12", "segunda")
	st := r.run("tm", "pr status", nil)
	if int(st.Data["unresolved_comments"].(float64)) != 2 {
		t.Fatalf("unresolved = %v", st.Data["unresolved_comments"])
	}
	id := r.gh.ReviewComments(repoFull, pr)[0]["id"].(int64)
	if got := r.run("tm", "pr reply", nil, itoa(int(id)), "corrigido"); !got.OK {
		t.Fatalf("reply: %+v", got)
	}
	if st := r.run("tm", "pr status", nil); int(st.Data["unresolved_comments"].(float64)) != 1 {
		t.Errorf("unresolved after the reply = %v", st.Data["unresolved_comments"])
	}
	if got := r.run("tm", "pr reply", nil, "99999", "x"); got.OK || got.Code != "not_found" {
		t.Errorf("reply to nothing: %+v", got)
	}
	if got := r.run("tm", "pr reply", nil, "abc", "x"); got.Exit != 2 {
		t.Errorf("reply with a bad id: %+v", got)
	}
}

func TestATableWithNoPRHasNoPRCommands(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	for _, tc := range []struct {
		cmd  string
		args []string
	}{{"pr status", nil}, {"pr comment", []string{"x"}}, {"pr reply", []string{"1", "x"}}, {"pr note", []string{"a.ts:1", "x"}}} {
		if got := r.run("tm", tc.cmd, nil, tc.args...); got.OK || got.Exit != 4 || got.Code != "no_pr" {
			t.Errorf("%s: %+v, want exit 4 no_pr", tc.cmd, got)
		}
	}
	// the Grand Master has no table
	if got := r.run("gm", "pr status", nil); got.OK {
		t.Errorf("the Grand Master: %+v", got)
	}
	// a PR that is not the table's (the registry says so) is refused
	other := r.gh.AddPR(repoFull, "de outra mesa", "43-outra", repoFull)
	if err := r.objs.Register(r.ctx, github.Object{Repo: repoFull, Kind: github.KindPR, Number: other, Table: "meu-app/43-outra"}); err != nil {
		t.Fatal(err)
	}
	r.prs["meu-app/42-login"] = other
	if got := r.run("tm", "pr comment", nil, "x"); got.OK || got.Code != "outside_epic" {
		t.Errorf("a PR of another table: %+v", got)
	}
	if n := len(r.gh.Comments(repoFull, other)); n != 0 {
		t.Errorf("%d comments on the PR of another table", n)
	}
	r.gh.AddPullFile(repoFull, other, "src/x.ts", patchLogin)
	for _, tc := range []struct {
		cmd  string
		args []string
	}{{"pr status", nil}, {"pr reply", []string{"1", "x"}}, {"pr note", []string{"src/x.ts:11", "x"}}} {
		if got := r.run("tm", tc.cmd, nil, tc.args...); got.OK || got.Code != "outside_epic" {
			t.Errorf("%s on the PR of another table: %+v", tc.cmd, got)
		}
	}
	if n := len(r.gh.ReviewComments(repoFull, other)); n != 0 {
		t.Errorf("%d review comments on the PR of another table", n)
	}
}

func TestAClassMayBeGrantedThePRCommands(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	prOfTheTable(t, r)
	// the QA lists them in cli.allow
	if got := r.run("qa", "pr status", nil); !got.OK {
		t.Errorf("a QA with the class that allows it: %+v", got)
	}
	// the Dev does not
	if got := r.run("dev", "pr status", nil); got.OK || got.Code != "class_denied" {
		t.Errorf("a Dev: %+v", got)
	}
	if got := r.run("dev", "pr comment", nil, "x"); got.Code != "class_denied" {
		t.Errorf("a Dev comment: %+v", got)
	}
}

func TestAReplyIsToAThreadOfThisPRNotToAnyComment(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	prOfTheTable(t, r)
	// a comment of review on another PR of the repository
	other := r.gh.AddPR(repoFull, "de outra mesa", "43-outra", repoFull)
	r.gh.AddPullFile(repoFull, other, "src/x.ts", patchLogin)
	if err := r.objs.Register(r.ctx, github.Object{Repo: repoFull, Kind: github.KindPR, Number: other, Table: "meu-app/43-outra"}); err != nil {
		t.Fatal(err)
	}
	r.gh.AddReviewComment(repoFull, other, "src/x.ts", 11, "de outra", 0)
	foreign := r.gh.ReviewComments(repoFull, other)[0]["id"].(int64)
	if got := r.run("tm", "pr reply", nil, itoa(int(foreign)), "intrometido"); got.OK || got.Code != "not_found" {
		t.Errorf("reply to the comment of another PR: %+v", got)
	}
	if n := len(r.gh.ReviewComments(repoFull, other)); n != 1 {
		t.Errorf("the other PR got %d comments, want its own only", n)
	}
	// a reply is not itself replied to: the thread starts at the first comment
	r.run("tm", "pr note", nil, "src/login.ts:11", "primeira")
	mine := r.gh.ReviewComments(repoFull, r.prs["meu-app/42-login"])[0]["id"].(int64)
	if got := r.run("tm", "pr reply", nil, itoa(int(mine)), "ok"); !got.OK {
		t.Fatal(got)
	}
	cs := r.gh.ReviewComments(repoFull, r.prs["meu-app/42-login"])
	if got := r.run("tm", "pr reply", nil, itoa(int(cs[1]["id"].(int64))), "resposta da resposta"); got.OK {
		t.Errorf("a reply to a reply: %+v", got)
	}
}

func TestTheStatusReadsEveryPageOfTheReviewsAndTheChecks(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	pr := prOfTheTable(t, r)
	sha := strings.Repeat("a", 40)
	for i := range 130 {
		r.gh.AddReview(repoFull, pr, "ana", "COMMENTED")
		r.gh.AddCheck(repoFull, sha, "job"+itoa(i), "success")
	}
	r.gh.AddReview(repoFull, pr, "bia", "CHANGES_REQUESTED") // the 131st
	r.gh.AddCheck(repoFull, sha, "last", "failure")          // the 131st
	got := r.run("tm", "pr status", nil)
	if !got.OK || got.Data["mergeable"] != false || len(got.Data["checks"].([]any)) != 131 || len(got.Data["reviews"].([]any)) != 131 {
		t.Fatalf("status: ok %v, mergeable %v, %d checks, %d reviews", got.OK, got.Data["mergeable"], len(got.Data["checks"].([]any)), len(got.Data["reviews"].([]any)))
	}
	if got.Data["blocked_by"] != "check last failure" {
		t.Errorf("blocked_by = %v", got.Data["blocked_by"])
	}
}

func TestAPRIsMergeableOnlyWhenGitHubSaysClean(t *testing.T) {
	t.Parallel()
	for state, want := range map[string]bool{"clean": true, "has_hooks": true, "unknown": false, "unstable": false, "blocked": false, "behind": false, "": false} {
		r := newRig(t)
		pr := prOfTheTable(t, r)
		r.gh.SetPull(repoFull, pr, strings.Repeat("a", 40), false, state)
		got := r.run("tm", "pr status", nil)
		if got.Data["mergeable"] != want {
			t.Errorf("%q: mergeable %v, want %v (%v)", state, got.Data["mergeable"], want, got.Data["blocked_by"])
		}
	}
}

func TestANoteIsNotPutOnAnotherHeadThanTheOneItWasCheckedAgainst(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	pr := prOfTheTable(t, r)
	*r.onFiles = func() { r.gh.SetPull(repoFull, pr, strings.Repeat("b", 40), false, "clean") } // a push in the middle
	got := r.run("tm", "pr note", nil, "src/login.ts:12", "x")
	if got.OK || got.Exit != 7 || got.Code != "head_changed" {
		t.Errorf("note: %+v, want exit 7 head_changed", got)
	}
	if n := len(r.gh.ReviewComments(repoFull, pr)); n != 0 {
		t.Errorf("%d comments were put", n)
	}
}

// CA-CLI-GH-11
func TestUnderTheQuotaWritesWaitForTheRenewalAndReadingsAreRefused(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	prOfTheTable(t, r)
	n := itoa(r.gh.AddIssue(repoFull, "da mesa"))
	if err := r.objs.Register(r.ctx, github.Object{Repo: repoFull, Kind: github.KindIssue, Number: 3, Table: "meu-app/42-login"}); err != nil {
		t.Fatal(err)
	}
	// the App answers once, so the engine learns how much is left, and then the quota is gone
	if got := r.run("tm", "issue list", nil); !got.OK {
		t.Fatalf("before: %+v", got)
	}
	r.gh.SetUsed("tok_mestre", 4600) // 400 of 5000 left: under 10%
	if got := r.run("tm", "pr status", nil); !got.OK {
		t.Fatalf("this answer teaches the engine the quota: %+v", got)
	}
	if got := r.run("tm", "pr status", nil); got.OK || got.Exit != 5 || got.Code != "github_unavailable" || !strings.Contains(got.Error, "renews at 13:00 UTC") {
		t.Fatalf("a reading under the quota: %+v", got)
	}
	got := r.run("tm", "issue comment", nil, n, "uma vez só")
	if !got.OK || got.Exit != 0 || got.Data["queued"] != true {
		t.Fatalf("a write under the quota: %+v, want exit 0 and queued", got)
	}
	if cs := r.gh.Comments(repoFull, 3); len(cs) != 0 {
		t.Fatalf("the comment went out under the quota: %q", cs)
	}
	r.clk.Advance(59 * time.Minute)
	if cs := r.gh.Comments(repoFull, 3); len(cs) != 0 {
		t.Fatal("the comment went out before the renewal")
	}
	r.gh.IssueToken("tok_mestre", "mestre") // the token of the double lasts an hour
	r.clk.Advance(time.Minute)
	if cs := r.gh.Comments(repoFull, 3); len(cs) != 1 || !strings.HasPrefix(cs[0], "uma vez só") {
		t.Errorf("after the renewal the comments are %q, want one", cs)
	}
	if got := r.run("tm", "pr status", nil); !got.OK {
		t.Errorf("a reading after the renewal: %+v", got)
	}
}

// CA-CLI-GH-11, the other half: the secondary limit is not repeated on its own
func TestASecondaryLimitIsNotRepeatedOnItsOwn(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	prOfTheTable(t, r)
	n := r.gh.AddIssue(repoFull, "da mesa")
	if err := r.objs.Register(r.ctx, github.Object{Repo: repoFull, Kind: github.KindIssue, Number: n, Table: "meu-app/42-login"}); err != nil {
		t.Fatal(err)
	}
	r.gh.FailNextWrite(403, "60")
	got := r.run("tm", "issue comment", nil, itoa(n), "x")
	if got.OK || got.Exit != 5 || got.Code != "github_unavailable" || !strings.Contains(got.Error, "limit") {
		t.Fatalf("comment: %+v", got)
	}
	if cs := r.gh.Comments(repoFull, n); len(cs) != 0 {
		t.Errorf("the comment went out: %q", cs)
	}
	r.clk.Advance(5 * time.Minute)
	if cs := r.gh.Comments(repoFull, n); len(cs) != 0 {
		t.Errorf("the comment was repeated on its own: %q", cs)
	}
}

func TestWithTheLeaseExpiredWritesWaitAndReadingsAreRefused(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	prOfTheTable(t, r)
	n := r.gh.AddIssue(repoFull, "da mesa")
	if err := r.objs.Register(r.ctx, github.Object{Repo: repoFull, Kind: github.KindIssue, Number: n, Table: "meu-app/42-login"}); err != nil {
		t.Fatal(err)
	}
	r.lease.expire()
	if got := r.run("tm", "issue comment", nil, itoa(n), "depois"); !got.OK || got.Data["queued"] != true {
		t.Fatalf("a write: %+v", got)
	}
	for _, cmd := range []string{"issue list", "pr status"} {
		if got := r.run("tm", cmd, nil); got.OK || got.Exit != 5 || got.Code != "github_unavailable" {
			t.Errorf("%s: %+v", cmd, got)
		}
	}
	if cs := r.gh.Comments(repoFull, n); len(cs) != 0 {
		t.Fatalf("the comment went out with the lease expired: %q", cs)
	}
	if sent := r.lease.renew(r.ctx); sent != 1 {
		t.Fatalf("the queue sent %d", sent)
	}
	if cs := r.gh.Comments(repoFull, n); len(cs) != 1 {
		t.Errorf("after the lease the comments are %q", cs)
	}
}

// Part of CA-CANAL-04: with the Mestre locked nothing is done on GitHub, not even
// what waited in the queue.
func TestNothingIsDoneOnGitHubWhileTheMestreIsLocked(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	n := r.gh.AddIssue(repoFull, "da mesa")
	if err := r.objs.Register(r.ctx, github.Object{Repo: repoFull, Kind: github.KindIssue, Number: n, Table: "meu-app/42-login"}); err != nil {
		t.Fatal(err)
	}
	r.locked.Store(true)
	for _, cmd := range []string{"issue list", "issue create", "issue comment", "pr status"} {
		args := []string{}
		flags := map[string]any{}
		switch cmd {
		case "issue comment":
			args = []string{itoa(n), "x"}
		case "issue create":
			flags = map[string]any{"title": "x"}
		}
		if got := r.run("tm", cmd, flags, args...); got.OK || got.Code != "locked" {
			t.Errorf("%s: %+v, want locked", cmd, got)
		}
	}
	if len(r.gh.Actions()) != 0 {
		t.Errorf("GitHub got %d writes while locked", len(r.gh.Actions()))
	}
	// what waited for the lease and goes out after the lock: refused when it goes
	r.locked.Store(false)
	r.lease.expire()
	if got := r.run("tm", "issue comment", nil, itoa(n), "depois"); !got.OK || got.Data["queued"] != true {
		t.Fatalf("queued: %+v", got)
	}
	r.locked.Store(true)
	r.lease.renew(r.ctx)
	if cs := r.gh.Comments(repoFull, n); len(cs) != 0 {
		t.Errorf("a queued comment went out under the lock: %q", cs)
	}
}
