package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/approvals"
	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/github"
	"github.com/lucasndas/mestre/internal/guild"
	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/orchestration/chain"
	"github.com/lucasndas/mestre/internal/users"
	"github.com/lucasndas/mestre/internal/vault"
)

// approvalWorld is the standard integration with the chain of approval, the
// Owner registered as "lucas" on GitHub, and the Attach done.
type approvalWorld struct {
	*ghCompose
	owner users.User
	head  string
	fwd   *localchan.LocalForwarder
}

// epochLock is the lock of the Guild that shows the epoch this Citadel holds.
type epochLock struct{}

func (epochLock) Check(context.Context) (uint64, error)               { return 1, nil }
func (epochLock) Claim(context.Context, uint64, string) (bool, error) { return true, nil }

func newApprovalWorld(t *testing.T) *approvalWorld {
	t.Helper()
	c := newGHComposeWith(t, true)
	w := &approvalWorld{ghCompose: c, head: strings.Repeat("a", 40)}
	owner, err := c.run.users.CreateOwner(c.ctx, "Lucas")
	if err != nil {
		t.Fatal(err)
	}
	login := "lucas"
	if w.owner, err = c.run.users.Edit(c.ctx, owner, owner.ID, users.Edit{GitHubLogin: &login}); err != nil {
		t.Fatal(err)
	}
	// the device flow of the App "Grão-Mestre"
	slug := github.Slug("lucas-mestre-grao-mestre")
	c.gh.SetClientID(slug, "Iv1."+slug)
	c.gh.SetDeviceFlow(slug, true)
	c.gh.SetClientSecret("Iv1."+slug, "client-secret-gm")
	if err := c.vault.Put(c.ctx, github.ClientSecretName("lucas-mestre-grao-mestre"), vault.TypeGitHubAppKey, []byte("client-secret-gm"), nil); err != nil {
		t.Fatal(err)
	}
	c.gh.SetPull("lucas/meu_app", c.pr, w.head, false, "clean")
	c.gh.SetPullAuthor("lucas/meu_app", c.pr, "lucas-mestre-mestre[bot]")
	w.fwd = localchan.NewLocalForwarder()
	c.run.Attach(c.orc, w.fwd)
	// the lease of the epoch is valid: the lock shows this epoch
	fence := guild.NewFence(guild.FenceConfig{Lock: epochLock{}, Clock: c.clk, Epoch: func() uint64 { return 1 }})
	c.run.BindFence(fence)
	go func() { _ = fence.Run(c.ctx) }()
	for i := 0; i < 2000 && !fence.LeaseValid(); i++ {
		c.clk.Advance(guild.LeaseInterval)
		runtime.Gosched()
	}
	if !fence.LeaseValid() {
		t.Fatal("the lease of the epoch did not become valid")
	}
	return w
}

// connect has the Owner connect the personal credential by the device flow.
func (w *approvalWorld) connect() {
	w.t.Helper()
	ch, err := w.run.Personal().Begin(w.ctx, w.owner.ID)
	if err != nil {
		w.t.Fatal(err)
	}
	w.gh.AuthorizeDevice(ch.UserCode, "lucas")
	w.clk.Advance(ch.Interval)
	if res, err := w.run.Personal().Poll(w.ctx, w.owner.ID); err != nil || res != github.PollConnected {
		w.t.Fatalf("connect = %q, %v", res, err)
	}
}

// request opens the request of approval of the PR of the table.
func (w *approvalWorld) request(typ classes.RequestType) int64 {
	w.t.Helper()
	id, err := w.orc.Chain.Request(w.ctx, chain.OpenRequest{Type: typ, Workspace: w.ws.Slug, Table: w.table.ID, About: "PR do épico",
		PR: &chain.PRRef{Repo: "lucas/Meu_App", Number: w.pr, SHA: w.head}, Target: approvals.PRTarget("lucas/Meu_App", w.pr, w.head, "main")})
	if err != nil {
		w.t.Fatal(err)
	}
	return id
}

func (w *approvalWorld) approve(id int64) {
	w.t.Helper()
	out, err := w.orc.Chain.DecideByPerson(w.ctx, chain.PersonDecision{Request: id, User: w.owner.ID, Level: string(users.Owner), Answer: chain.AnswerApprove})
	if err != nil || out.State != chain.Approved {
		w.t.Fatalf("decide = %+v, %v", out, err)
	}
}

func TestTheApprovalOfAPersonIsAppliedOnGitHubByTheGrandMasterAppAndInHerNameInTheHumanMode(t *testing.T) {
	t.Parallel()
	for _, typ := range []classes.RequestType{classes.RequestPR, classes.RequestMerge} {
		// the default mode: the App "Grão-Mestre" reviews, saying whose approval it is
		w := newApprovalWorld(t)
		w.approve(w.request(typ))
		reviews := w.gh.ReviewsOf("lucas/meu_app", w.pr)
		if len(reviews) != 1 || reviews[0].State != "APPROVED" || reviews[0].Commit != w.head || reviews[0].By != "lucas-mestre-grao-mestre[bot]" || !strings.Contains(reviews[0].Body, "Lucas") {
			t.Fatalf("%s: reviews = %+v, want the approval of the App on the commit of the request", typ, reviews)
		}
		// the human mode (the review of the App does not count): the person reviews, with her credential
		h := newApprovalWorld(t)
		h.connect()
		if err := (&approvals.Modes{DB: h.db, Clock: h.clk}).Set(h.ctx, h.ws.Slug, approvals.ModeHuman, "o review do App não conta"); err != nil {
			t.Fatal(err)
		}
		h.approve(h.request(typ))
		reviews = h.gh.ReviewsOf("lucas/meu_app", h.pr)
		if len(reviews) != 1 || reviews[0].State != "APPROVED" || reviews[0].Commit != h.head || !strings.EqualFold(reviews[0].By, "lucas") {
			t.Fatalf("%s: reviews in the human mode = %+v, want the approval of lucas herself", typ, reviews)
		}
	}
}

func TestWithoutHerCredentialInTheHumanModeNoReviewIsSentInHerName(t *testing.T) {
	t.Parallel()
	w := newApprovalWorld(t)
	if err := (&approvals.Modes{DB: w.db, Clock: w.clk}).Set(w.ctx, w.ws.Slug, approvals.ModeHuman, "o review do App não conta"); err != nil {
		t.Fatal(err)
	}
	w.approve(w.request(classes.RequestPR))
	if reviews := w.gh.ReviewsOf("lucas/meu_app", w.pr); len(reviews) != 0 {
		t.Fatalf("reviews = %+v; a review went out without her credential", reviews)
	}
}

func TestTheApprovalOfTheSettingsOfTheRepositoryCreatesTheRuleset(t *testing.T) {
	t.Parallel()
	w := newApprovalWorld(t)
	body := map[string]any{"name": github.RulesetName, "target": "branch", "enforcement": "active", "rules": []map[string]any{{"type": "pull_request"}}}
	target, err := jsonOf(approvals.SettingsTarget{Kind: approvals.KindRuleset, Repo: "lucas/Meu_App", Branch: "main", Body: body})
	if err != nil {
		t.Fatal(err)
	}
	w.gh.SetPro("lucas/meu_app", true) // a ruleset on a private repository needs the plan
	id, err := w.orc.Chain.Request(w.ctx, chain.OpenRequest{Type: classes.RequestRepoSettings, Workspace: w.ws.Slug, About: "ruleset", Target: target})
	if err != nil {
		t.Fatal(err)
	}
	w.approve(id)
	if got := w.gh.Rulesets("lucas/meu_app"); len(got) != 1 {
		t.Fatalf("rulesets = %v, want the one the Owner approved", got)
	}
}

func TestTheFlowAndThePolicyReadThePRsAsTheGrandMasterApp(t *testing.T) {
	t.Parallel()
	w := newApprovalWorld(t)
	src := w.run.source()
	facts, err := src.PullRequest(w.ctx, w.ws.Slug, chain.PRRef{Repo: "lucas/Meu_App", Number: w.pr, SHA: w.head})
	if err != nil || !facts.LabelsRead {
		t.Fatalf("facts = %+v, %v", facts, err)
	}
	if facts.OpenedByGrandMaster {
		t.Error("a PR opened by the App Mestre is flagged as opened by the Grão-Mestre")
	}
	// the author is the bot of the App "Grão-Mestre": the same App can not approve it
	w.gh.SetPullAuthor("lucas/meu_app", w.pr, "lucas-mestre-grao-mestre[bot]")
	if facts, err = src.PullRequest(w.ctx, w.ws.Slug, chain.PRRef{Repo: "lucas/Meu_App", Number: w.pr, SHA: w.head}); err != nil || !facts.OpenedByGrandMaster {
		t.Fatalf("facts = %+v, %v; want the PR flagged as opened by the Grão-Mestre", facts, err)
	}
	// the target of a request the gate ran on carries the tip of the base of the mirror
	req := chain.Request{Workspace: w.ws.Slug, Table: w.table.ID, Type: classes.RequestPR, PR: &chain.PRRef{Repo: "lucas/Meu_App", Number: w.pr, SHA: w.head}}
	req.Target, err = chain.WithBaseSHA(approvals.PRTarget("lucas/Meu_App", w.pr, w.head, "main"), "old")
	if err != nil {
		t.Fatal(err)
	}
	cur, err := (lateTargets{w.run}).Current(w.ctx, req)
	if err != nil || chain.BaseSHAOf(cur) == "" || chain.BaseSHAOf(cur) == "old" {
		t.Fatalf("current target = %s, %v; want the live tip of the base", cur, err)
	}
}

func TestTheApprovalTestOfTheSessionZeroRunsOverTheApps(t *testing.T) {
	t.Parallel()
	w := newApprovalWorld(t)
	review := w.run.AppReview()
	rules, err := review.ReadRules(w.ctx, w.ws.Repo, "main")
	if err != nil || rules.RequiresReview {
		t.Fatalf("rules = %+v, %v", rules, err)
	}
}

func jsonOf(v any) ([]byte, error) { return json.Marshal(v) }

func TestTheGrandMasterIsNotAnotherCharacterWhoReviewedAndTheSettingsTargetIsTheLiveOne(t *testing.T) {
	t.Parallel()
	w := newApprovalWorld(t)
	logins, err := w.run.botLogins(w.ctx)
	if err != nil || !logins["lucas-mestre-mestre[bot]"] || logins["lucas-mestre-grao-mestre[bot]"] {
		t.Fatalf("logins = %v, %v; want the bots of the characters and not the one of the Grão-Mestre", logins, err)
	}
	target := []byte(`{"kind":"ruleset","repo":"lucas/meu_app","body":{}}`)
	cur, err := (lateTargets{w.run}).Current(w.ctx, chain.Request{Type: classes.RequestRepoSettings, Target: target})
	if err != nil || string(cur) != string(target) {
		t.Fatalf("current = %s, %v; want the target itself: what is approved is what is applied", cur, err)
	}
}

func TestARequestNeverAppliesReviewMergeOrRulesetInAnotherRepositoryThanItsWorkspace(t *testing.T) {
	t.Parallel()
	w := newApprovalWorld(t)
	other := "lucas/outro"
	w.gh.AddRepo("lucas", "outro")
	// at the creation: PR, merge and settings of another repository are refused
	for _, typ := range []classes.RequestType{classes.RequestPR, classes.RequestMerge} {
		_, err := w.orc.Chain.Request(w.ctx, chain.OpenRequest{Type: typ, Workspace: w.ws.Slug, Table: w.table.ID, About: "PR de outro repositório",
			PR: &chain.PRRef{Repo: other, Number: w.pr, SHA: w.head}, Target: approvals.PRTarget(other, w.pr, w.head, "main")})
		if !errors.Is(err, chain.ErrWrongRepo) {
			t.Fatalf("%s of another repository = %v, want ErrWrongRepo", typ, err)
		}
	}
	target, err := jsonOf(approvals.SettingsTarget{Kind: approvals.KindRuleset, Repo: other, Branch: "main", Body: map[string]any{"name": github.RulesetName}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.orc.Chain.Request(w.ctx, chain.OpenRequest{Type: classes.RequestRepoSettings, Workspace: w.ws.Slug, About: "ruleset", Target: target}); !errors.Is(err, chain.ErrWrongRepo) {
		t.Fatalf("settings of another repository = %v, want ErrWrongRepo", err)
	}
	if open, _ := w.orc.Chain.Store().Open(w.ctx); len(open) != 0 {
		t.Fatalf("requests = %+v, want none", open)
	}

	// at the decision: a request that got in some other way is refused too, and GitHub is not touched
	before := len(w.gh.Requests())
	forged := chain.Request{ID: 99, Workspace: w.ws.Slug, Type: classes.RequestPR, PR: &chain.PRRef{Repo: other, Number: w.pr, SHA: w.head}, Target: target,
		Decision: &chain.Closing{State: chain.Approved, By: "user:" + w.owner.ID}}
	if err := w.run.approver.Load().Decided(w.ctx, chain.Result{Request: forged, Approved: true}); !errors.Is(err, chain.ErrWrongRepo) {
		t.Fatalf("approver = %v, want ErrWrongRepo", err)
	}
	forged.Type = classes.RequestRepoSettings
	if err := w.run.settings.Load().Decided(w.ctx, chain.Result{Request: forged, Approved: true}); !errors.Is(err, chain.ErrWrongRepo) {
		t.Fatalf("settings = %v, want ErrWrongRepo", err)
	}
	if n := len(w.gh.Requests()); n != before {
		t.Fatalf("GitHub got %d requests while refusing", n-before)
	}
	if got := w.gh.Rulesets("lucas/outro"); len(got) != 0 {
		t.Fatalf("rulesets = %v", got)
	}
}

type failingSink struct{}

func (failingSink) Deliver(context.Context, github.Watch, string, []github.Event) error {
	return errors.New("boom")
}

func TestAReaderThatFailsDoesNotHoldTheReadingOfTheEvents(t *testing.T) {
	t.Parallel()
	w := newApprovalWorld(t)
	failing := failingSink{}
	var logs bytes.Buffer
	i := isolated{inner: failing, log: slog.New(slog.NewTextHandler(&logs, nil)), name: "x"}
	if err := i.Deliver(w.ctx, github.Watch{Workspace: "w"}, "", nil); err != nil {
		t.Fatalf("deliver = %v; the failure must not reach the poller", err)
	}
	if !strings.Contains(logs.String(), "boom") {
		t.Errorf("the failure was not logged: %q", logs.String())
	}
}

func TestAReviewMadeOnGitHubItselfConcludesTheRequestThatWaitsForThePeople(t *testing.T) {
	t.Parallel()
	w := newApprovalWorld(t)
	poll, err := w.run.poller(w.orc)
	if err != nil {
		t.Fatal(err)
	}
	// a request of risk alto waits for the people from the start
	id, err := w.orc.Chain.Request(w.ctx, chain.OpenRequest{Type: classes.RequestPR, Workspace: w.ws.Slug, Table: w.table.ID, About: "PR do épico",
		Risk: classes.RiskResult{Risk: classes.RiskHigh},
		PR:   &chain.PRRef{Repo: "lucas/Meu_App", Number: w.pr, SHA: w.head}, Target: approvals.PRTarget("lucas/Meu_App", w.pr, w.head, "main")})
	if err != nil {
		t.Fatal(err)
	}
	if r, _ := w.orc.Chain.Get(w.ctx, id); r.State != chain.Open || r.Level.Kind != chain.LevelPeople {
		t.Fatalf("request = %s at %+v, want open with the people", r.State, r.Level)
	}
	w.gh.SetPermission("lucas/meu_app", "lucas", "write")
	w.clk.Advance(time.Minute)
	w.gh.AddReviewAt("lucas/meu_app", w.pr, "lucas", "APPROVED", w.head)
	if err := poll.Tick(w.ctx); err != nil {
		t.Fatal(err)
	}
	if r, _ := w.orc.Chain.Get(w.ctx, id); r.State != chain.Approved {
		t.Fatalf("request = %s after the review of lucas on GitHub, want approved", r.State)
	}
}

// call runs a command as the Grão-Mestre of the workspace, through the local channel.
func (w *approvalWorld) call(cmd string, flags map[string]any, args ...string) localchan.Response {
	w.t.Helper()
	gm, _, err := w.orc.Registry.GrandMaster(w.ctx, w.ws.Slug)
	if err != nil {
		w.t.Fatal(err)
	}
	return w.fwd.Forward(w.ctx, &localchan.Call{
		Request: localchan.Request{V: 1, ID: "c-" + cmd, AgentID: gm.ID, Cmd: cmd, Args: localchan.TextArgs(args...), Flags: flags},
		Caller:  localchan.Caller{Identified: true, AgentID: gm.ID, TerminalID: "t-" + gm.ID}})
}

// Part of CA-CLI-GM-04: over the real composition, `gh merge` opens the request of the merge in
// the chain (the commit and the base of the PR), waits, and merges the approved commit
// when the Owner approves; `gh settings apply` asks the Owner and the ruleset appears only
// when it approves.
func TestGhMergeAndGhSettingsApplyOverTheRealCompositionWaitForTheOwner(t *testing.T) {
	t.Parallel()
	w := newApprovalWorld(t)
	itoa := func(n int) string { return strconv.Itoa(n) }
	got := w.call("gh merge", nil, itoa(w.pr))
	if got.OK || got.Error == nil || got.Error.Code != "approval_pending" || got.ExitCode() != localchan.ExitPending {
		t.Fatalf("merge before the approval: %+v %+v", got, got.Error)
	}
	open, err := (&chain.Store{DB: w.db}).OpenPR(w.ctx, w.ws.Slug, "lucas/meu_app", w.pr)
	if err != nil || len(open) != 1 || open[0].Type != classes.RequestMerge || open[0].PR == nil || open[0].PR.SHA != w.head {
		t.Fatalf("open requests of the PR = %+v, %v; want the request of the merge at the commit", open, err)
	}
	if len(w.gh.Merges("lucas/meu_app")) != 0 {
		t.Fatal("merged before the approval")
	}
	w.approve(open[0].ID)
	got = w.call("gh merge", nil, itoa(w.pr))
	if !got.OK {
		t.Fatalf("merge after the approval: %+v", got.Error)
	}
	if ms := w.gh.Merges("lucas/meu_app"); len(ms) != 1 || ms[0].SHA != w.head || ms[0].Method != "merge" {
		t.Fatalf("GitHub merged %+v; want the approved commit by the default method", ms)
	}
	// the ruleset
	w.gh.SetPro("lucas/meu_app", true)
	body := `{"name":"` + github.RulesetName + `","target":"branch","enforcement":"active","rules":[{"type":"pull_request"}]}`
	got = w.call("gh settings", map[string]any{"body": body}, "apply")
	if got.OK || got.Error == nil || got.Error.Code != "approval_pending" {
		t.Fatalf("apply before the approval: %+v", got)
	}
	if n := len(w.gh.Rulesets("lucas/meu_app")); n != 0 {
		t.Fatalf("%d rulesets before the Owner approved", n)
	}
	all, err := (&chain.Store{DB: w.db}).Open(w.ctx)
	var settings []chain.Request
	for _, r := range all {
		if r.Type == classes.RequestRepoSettings {
			settings = append(settings, r)
		}
	}
	if err != nil || len(settings) != 1 {
		t.Fatalf("requests of settings = %+v, %v", settings, err)
	}
	w.approve(settings[0].ID)
	if n := len(w.gh.Rulesets("lucas/meu_app")); n != 1 {
		t.Fatalf("%d rulesets after the Owner approved; want 1", n)
	}
}
