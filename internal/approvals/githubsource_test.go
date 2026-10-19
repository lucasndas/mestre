package approvals_test

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/approvals"
	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/github"
	"github.com/lucasndas/mestre/internal/orchestration/chain"
	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/internal/testkit/fakechannel"
	"github.com/lucasndas/mestre/internal/testkit/fakegithub"
	"github.com/lucasndas/mestre/internal/testkit/fakenet"
	"github.com/lucasndas/mestre/internal/testkit/guildtpl"
	"github.com/lucasndas/mestre/internal/users"
	"github.com/lucasndas/mestre/pkg/clock"
)

const repo = "lucas/meu-app"

type ghWorld struct {
	t   *testing.T
	gh  *fakegithub.Server
	src approvals.GitHubSource
	pr  int
	sha string
}

func newGHWorld(t *testing.T) *ghWorld {
	t.Helper()
	clk := clock.NewFake(time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC))
	gh, err := fakegithub.New(clk, t)
	if err != nil {
		t.Fatal(err)
	}
	gh.AddRepo("lucas", "meu-app")
	gh.IssueToken("tok", "grand_master")
	rest := &github.REST{BaseURL: "https://api.github.com", HTTP: &http.Client{Transport: fakenet.HandlerTransport(gh)},
		Token: func(context.Context, string) (string, error) { return "tok", nil }}
	w := &ghWorld{t: t, gh: gh, sha: strings.Repeat("a", 40)}
	w.src = approvals.GitHubSource{Reader: func(context.Context, string) (approvals.PullReader, error) { return rest, nil },
		Characters: func(context.Context) (map[string]bool, error) {
			return map[string]bool{"dev[bot]": true, "revisor[bot]": true, "qa[bot]": true}, nil
		}}
	w.pr = gh.AddPR(repo, "Login", "42-login", repo)
	gh.SetPullAuthor(repo, w.pr, "dev[bot]")
	gh.SetPull(repo, w.pr, w.sha, false, "clean")
	return w
}

func (w *ghWorld) facts() (chain.PRFacts, error) {
	return w.src.PullRequest(context.Background(), "meu-app", chain.PRRef{Repo: repo, Number: w.pr, SHA: w.sha})
}

// Part of CA-APROV-POL-01: the conditions of the policy read the real facts.
func TestTheFactsOfAPRAreWhatGitHubSaysAtTheCommitOfTheRequest(t *testing.T) {
	t.Parallel()
	w := newGHWorld(t)
	w.gh.AddPullFile(repo, w.pr, "src/login.ts", "@@ -1,2 +1,4 @@\n a\n-b\n+c\n+d\n+++ DROP TABLE users\n")
	w.gh.AddPullFileOf(repo, w.pr, "old.ts", "", "removed", "", 0, 9)
	w.gh.AddPullFileOf(repo, w.pr, "big.sql", "", "modified", "", 5000, 10) // GitHub gave no patch
	w.gh.AddPullFileOf(repo, w.pr, "new/name.ts", "@@ -1 +1 @@\n-x\n+y\n", "renamed", "name.ts", 1, 1)
	w.gh.AddIssueLabel(repo, w.pr, "mestre:humano")
	w.gh.AddCheck(repo, w.sha, "build", "success")
	w.gh.AddCheck(repo, w.sha, "lint", "skipped")
	w.gh.AddReview(repo, w.pr, "revisor[bot]", "APPROVED")
	f, err := w.facts()
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]chain.PRFile{}
	for _, x := range f.Files {
		by[x.Path] = x
	}
	if l := by["src/login.ts"]; l.Added != 3 || l.Removed != 1 || len(l.AddedLines) != 3 || l.AddedLines[0] != "c" || l.AddedLines[2] != "++ DROP TABLE users" || l.PatchMissing || l.Deleted {
		t.Errorf("src/login.ts = %+v", l)
	}
	if o := by["old.ts"]; !o.Deleted || o.PatchMissing || o.Removed != 9 {
		t.Errorf("old.ts = %+v", o)
	}
	if b := by["big.sql"]; !b.PatchMissing {
		t.Errorf("big.sql = %+v: a file with changes and no patch is one the engine can not read", b)
	}
	if n := by["new/name.ts"]; n.PreviousPath != "name.ts" {
		t.Errorf("renamed = %+v", n)
	}
	if !f.LabelsRead || len(f.Labels) != 1 || f.Labels[0] != "mestre:humano" || !f.CIGreen || !f.ReviewDone || f.Incomplete {
		t.Errorf("facts = %+v", f)
	}
}

func TestCIIsGreenOnlyWithChecksAndAllOfThemFine(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		checks [][2]string
		want   bool
	}{
		"none":     {nil, false},
		"green":    {[][2]string{{"a", "success"}, {"b", "neutral"}}, true},
		"failed":   {[][2]string{{"a", "success"}, {"b", "failure"}}, false},
		"running":  {[][2]string{{"a", "success"}, {"b", ""}}, false},
		"timedout": {[][2]string{{"a", "timed_out"}}, false},
		"canceled": {[][2]string{{"a", "cancelled"}}, false},
	} {
		w := newGHWorld(t)
		for _, c := range tc.checks {
			w.gh.AddCheck(repo, w.sha, c[0], c[1])
		}
		f, err := w.facts()
		if err != nil || f.CIGreen != tc.want {
			t.Errorf("%s: green %v, %v; want %v", name, f.CIGreen, err, tc.want)
		}
	}
}

func TestAReviewIsDoneOnlyByAnotherCharacterWithNothingPending(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		reviews [][2]string
		want    bool
	}{
		"none":                              {nil, false},
		"another character":                 {[][2]string{{"revisor[bot]", "APPROVED"}}, true},
		"only comments":                     {[][2]string{{"revisor[bot]", "COMMENTED"}}, true},
		"the author itself":                 {[][2]string{{"dev[bot]", "APPROVED"}}, false},
		"a person":                          {[][2]string{{"lucas", "APPROVED"}}, false},
		"an app of no one":                  {[][2]string{{"intruso[bot]", "APPROVED"}}, false},
		"a comment does not lift a request": {[][2]string{{"qa[bot]", "CHANGES_REQUESTED"}, {"qa[bot]", "COMMENTED"}, {"revisor[bot]", "APPROVED"}}, false},
		"changes requested":                 {[][2]string{{"revisor[bot]", "APPROVED"}, {"qa[bot]", "CHANGES_REQUESTED"}}, false},
		"requested, then ok":                {[][2]string{{"qa[bot]", "CHANGES_REQUESTED"}, {"qa[bot]", "APPROVED"}}, true},
	} {
		w := newGHWorld(t)
		for _, r := range tc.reviews {
			w.gh.AddReview(repo, w.pr, r[0], r[1])
		}
		f, err := w.facts()
		if err != nil || f.ReviewDone != tc.want {
			t.Errorf("%s: done %v, %v; want %v", name, f.ReviewDone, err, tc.want)
		}
	}
}

func TestThePRWhoseHeadMovedHasNoFactsAtTheCommitOfTheRequest(t *testing.T) {
	t.Parallel()
	w := newGHWorld(t)
	w.gh.SetPull(repo, w.pr, strings.Repeat("b", 40), false, "clean")
	if _, err := w.facts(); err == nil {
		t.Error("facts were given for another commit")
	}
	if _, err := w.src.PullRequest(context.Background(), "meu-app", chain.PRRef{Repo: repo, Number: 999, SHA: w.sha}); err == nil {
		t.Error("facts of a PR that does not exist")
	}
}

// Part of CA-APROV-FLUXO-01: with the live target read from GitHub, a push after
// the question makes the approval invalid and nothing is applied.
func TestAPushAfterTheQuestionInvalidatesTheApproval(t *testing.T) {
	t.Parallel()
	w := newGHWorld(t)
	ctx := context.Background()
	clk := clock.NewFake(time.Date(2026, 10, 19, 14, 30, 0, 0, time.UTC))
	db, err := guildtpl.Open(ctx, filepath.Join(t.TempDir(), "mestre.db"), store.Options{Synchronous: "OFF", Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Writer().ExecContext(ctx, `INSERT INTO workspaces (slug, repo, repo_key, clone_url, default_branch, state, stage, created_at, updated_at)
		VALUES ('loja', 'lucas/loja', 'lucas/loja', 'https://example.test/x.git', 'main', 'active', '', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	svc, err := users.New(users.Options{DB: db, Clock: clk, PIN: users.PINCost{MemoryKiB: 8, Time: 1, Threads: 1}})
	if err != nil {
		t.Fatal(err)
	}
	owner, err := svc.CreateOwner(ctx, "Lucas")
	if err != nil {
		t.Fatal(err)
	}
	ch := fakechannel.NewChain(db, clk)
	flow, err := approvals.New(approvals.Options{DB: db, Clock: clk, Chain: ch, Users: svc, Messenger: fakechannel.New(), Targets: w.src, Locked: func(context.Context) bool { return false }})
	if err != nil {
		t.Fatal(err)
	}
	ch.Flow = flow
	target := approvals.PRTarget(repo, w.pr, w.sha, "main")
	imp, err := chain.Impress(target)
	if err != nil {
		t.Fatal(err)
	}
	r := chain.Request{ID: 1, Workspace: "loja", Type: classes.RequestPR, State: chain.Open, Level: chain.Level{Kind: chain.LevelPeople},
		PR: &chain.PRRef{Repo: repo, Number: w.pr, SHA: w.sha}, Target: target, Impression: imp}
	if err := ch.Add(ctx, r); err != nil {
		t.Fatal(err)
	}
	if err := flow.Ask(ctx, chain.PeopleQuestion{Request: r, Text: "?", Approvals: 1, MinLevel: classes.LevelApprover, Answers: []chain.Answer{chain.AnswerApprove}}); err != nil {
		t.Fatal(err)
	}
	w.gh.SetPull(repo, w.pr, strings.Repeat("c", 40), false, "clean") // a push
	if _, err := flow.Answer(ctx, owner.ID, approvals.Input{Request: 1, Answer: chain.AnswerApprove}); !errors.Is(err, approvals.ErrTargetChanged) {
		t.Fatalf("the answer: %v", err)
	}
	if ch.State(1) != chain.Invalidated || len(ch.Decisions()) != 0 {
		t.Errorf("state %s, decisions %d", ch.State(1), len(ch.Decisions()))
	}
	// the same head again would have been fine
	w.gh.SetPull(repo, w.pr, w.sha, false, "clean")
	r2 := r
	r2.ID = 2
	if err := ch.Add(ctx, r2); err != nil {
		t.Fatal(err)
	}
	if err := flow.Ask(ctx, chain.PeopleQuestion{Request: r2, Text: "?", Approvals: 1, MinLevel: classes.LevelApprover, Answers: []chain.Answer{chain.AnswerApprove}}); err != nil {
		t.Fatal(err)
	}
	if _, err := flow.Answer(ctx, owner.ID, approvals.Input{Request: 2, Answer: chain.AnswerApprove}); err != nil || ch.State(2) != chain.Approved {
		t.Errorf("the head that did not move: %v, %s", err, ch.State(2))
	}
}

func TestAReviewOfAnotherCommitDoesNotDescribeTheHead(t *testing.T) {
	t.Parallel()
	w := newGHWorld(t)
	w.gh.AddReviewAt(repo, w.pr, "revisor[bot]", "APPROVED", strings.Repeat("0", 40)) // an old commit
	if f, err := w.facts(); err != nil || f.ReviewDone {
		t.Errorf("an approval of another commit counted: %v, %v", f.ReviewDone, err)
	}
	w.gh.AddReview(repo, w.pr, "qa[bot]", "APPROVED") // on the head
	if f, err := w.facts(); err != nil || !f.ReviewDone {
		t.Errorf("an approval of the head did not count: %v, %v", f.ReviewDone, err)
	}
}

func TestTheLegacyStatusesCountForTheCI(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		check, status string
		want          bool
	}{
		"only a status that passed":        {"", "success", true},
		"a check and a failed status":      {"success", "failure", false},
		"a check and a pending status":     {"success", "pending", false},
		"a check and a status that passed": {"success", "success", true},
	} {
		w := newGHWorld(t)
		if tc.check != "" {
			w.gh.AddCheck(repo, w.sha, "build", tc.check)
		}
		w.gh.AddStatus(repo, w.sha, "ci/legacy", tc.status)
		if f, err := w.facts(); err != nil || f.CIGreen != tc.want {
			t.Errorf("%s: green %v, %v", name, f.CIGreen, err)
		}
	}
}

func TestAClosedPRAndABaseThatMovedAreNotWhatWasAsked(t *testing.T) {
	t.Parallel()
	w := newGHWorld(t)
	w.gh.SetPullState(repo, w.pr, "closed", false, "")
	if _, err := w.facts(); err == nil {
		t.Error("facts of a closed PR")
	}
	w2 := newGHWorld(t)
	before, err := w2.src.Current(context.Background(), chain.Request{Workspace: "meu-app", Type: classes.RequestPR, PR: &chain.PRRef{Repo: repo, Number: w2.pr, SHA: w2.sha}})
	if err != nil {
		t.Fatal(err)
	}
	w2.gh.SetPullState(repo, w2.pr, "open", false, "release")
	after, _ := w2.src.Current(context.Background(), chain.Request{Workspace: "meu-app", Type: classes.RequestPR, PR: &chain.PRRef{Repo: repo, Number: w2.pr, SHA: w2.sha}})
	a, _ := chain.Impress(before)
	b, _ := chain.Impress(after)
	if a == b {
		t.Error("changing the base did not change the target")
	}
}
