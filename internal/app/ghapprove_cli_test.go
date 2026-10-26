package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mestre-ia/mestre/internal/approvals"
	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/cli"
	"github.com/mestre-ia/mestre/internal/github"
	"github.com/mestre-ia/mestre/internal/localchan"
	"github.com/mestre-ia/mestre/internal/orchestration/chain"
	"github.com/mestre-ia/mestre/internal/testkit/localrig"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// The commands of the Grão-Mestre on GitHub (docs/cli/grao-mestre.md) over the real
// composition: the real chain and policy, the real approvals.Approver and the double of
// GitHub.

const approveRepo = "lucas/meu_app"

// gmID is the id of the Grão-Mestre of the workspace.
func (w *approvalWorld) gmID() string {
	w.t.Helper()
	gm, ok, err := w.orc.Registry.GrandMaster(w.ctx, w.ws.Slug)
	if err != nil || !ok {
		w.t.Fatalf("grand master = %v, %v", ok, err)
	}
	return gm.ID
}

// fails says the answer is the refusal with the code and the exit.
func fails(got localchan.Response, code string, exit int) bool {
	return !got.OK && got.Error != nil && got.Error.Code == code && got.ExitCode() == exit
}

// CA-CLI-GM-03: the Grão-Mestre reads the PR with `gh review`, the PR gets a new commit, and
// `gh approve` ends with 7 head_changed: no review goes to GitHub, on either commit, although
// an approval stands for the commit he read.
func TestGhApproveAfterThePRMovedSinceTheReviewIsHeadChangedAndApprovesNothing(t *testing.T) {
	t.Parallel()
	w := newApprovalWorld(t)
	pr := strconv.Itoa(w.pr)
	// an approval of the Grão-Mestre that stands for the commit he reads
	if _, err := (&chain.Store{DB: w.db}).Insert(w.ctx, chain.Request{Workspace: w.ws.Slug, Table: w.table.ID, Type: classes.RequestPR, State: chain.Approved,
		Level: chain.Level{Kind: chain.LevelGrandMaster}, PR: &chain.PRRef{Repo: approveRepo, Number: w.pr, SHA: w.head}, CreatedAt: w.clk.Now(), ClosedAt: w.clk.Now(),
		Decision: &chain.Closing{State: chain.Approved, By: w.gmID(), Answer: chain.AnswerApprove}}); err != nil {
		t.Fatal(err)
	}
	if got := w.call("gh review", nil, pr); !got.OK {
		t.Fatalf("gh review: %+v", got.Error)
	}
	moved := strings.Repeat("b", 40)
	w.gh.SetPull(approveRepo, w.pr, moved, false, "clean")
	got := w.call("gh approve", map[string]any{"reason": "testes verdes"}, pr)
	if !fails(got, "head_changed", localchan.ExitConflict) {
		t.Fatalf("gh approve after a push: %+v %+v; want exit 7 head_changed", got, got.Error)
	}
	if rs := w.gh.ReviewsOf(approveRepo, w.pr); len(rs) != 0 {
		t.Fatalf("reviews = %+v; nothing may be approved", rs)
	}
	// a new reading of the new commit: the approval is for the old one, and still nothing goes out
	if got := w.call("gh review", nil, pr); !got.OK {
		t.Fatalf("gh review: %+v", got.Error)
	}
	if got = w.call("gh approve", map[string]any{"reason": "testes verdes"}, pr); !fails(got, "human_required", localchan.ExitDenied) {
		t.Fatalf("gh approve of the new commit: %+v %+v; want human_required (the approval is of the old commit)", got, got.Error)
	}
	if rs := w.gh.ReviewsOf(approveRepo, w.pr); len(rs) != 0 {
		t.Fatalf("reviews = %+v; nothing may be approved", rs)
	}
	// the control: back on the commit read and approved, the same command approves
	w.gh.SetPull(approveRepo, w.pr, w.head, false, "clean")
	if got := w.call("gh review", nil, pr); !got.OK {
		t.Fatalf("gh review: %+v", got.Error)
	}
	if got = w.call("gh approve", map[string]any{"reason": "testes verdes"}, pr); !got.OK {
		t.Fatalf("gh approve on the commit read: %+v", got.Error)
	}
	if rs := w.gh.ReviewsOf(approveRepo, w.pr); len(rs) != 1 || rs[0].State != "APPROVED" || rs[0].Commit != w.head {
		t.Fatalf("reviews = %+v; want the approval on the commit read", rs)
	}
}

// CA-CLI-GM-04: a PR that touches a sensitive area of the policy (pagamentos), with no
// approval of a user: the policy does not let the Grão-Mestre approve it alone, and `gh
// approve` ends with 3 human_required with nothing sent to GitHub; once a user approves that
// commit, the approval is published on GitHub.
func TestGhApproveOfAPRInASensitiveAreaNeedsTheApprovalOfAUserAndThenPublishesIt(t *testing.T) {
	t.Parallel()
	w := newApprovalWorld(t)
	pr := strconv.Itoa(w.pr)
	w.gh.AddPullFile(approveRepo, w.pr, "src/billing/charge.go", "@@ -1 +1 @@\n-total = 1\n+total = 2\n")
	w.gh.AddCheck(approveRepo, w.head, "build", "success")
	id := w.request(classes.RequestPR)
	// the policy: the Grão-Mestre can not decide it alone, the sensitive area is one reason
	if _, err := w.orc.Chain.Decide(w.ctx, chain.Decision{Request: id, By: w.gmID(), Answer: chain.AnswerApprove, Reason: "ok"}); !isRefusal(err, chain.CodeHumanRequired) ||
		!strings.Contains(err.Error(), "área sensível pagamentos") {
		t.Fatalf("the Grão-Mestre decides = %v; want human_required for the sensitive area", err)
	}
	if got := w.call("gh review", nil, pr); !got.OK {
		t.Fatalf("gh review: %+v", got.Error)
	}
	got := w.call("gh approve", map[string]any{"reason": "está tudo verde"}, pr)
	if !fails(got, "human_required", localchan.ExitDenied) {
		t.Fatalf("gh approve with no approval of a user: %+v %+v; want exit 3 human_required", got, got.Error)
	}
	if rs := w.gh.ReviewsOf(approveRepo, w.pr); len(rs) != 0 {
		t.Fatalf("reviews = %+v; nothing may be approved", rs)
	}
	if r, err := w.orc.Chain.Get(w.ctx, id); err != nil || r.State != chain.Open || r.Decision != nil {
		t.Fatalf("the request is %s (%+v, %v); want it open and undecided", r.State, r.Decision, err)
	}
	// the approval of a user for that commit: it is published
	w.approve(id)
	rs := w.gh.ReviewsOf(approveRepo, w.pr)
	if len(rs) != 1 || rs[0].State != "APPROVED" || rs[0].Commit != w.head || rs[0].By != "lucas-mestre-grao-mestre[bot]" || !strings.Contains(rs[0].Body, "Lucas") {
		t.Fatalf("reviews = %+v; want the approval published on the commit, naming the user", rs)
	}
	if got = w.call("gh approve", map[string]any{"reason": "está tudo verde"}, pr); !got.OK {
		t.Fatalf("gh approve after the approval of the user: %+v", got.Error)
	}
	if rs = w.gh.ReviewsOf(approveRepo, w.pr); len(rs) != 2 || rs[1].State != "APPROVED" || rs[1].Commit != w.head || !strings.Contains(rs[1].Body, "Lucas") {
		t.Fatalf("reviews = %+v; want gh approve to publish the approval of the user", rs)
	}
}

func isRefusal(err error, code string) bool {
	var ref *chain.Refusal
	return errors.As(err, &ref) && ref.Code == code
}

// cliRun runs the real `mestre` CLI in the terminal of the Grão-Mestre, through the local
// channel, and gives the exit code and the answer it printed with --json.
func (w *approvalWorld) cliRun(args ...string) (int, map[string]any) {
	w.t.Helper()
	gm := w.gmID()
	rig := localrig.New(clock.Real{}, localrig.Options{Config: localchan.Config{Forwarder: w.fwd}})
	rig.Start(w.t)
	term, host := "t-"+gm, "proc-"+gm
	rig.Character(term, gm)
	rig.Process(host, term)
	var stdout, stderr bytes.Buffer
	code := cli.Run(w.ctx, cli.Env{
		Getenv: func(k string) string {
			if k == localchan.EnvAgentID {
				return gm
			}
			return ""
		},
		Stdout: &stdout, Stderr: &stderr, Clock: rig.Clock, Cwd: w.t.TempDir(), Version: "1.0.0",
		Dial: func(string) localchan.Dialer { return rig.Dialer(host) },
	}, append(args, "--json"))
	var out map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		w.t.Fatalf("stdout is not JSON: %q (stderr %q): %v", stdout.String(), stderr.String(), err)
	}
	return code, out
}

// CA-CLI-GM-05: `mestre gh settings apply --file r.json` ends with 9 and nothing changes on
// GitHub before the approval: the ruleset of the file is only a request to the Owner, and
// it appears on GitHub only when the Owner approves.
func TestGhSettingsApplyWithAFileEndsPendingAndChangesNothingOnGitHubBeforeTheApproval(t *testing.T) {
	t.Parallel()
	w := newApprovalWorld(t)
	w.gh.SetPro(approveRepo, true)
	file := filepath.Join(t.TempDir(), "r.json")
	body := `{"name":"` + github.RulesetName + `","target":"branch","enforcement":"active","rules":[{"type":"pull_request"}]}`
	if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	reviews, merges := len(w.gh.ReviewsOf(approveRepo, w.pr)), len(w.gh.Merges(approveRepo))
	code, out := w.cliRun("gh", "settings", "apply", "--file", file)
	if code != localchan.ExitPending {
		t.Fatalf("exit = %d (%v); want 9", code, out)
	}
	if e, _ := out["error"].(map[string]any); e == nil || e["code"] != "approval_pending" {
		t.Fatalf("answer = %v; want approval_pending", out)
	}
	if n := len(w.gh.Rulesets(approveRepo)); n != 0 {
		t.Fatalf("%d rulesets on GitHub before the approval", n)
	}
	if len(w.gh.ReviewsOf(approveRepo, w.pr)) != reviews || len(w.gh.Merges(approveRepo)) != merges {
		t.Fatal("something changed on GitHub before the approval")
	}
	open, err := (&chain.Store{DB: w.db}).Open(w.ctx)
	if err != nil {
		t.Fatal(err)
	}
	var settings []chain.Request
	for _, r := range open {
		if r.Type == classes.RequestRepoSettings {
			settings = append(settings, r)
		}
	}
	if len(settings) != 1 || !strings.Contains(string(settings[0].Target), `"enforcement":"active"`) {
		t.Fatalf("requests of settings = %+v; want the one with the body of the file", settings)
	}
	w.approve(settings[0].ID)
	if got := w.gh.Rulesets(approveRepo); len(got) != 1 || got[0]["name"] != github.RulesetName {
		t.Fatalf("rulesets after the approval = %v; want the one of the file", got)
	}
}

// With the Mestre locked (/travar), `gh approve`, `gh merge`, `gh settings apply` and `gh
// review` with a review end with 3 locked and nothing is done on GitHub, even with an
// approval that stands (docs/cli/grao-mestre.md).
func TestTheCommandsOfTheGrandMasterOnGitHubAreLockedAndDoNothingWhileTheMestreIsLocked(t *testing.T) {
	t.Parallel()
	w := newApprovalWorld(t)
	pr := strconv.Itoa(w.pr)
	w.gh.SetPro(approveRepo, true)
	st := &chain.Store{DB: w.db}
	// approvals of the Owner that stand for the review and for the merge of the commit
	for _, typ := range []classes.RequestType{classes.RequestPR, classes.RequestMerge} {
		if _, err := st.Insert(w.ctx, chain.Request{Workspace: w.ws.Slug, Table: w.table.ID, Type: typ, State: chain.Approved, Level: chain.Level{Kind: chain.LevelPeople},
			PR: &chain.PRRef{Repo: approveRepo, Number: w.pr, SHA: w.head}, Target: approvals.PRTarget(approveRepo, w.pr, w.head, "main"), CreatedAt: w.clk.Now(), ClosedAt: w.clk.Now(),
			Decision: &chain.Closing{State: chain.Approved, By: "user:" + w.owner.ID, Answer: chain.AnswerApprove}}); err != nil {
			t.Fatal(err)
		}
	}
	if got := w.call("gh review", nil, pr); !got.OK {
		t.Fatalf("gh review: %+v", got.Error)
	}
	before, err := st.Open(w.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.run.lock.Lock(w.ctx, w.owner); err != nil {
		t.Fatal(err)
	}
	body := `{"name":"` + github.RulesetName + `","target":"branch","enforcement":"active","rules":[{"type":"pull_request"}]}`
	for _, c := range []struct {
		cmd   string
		flags map[string]any
		args  []string
	}{
		{"gh approve", map[string]any{"reason": "ok"}, []string{pr}},
		{"gh merge", nil, []string{pr}},
		{"gh settings", map[string]any{"body": body}, []string{"apply"}},
		{"gh review", map[string]any{"comment": true, "body": "ok"}, []string{pr}},
		{"gh review", map[string]any{"request-changes": true, "body": "falta teste"}, []string{pr}},
	} {
		if got := w.call(c.cmd, c.flags, c.args...); !fails(got, "locked", localchan.ExitDenied) {
			t.Errorf("%s %v while locked: %+v %+v; want exit 3 locked", c.cmd, c.flags, got, got.Error)
		}
	}
	if rs := w.gh.ReviewsOf(approveRepo, w.pr); len(rs) != 0 {
		t.Errorf("reviews while locked = %+v", rs)
	}
	if ms := w.gh.Merges(approveRepo); len(ms) != 0 {
		t.Errorf("merges while locked = %+v", ms)
	}
	if rs := w.gh.Rulesets(approveRepo); len(rs) != 0 {
		t.Errorf("rulesets while locked = %v", rs)
	}
	if after, err := st.Open(w.ctx); err != nil || len(after) != len(before) {
		t.Errorf("open requests %d -> %d (%v); a locked command asked for something", len(before), len(after), err)
	}
}
