package ghcli_test

import (
	"strings"
	"testing"

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
