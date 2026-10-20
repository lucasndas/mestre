package quests_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/orchestration/quests"
)

// verifyRules makes every class verify, and ask for a proof when proof is set.
type verifyRules struct{ proof bool }

func (v verifyRules) DoneRequires(context.Context, string, string, string) (classes.DoneRequires, error) {
	d := classes.DoneRequires{Verify: true, Proof: classes.ProofNone}
	if v.proof {
		d.Proof = classes.ProofAll
	}
	return d, nil
}

// qaRules is the class QA of CA-PROVA-13: it asks for proof and verification.
type qaRules struct{}

func (qaRules) DoneRequires(context.Context, string, string, string) (classes.DoneRequires, error) {
	return classes.DoneRequires{Proof: classes.ProofAll, Verify: true}, nil
}

// fakeVerifier is the executor of the verification: it gives the results it
// was told to, one for each run, and can hold a run until released.
type fakeVerifier struct {
	mu        sync.Mutex
	confirmed bool
	results   []quests.VerifyResult
	runs      []quests.VerifyRequest
	hold      chan struct{}
	started   chan struct{}
}

func (f *fakeVerifier) Confirmed(context.Context, string) (bool, error) { return f.confirmed, nil }

func (f *fakeVerifier) Verify(_ context.Context, r quests.VerifyRequest) (quests.VerifyResult, error) {
	f.mu.Lock()
	f.runs = append(f.runs, r)
	hold, started := f.hold, f.started
	var res quests.VerifyResult
	if len(f.results) > 0 {
		res, f.results = f.results[0], f.results[1:]
	} else {
		res = quests.VerifyResult{OK: true}
	}
	f.mu.Unlock()
	if started != nil {
		close(started)
	}
	if hold != nil {
		<-hold
	}
	return res, nil
}

func (f *fakeVerifier) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.runs)
}

func red(cmd, out string, places ...string) quests.VerifyResult {
	return quests.VerifyResult{Command: cmd, Output: out, Places: places}
}

func verifyWorld(t *testing.T, v *fakeVerifier, rules quests.DoneRules) *world {
	t.Helper()
	return newWorld(t, worldOpts{done: rules, verifier: v})
}

func devOnly(extra string) string {
	return "name: Uma\n" + extra + "steps:\n  - {id: dev, sheet: Dev Claude}\n"
}

func resultOf(t *testing.T, r localchan.Response) quests.DoneResult {
	t.Helper()
	if !r.OK {
		t.Fatalf("done = %+v", r.Error)
	}
	var res quests.DoneResult
	if err := json.Unmarshal(r.Data, &res); err != nil {
		t.Fatal(err)
	}
	return res
}

// systemItems are the texts of the items system of a character.
func (w *world) systemItems(name string) string {
	w.t.Helper()
	items, err := w.orc.Inbox.Store().All(w.ctx, w.char(name).ID)
	if err != nil {
		w.t.Fatal(err)
	}
	var b strings.Builder
	for _, it := range items {
		if it.Kind == "system" {
			b.WriteString(it.Text + "\n")
		}
	}
	return b.String()
}

// CA-PROVA-03
func TestARedVerificationLeavesTheStepRunningWithTheOutputAndAGreenOneEndsIt(t *testing.T) {
	t.Parallel()
	v := &fakeVerifier{confirmed: true, results: []quests.VerifyResult{red("go test ./...", "--- FAIL: TestLogin\nlogin_test.go:7: x"), {OK: true}}}
	w := verifyWorld(t, v, verifyRules{})
	n := w.issueOf("- [ ] C1 um\n")
	q := w.run(w.create(devOnly(fmt.Sprintf("issue: %d\n", n))))
	w.commit("feat: login", map[string]string{"login.go": "1"})

	res := resultOf(t, w.done("Dev Claude 1", map[string]any{"proof": []string{"C1=TestLogin"}}, "pronto"))
	if res.Verification != "pending" {
		t.Fatalf("verification = %q, want pending", res.Verification)
	}
	w.orc.Quests.WaitVerifications()
	w.wantStates(q, map[string]quests.StepState{"dev": quests.StepRunning})
	if got := w.step(q, "dev").Returns; got != 1 {
		t.Errorf("returns = %d, want a volta counted", got)
	}
	items := w.systemItems("Dev Claude 1")
	for _, want := range []string{"go test ./...", "--- FAIL: TestLogin", "login_test.go:7: x"} {
		if !strings.Contains(items, want) {
			t.Errorf("the character was not told %q:\n%s", want, items)
		}
	}
	if strings.Contains(w.issueBody(n), "[x] C1") {
		t.Error("a proof was marked before the verification passed")
	}

	resultOf(t, w.done("Dev Claude 1", map[string]any{"proof": []string{"C1=TestLogin"}}, "corrigido"))
	w.orc.Quests.WaitVerifications()
	if got := w.quest(q.ID); got.State != quests.Completed {
		t.Fatalf("the quest is %s after the green verification", got.State)
	}
	if body := w.issueBody(n); !strings.Contains(body, "[x] C1") || !strings.Contains(body, "verificada pelo motor") || strings.Contains(body, "declarada") {
		t.Errorf("the proof is not verified by the engine:\n%s", body)
	}
	if v.count() != 2 || v.runs[0].Branch != w.table.Branch {
		t.Errorf("runs = %+v", v.runs)
	}
}

// CA-PROVA-04
func TestAFileNotCommittedRefusesTheDoneOfAClassThatVerifies(t *testing.T) {
	t.Parallel()
	v := &fakeVerifier{confirmed: true}
	w := verifyWorld(t, v, verifyRules{})
	q := w.run(w.create(devOnly("")))
	dir, err := w.orc.Tables.Worktree(w.ctx, w.slug, w.table.ID, "cidadela")
	if err != nil {
		t.Fatal(err)
	}
	w.git.Handle(dir).SetFile("notes.md", "wip")
	r := w.done("Dev Claude 1", nil, "pronto")
	if r.OK || r.Error.Code != "uncommitted_changes" || r.ExitCode() != localchan.ExitConflict || !strings.Contains(r.Error.Message, "notes.md") {
		t.Fatalf("done = %+v", r)
	}
	w.wantStates(q, map[string]quests.StepState{"dev": quests.StepRunning})
	if v.count() != 0 {
		t.Error("a verification started over a branch that is not the work")
	}
	// an ignored file is not a change; the file that is a change, in a folder,
	// is listed with the path the way git gives it
	w.git.Handle(dir).SetFile("notes.md", "")
	w.git.Handle(dir).Data().AddExclude("*.log")
	w.git.Handle(dir).SetFile("run.log", "ruído")
	w.git.Handle(dir).SetFile("docs/rascunho.md", "wip")
	r = w.done("Dev Claude 1", nil, "pronto")
	if r.OK || r.Error.Code != "uncommitted_changes" || !strings.Contains(r.Error.Message, "docs/rascunho.md") || strings.Contains(r.Error.Message, "run.log") {
		t.Fatalf("done with a file in a folder and an ignored one = %+v", r)
	}
}

// CA-PROVA-04, the half of no change: with nothing left out of a commit the
// verification begins.
func TestWithNothingToCommitTheDoneOfAClassThatVerifiesStartsTheVerification(t *testing.T) {
	t.Parallel()
	v := &fakeVerifier{confirmed: true}
	w := verifyWorld(t, v, verifyRules{})
	q := w.run(w.create(devOnly("")))
	dir, err := w.orc.Tables.Worktree(w.ctx, w.slug, w.table.ID, "cidadela")
	if err != nil {
		t.Fatal(err)
	}
	w.git.Handle(dir).Data().AddExclude("*.log")
	w.git.Handle(dir).SetFile("run.log", "ruído") // ignored: not a change
	res := resultOf(t, w.done("Dev Claude 1", nil, "pronto"))
	w.orc.Quests.WaitVerifications()
	if v.count() != 1 {
		t.Errorf("verifications = %d, want one (%+v)", v.count(), res)
	}
	if got := w.quest(q.ID); got.State != quests.Completed {
		t.Errorf("the quest is %s after the green verification", got.State)
	}
}

// CA-PROVA-05
func TestWithoutAConfirmedCommandTheStepEndsUnverifiedAndTheMasterIsTold(t *testing.T) {
	t.Parallel()
	v := &fakeVerifier{confirmed: false}
	w := verifyWorld(t, v, verifyRules{})
	n := w.issueOf("- [ ] C1 um\n")
	q := w.run(w.create(devOnly(fmt.Sprintf("issue: %d\n", n))))
	res := resultOf(t, w.done("Dev Claude 1", map[string]any{"proof": []string{"C1=teste"}}, "pronto"))
	if res.Verification != "unverified" || res.QuestState != "completed" {
		t.Fatalf("result = %+v", res)
	}
	if v.count() != 0 {
		t.Error("it verified with no command")
	}
	if body := w.issueBody(n); !strings.Contains(body, "declarada por") || strings.Contains(body, "verificada pelo motor") {
		t.Errorf("the proof:\n%s", body)
	}
	var told bool
	items, _ := w.orc.Inbox.Store().All(w.ctx, w.master.ID)
	for _, it := range items {
		told = told || strings.Contains(it.Text, "sem verificação")
	}
	if !told {
		t.Error("the Master of the table was not told")
	}
	_ = q
}

// CA-PROVA-10
func TestTheSameVerificationFailureThreeTimesPausesTheQuest(t *testing.T) {
	t.Parallel()
	same := red("go test", "src/a.go:7: x", "src/a.go:7")
	// on Windows the same place comes with backslashes and a column: still the same failure
	onWindows := red("go test", `src\a.go:7:3: x`, `src\a.go:7:3`)
	v := &fakeVerifier{confirmed: true, results: []quests.VerifyResult{same, onWindows, same}}
	w := verifyWorld(t, v, verifyRules{})
	q := w.run(w.create("name: Uma\nsame_failure: 3\nsteps:\n  - {id: dev, sheet: Dev Claude}\n"))
	for i := 0; i < 3; i++ {
		resultOf(t, w.done("Dev Claude 1", nil, fmt.Sprintf("tentativa %d", i+1)))
		w.orc.Quests.WaitVerifications()
	}
	got := w.quest(q.ID)
	if got.State != quests.Paused || got.PausedReason != "mesma falha repetida" {
		t.Fatalf("the quest is %s (%q)", got.State, got.PausedReason)
	}
	var event string
	for _, d := range w.machines.Delivered(w.master) {
		if strings.Contains(d, "return_limit") {
			event = d
		}
	}
	if !strings.Contains(event, "src/a.go:7") || !strings.Contains(event, "3 tentativas") {
		t.Errorf("the event: %q", event)
	}
}

// CA-PROVA-13
func TestAReturnOfAClassThatAsksForEverythingAsksForNothing(t *testing.T) {
	t.Parallel()
	v := &fakeVerifier{confirmed: true}
	w := verifyWorld(t, v, qaRules{})
	n := w.issueOf("- [ ] C1 um\n- [ ] C2 dois\n")
	q := w.run(w.create(fmt.Sprintf("name: Rev\nissue: %d\nsteps:\n  - {id: dev, sheet: Dev Claude}\n  - {id: qa, sheet: QA Antigravity, after: [dev], can_return_to: dev}\n", n)))
	// the dev ends its step with the verification green and the proofs
	resultOf(t, w.done("Dev Claude 1", map[string]any{"proof": []string{"C1=a", "C2=b"}}, "pronto"))
	w.orc.Quests.WaitVerifications()
	runs := v.count()
	dir, err := w.orc.Tables.Worktree(w.ctx, w.slug, w.table.ID, "cidadela")
	if err != nil {
		t.Fatal(err)
	}
	w.git.Handle(dir).SetFile("notes.md", "wip")
	res := resultOf(t, w.done("QA Antigravity 1", map[string]any{"return": "bug", "cause": "test", "where": []string{"src/a.go:7"}}, "reprovado"))
	if res.ReturnedTo == nil || *res.ReturnedTo != "dev" {
		t.Fatalf("the return = %+v", res)
	}
	if v.count() != runs {
		t.Error("a return ran the verification")
	}
	if got := w.step(w.quest(q.ID), "qa").Returns; got != 1 {
		t.Errorf("returns = %d, want one", got)
	}
}

// CA-PROVA-14
func TestASecondDoneDuringTheVerificationIsRefusedAndATimeoutIsRed(t *testing.T) {
	t.Parallel()
	v := &fakeVerifier{confirmed: true, hold: make(chan struct{}), started: make(chan struct{}),
		results: []quests.VerifyResult{{TimedOut: true, Command: "go test", Output: "killed"}}}
	w := verifyWorld(t, v, verifyRules{})
	q := w.run(w.create(devOnly("")))
	resultOf(t, w.done("Dev Claude 1", nil, "pronto"))
	<-v.started
	r := w.done("Dev Claude 1", nil, "de novo")
	if r.OK || r.Error.Code != "verification_running" || r.ExitCode() != localchan.ExitConflict {
		t.Fatalf("a second done = %+v", r)
	}
	close(v.hold)
	w.orc.Quests.WaitVerifications()
	if items := w.systemItems("Dev Claude 1"); !strings.Contains(items, "tempo esgotado") || !strings.Contains(items, "go test") {
		t.Errorf("a timeout is red with its reason:\n%s", items)
	}
	w.wantStates(q, map[string]quests.StepState{"dev": quests.StepRunning})
	if got := w.step(q, "dev").Returns; got != 1 {
		t.Errorf("returns = %d", got)
	}
	// the limits of the text of the done and of the cause
	if r := w.done("Dev Claude 1", map[string]any{"proof": []string{"C1=" + strings.Repeat("x", quests.MaxProof+1)}}, "x"); r.OK || r.ExitCode() != localchan.ExitLimit {
		t.Errorf("a proof of 501 characters = %+v", r)
	}
	if r := w.done("Dev Claude 1", map[string]any{"cause": "urgente"}, "x"); r.OK || r.ExitCode() != localchan.ExitUsage {
		t.Errorf("--cause urgente = %+v", r)
	}
}

// The evidence of a notice about a table (CA-BLOQ-01): the engine keeps the last
// verification of the table that failed, until one passes.
func TestTheLastRedVerificationOfATableIsKeptUntilOnePasses(t *testing.T) {
	t.Parallel()
	v := &fakeVerifier{confirmed: true, results: []quests.VerifyResult{red("go test ./...", "--- FAIL: TestCheckout\n    checkout_test.go:22: missing STRIPE_TEST_KEY", "checkout_test.go:22"), {OK: true}}}
	w := verifyWorld(t, v, verifyRules{})
	w.run(w.create(devOnly("")))
	if _, ok := w.orc.Quests.LastRed(w.slug, w.table.ID); ok {
		t.Fatal("a red verification before any ran")
	}
	resultOf(t, w.done("Dev Claude 1", nil, "tentativa 1"))
	w.orc.Quests.WaitVerifications()
	got, ok := w.orc.Quests.LastRed(w.slug, w.table.ID)
	if !ok || got.Command != "go test ./..." || !strings.Contains(got.Output, "missing STRIPE_TEST_KEY") || len(got.Places) != 1 || got.Places[0] != "checkout_test.go:22" {
		t.Fatalf("last red = %+v, %v", got, ok)
	}
	if _, ok := w.orc.Quests.LastRed(w.slug, "outra-mesa"); ok {
		t.Error("the red verification of a table is of another")
	}
	resultOf(t, w.done("Dev Claude 1", nil, "tentativa 2"))
	w.orc.Quests.WaitVerifications()
	if _, ok := w.orc.Quests.LastRed(w.slug, w.table.ID); ok {
		t.Error("a green verification left the red one as the evidence")
	}
}
