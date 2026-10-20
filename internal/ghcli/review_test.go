package ghcli_test

import (
	"context"
	"strings"
	"sync"
	"testing"
)

type headsLog struct {
	mu  sync.Mutex
	got map[int]string
}

func (h *headsLog) Get(_ context.Context, _ string, pr int) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.got[pr], nil
}

func (h *headsLog) Record(_ context.Context, _ string, pr int, sha string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.got[pr] = sha
	return nil
}

func reviewRig(t *testing.T) (*rig, *headsLog, int) {
	t.Helper()
	r := newRig(t)
	heads := &headsLog{got: map[int]string{}}
	r.h.Heads = heads
	pr := r.gh.AddPR(repoFull, "feat: login", "42-login", repoFull)
	r.gh.SetPull(repoFull, pr, "aaaabbbbccccdddd", false, "clean")
	r.gh.AddPullFileOf(repoFull, pr, "src/login.ts", "@@ -1 +1 @@\n-timeout = 10\n+timeout = 30\n", "modified", "", 1, 1)
	r.gh.AddCheck(repoFull, "aaaabbbbccccdddd", "build", "success")
	return r, heads, pr
}

// `gh review` reads the pull request and keeps the commit it read.
func TestGhReviewReadsThePullRequestAndKeepsTheCommit(t *testing.T) {
	t.Parallel()
	r, heads, pr := reviewRig(t)
	got := r.run("gm", "gh review", nil, itoa(pr))
	if !got.OK || got.Data["head_sha"] != "aaaabbbbccccdddd" || got.Data["diff"] != nil {
		t.Fatalf("review: %+v", got)
	}
	if files := got.Data["files"].([]any); len(files) != 1 || files[0].(map[string]any)["path"] != "src/login.ts" {
		t.Fatalf("files = %v", files)
	}
	if checks := got.Data["checks"].([]any); len(checks) != 1 {
		t.Fatalf("checks = %v", checks)
	}
	if heads.got[pr] != "aaaabbbbccccdddd" {
		t.Fatalf("the commit read is %q", heads.got[pr])
	}
	withDiff := r.run("gm", "gh review", map[string]any{"diff": true}, itoa(pr))
	if d, _ := withDiff.Data["diff"].(string); !withDiff.OK || !strings.Contains(d, "+timeout = 30") {
		t.Fatalf("--diff: %+v", withDiff)
	}
	// a new commit is a new reading
	r.gh.SetPull(repoFull, pr, "eeeeffff00001111", false, "clean")
	r.run("gm", "gh review", nil, itoa(pr))
	if heads.got[pr] != "eeeeffff00001111" {
		t.Fatalf("the commit read is %q after the push", heads.got[pr])
	}
	if got := r.run("tm", "gh review", nil, itoa(pr)); got.OK || got.Code != "level_denied" {
		t.Fatalf("a Mestre da mesa: %+v, want level_denied", got)
	}
}

func TestGhReviewSendsACommentOrAskForChangesOnTheCommitItRead(t *testing.T) {
	t.Parallel()
	r, _, pr := reviewRig(t)
	// a review is for the commit that was read: nothing was read yet
	if got := r.run("gm", "gh review", map[string]any{"comment": true, "body": "sem ler"}, itoa(pr)); got.OK || got.Code != "head_changed" {
		t.Fatalf("a review before the reading: %+v, want head_changed", got)
	}
	r.run("gm", "gh review", nil, itoa(pr))
	// a push after the reading: the review is refused, not sent on the new commit
	r.gh.SetPull(repoFull, pr, "9999000011112222", false, "clean")
	if got := r.run("gm", "gh review", map[string]any{"request-changes": true, "body": "e o novo?"}, itoa(pr)); got.OK || got.Code != "head_changed" || got.Exit != 7 {
		t.Fatalf("a review after a push: %+v, want exit 7 head_changed", got)
	}
	if len(r.gh.ReviewsOf(repoFull, pr)) != 0 {
		t.Fatal("a review went to a commit the Grand Master did not read")
	}
	r.gh.SetPull(repoFull, pr, "aaaabbbbccccdddd", false, "clean")
	if got := r.run("gm", "gh review", map[string]any{"comment": true, "body": "ok com ressalvas"}, itoa(pr)); !got.OK {
		t.Fatalf("comment: %+v", got)
	}
	if got := r.run("gm", "gh review", map[string]any{"request-changes": true, "body": "falta teste"}, itoa(pr)); !got.OK {
		t.Fatalf("request-changes: %+v", got)
	}
	sent := r.gh.ReviewsOf(repoFull, pr)
	if len(sent) != 2 || sent[0].State != "COMMENTED" || sent[1].State != "CHANGES_REQUESTED" || sent[0].Commit != "aaaabbbbccccdddd" {
		t.Fatalf("reviews sent = %+v, want a comment and a request of changes on the commit", sent)
	}
	for name, flags := range map[string]map[string]any{
		"no body":         {"comment": true},
		"both reviews":    {"comment": true, "request-changes": true, "body": "x"},
		"body alone":      {"body": "x"},
		"diff and review": {"comment": true, "body": "x", "diff": true},
	} {
		if got := r.run("gm", "gh review", flags, itoa(pr)); got.OK || got.Code != "usage" {
			t.Errorf("%s: %+v, want usage", name, got)
		}
	}
	r.locked.Store(true)
	if got := r.run("gm", "gh review", map[string]any{"comment": true, "body": "travado"}, itoa(pr)); got.OK || got.Code != "locked" {
		t.Errorf("locked: %+v, want locked", got)
	}
	r.locked.Store(false)
	r.gh.SetPullState(repoFull, pr, "closed", false, "main")
	if got := r.run("gm", "gh review", map[string]any{"comment": true, "body": "tarde"}, itoa(pr)); got.OK || got.Code != "not_open" {
		t.Errorf("closed: %+v, want not_open", got)
	}
	if n := len(r.gh.ReviewsOf(repoFull, pr)); n != 2 {
		t.Errorf("%d reviews after the refusals, want 2", n)
	}
}

type fakeSettings struct{}

func (fakeSettings) Branch(context.Context, string) (string, error) { return "main", nil }
func (fakeSettings) Mode(context.Context, string) (string, string, error) {
	return "app", "", nil
}
func (fakeSettings) Apps(context.Context) ([]string, error) { return []string{"Mestre (ready)"}, nil }

func TestGhSettingsShowReadsTheRulesAndApplyChangesNothingWithoutTheSettingsOfTheRun(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.h.Settings = fakeSettings{}
	r.gh.SetProtection(repoFull, "main", map[string]any{"required_pull_request_reviews": map[string]any{"required_approving_review_count": 2}})
	got := r.run("gm", "gh settings", nil, "show")
	if !got.OK || got.Data["approvals"] != float64(2) || got.Data["branch"] != "main" || got.Data["approval_mode"] != "app" {
		t.Fatalf("settings show: %+v", got)
	}
	if got := r.run("gm", "gh settings", map[string]any{"body": `{"name":"x"}`}, "apply"); got.OK || got.Code != "github_unavailable" {
		t.Fatalf("apply: %+v, want it refused and nothing changed", got)
	}
	if len(r.gh.Rulesets(repoFull)) != 0 {
		t.Fatal("apply made a ruleset")
	}
	if got := r.run("tm", "gh settings", nil, "show"); got.OK || got.Code != "level_denied" {
		t.Fatalf("a Mestre da mesa: %+v", got)
	}
}
