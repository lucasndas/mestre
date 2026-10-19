package chain_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/orchestration/chain"
)

// green is a PR the policy lets the Grão-Mestre approve: green CI, a finished
// review and n small files outside the sensitive areas.
func green(n int) chain.PRFacts {
	f := chain.PRFacts{LabelsRead: true, CIGreen: true, ReviewDone: true}
	for i := range n {
		f.Files = append(f.Files, chain.PRFile{Path: fmt.Sprintf("internal/pkg/file%d.go", i), Added: 10, Removed: 2})
	}
	return f
}

// prRequest opens a request of a PR, born at the Grão-Mestre.
func (w *world) prRequest(typ classes.RequestType, number int, f chain.PRFacts) int64 {
	w.t.Helper()
	w.prs.set(number, f)
	id, err := w.ch.Request(w.ctx, chain.OpenRequest{Type: typ, Workspace: w.slug, About: fmt.Sprintf("PR %d", number),
		PR: &chain.PRRef{Repo: "lucas/Meu_App", Number: number, SHA: "abc123"}})
	if err != nil {
		w.t.Fatal(err)
	}
	return id
}

// humanRequired is the refusal of the Grão-Mestre's approval that leaves the
// request at his level, with why in the message.
func (w *world) humanRequired(id int64, why ...string) {
	w.t.Helper()
	ref := w.refusal(chain.Decision{Request: id, By: w.gm.ID, Answer: chain.AnswerApprove, Reason: "parece certo"})
	if ref.Code != chain.CodeHumanRequired || ref.Climbed || !contains(ref.Message, why...) {
		w.t.Fatalf("approval of #%d = %+v, want human_required (not climbed) citing %v", id, ref, why)
	}
	if r := w.request(id); r.State != chain.Open || r.Level.Kind != chain.LevelGrandMaster {
		w.t.Fatalf("after human_required #%d is %s at %+v, want open at the Grão-Mestre", id, r.State, r.Level)
	}
}

// CA-APROV-POL-01
func TestTheGrandMasterApprovesAPRThatPassesTheConditionsAndTheReasonIsKept(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	id := w.prRequest(classes.RequestPR, 12, green(10))
	out := w.decide(w.gm, id, chain.AnswerApprove, "dentro do escopo da issue")
	if out.State != chain.Approved {
		t.Fatalf("outcome = %+v, want approved", out)
	}
	audit, err := w.ch.Audit(w.ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	last := audit[len(audit)-1]
	if last.Outcome != "accepted" || !strings.Contains(last.Reason, "dentro do escopo da issue") {
		t.Fatalf("audit = %+v, want the accepted decision with its reason", last)
	}
}

// CA-APROV-POL-02
func TestAPRThatTouchesAMigrationGoesToThePeopleOnlyWhenTheGrandMasterAsksOrHisTimeEnds(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	f := green(2)
	f.Files = append(f.Files, chain.PRFile{Path: "db/migrations/0003.sql", Added: 12})
	id := w.prRequest(classes.RequestPR, 21, f)
	w.humanRequired(id, "migracoes", "db/migrations/0003.sql")
	if got := w.users.askedFor(id); len(got) != 0 {
		t.Fatalf("the people were asked before the Grão-Mestre took it to them: %+v", got)
	}
	// asking is his answer "Subir agora": same id, now with the people
	w.decide(w.gm, id, chain.AnswerClimb, "migração")
	if r := w.request(id); r.State != chain.Open || r.Level.Kind != chain.LevelPeople || len(w.users.askedFor(id)) == 0 {
		t.Fatalf("after asking: %s at %+v, asked %d times", r.State, r.Level, len(w.users.askedFor(id)))
	}
	// or his time (10 min, fake clock) runs out, with the same id
	id2 := w.prRequest(classes.RequestPR, 22, f)
	w.humanRequired(id2, "migracoes")
	w.clk.Advance(10*time.Minute + time.Second)
	if r := w.request(id2); r.ID != id2 || r.Level.Kind != chain.LevelPeople {
		t.Fatalf("after the time of the Grão-Mestre #%d is at %+v, want the people", id2, r.Level)
	}
}

// CA-APROV-POL-03
func TestAQuorumRuleSendsAGreenMergeToThePeopleAskingForTheApprovals(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	rules := chain.DefaultRules()
	rules.QuorumRules = []chain.QuorumRule{{Type: classes.RequestMerge, Approvals: 2}}
	w.policy.setRules(rules)
	id := w.prRequest(classes.RequestMerge, 30, green(3))
	if w.level(id) != chain.LevelPeople {
		t.Fatalf("the merge is at %+v, want the people", w.request(id).Level)
	}
	asked := w.users.askedFor(id)
	if len(asked) == 0 || asked[0].Approvals != 2 {
		t.Fatalf("question = %+v, want 2 approvals", asked)
	}
	// another type is not touched by the rule
	other := w.prRequest(classes.RequestPR, 31, green(3))
	if w.level(other) != chain.LevelGrandMaster {
		t.Fatalf("a PR is at %+v, want the Grão-Mestre", w.request(other).Level)
	}
}

// CA-APROV-POL-15
func TestAnAltoPermissionAsksOnlyAdministratorsAndTheOwner(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	dev := w.recruit("Dev")
	id := w.shell(dev, `psql -c "DROP TABLE users"`)
	if w.level(id) != chain.LevelPeople {
		t.Fatalf("alto is at %+v, want the people", w.request(id).Level)
	}
	asked := w.users.askedFor(id)
	if len(asked) == 0 || asked[0].MinLevel != classes.LevelAdmin {
		t.Fatalf("question = %+v, want the minimum level Administrator", asked)
	}
	_, err := w.ch.DecideByPerson(w.ctx, chain.PersonDecision{Request: id, User: "ana", Level: "approver", Answer: chain.AnswerApprove})
	if ref, ok := err.(*chain.Refusal); !ok || ref.Code != chain.CodeHumanRequired {
		t.Fatalf("an Approver approving alto = %v, want human_required", err)
	}
	if w.request(id).State != chain.Open {
		t.Fatal("the refused approval closed the request")
	}
	if out, err := w.ch.DecideByPerson(w.ctx, chain.PersonDecision{Request: id, User: "bia", Level: "administrator", Answer: chain.AnswerApprove}); err != nil || out.State != chain.Approved {
		t.Fatalf("an Administrator approving alto = %+v, %v", out, err)
	}
}

// CA-APROV-POL-17
func TestASensitiveWordInAnAddedLineSendsThePRToAPersonButARemovedOneDoesNot(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	add := green(1)
	add.Files = append(add.Files, chain.PRFile{Path: "scripts/limpa.sh", Added: 1, AddedLines: []string{"psql -c 'DROP TABLE x'"}})
	w.humanRequired(w.prRequest(classes.RequestPR, 40, add), "DROP", "scripts/limpa.sh")
	// a word inside another is not the word; the removal of the line does not trigger
	rem := green(1)
	rem.Files = append(rem.Files, chain.PRFile{Path: "scripts/limpa.sh", Removed: 1, Added: 1, AddedLines: []string{"echo DROPLET"}})
	id := w.prRequest(classes.RequestPR, 41, rem)
	if out := w.decide(w.gm, id, chain.AnswerApprove, "só tirou o DROP"); out.State != chain.Approved {
		t.Fatalf("outcome = %+v", out)
	}
}

// CA-APROV-POL-18
func TestTheLinesIgnoredDoNotCountAndAnIncompleteDiffIsHumanRequired(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	rules := chain.DefaultRules()
	rules.MaxLines = 600
	w.policy.setRules(rules)
	big := green(1)
	big.Files[0].Added, big.Files[0].Removed = 30, 10
	big.Files = append(big.Files, chain.PRFile{Path: "package-lock.json", Added: 3000})
	if out := w.decide(w.gm, w.prRequest(classes.RequestPR, 50, big), chain.AnswerApprove, "40 linhas de código"); out.State != chain.Approved {
		t.Fatalf("outcome = %+v, want approved with 40 counted lines", out)
	}
	over := green(1)
	over.Files[0].Added = 601
	w.humanRequired(w.prRequest(classes.RequestPR, 51, over), "603 linhas")
	cut := green(1)
	cut.Incomplete = true
	w.humanRequired(w.prRequest(classes.RequestPR, 52, cut), chain.ReasonIncompleteDiff)
	nopatch := green(1)
	nopatch.Files = append(nopatch.Files, chain.PRFile{Path: "src/grande.go", PatchMissing: true})
	w.humanRequired(w.prRequest(classes.RequestPR, 53, nopatch), chain.ReasonIncompleteDiff, "src/grande.go")
	// a PR the engine could not read at all
	w.prs.err = fmt.Errorf("github: 502")
	w.humanRequired(w.prRequest(classes.RequestPR, 54, green(1)), chain.ReasonIncompleteDiff)
}

// CA-APROV-POL-19
func TestThePRLabelMestreHumanisNeverApprovedByTheGrandMaster(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	f := green(2)
	f.Labels = []string{"bug", chain.HumanLabel}
	w.humanRequired(w.prRequest(classes.RequestPR, 60, f), chain.HumanLabel)
	f.Labels = []string{"bug"}
	if out := w.decide(w.gm, w.prRequest(classes.RequestPR, 61, f), chain.AnswerApprove, "sem o rótulo"); out.State != chain.Approved {
		t.Fatalf("outcome = %+v", out)
	}
	unread := green(2)
	unread.LabelsRead = false
	w.humanRequired(w.prRequest(classes.RequestPR, 62, unread), chain.ReasonIncompleteDiff)
}

// CA-APROV-POL-20
func TestASecretFileSendsThePRToAPersonButADocDoesNot(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	secret := green(1)
	secret.Files = append(secret.Files, chain.PRFile{Path: "config/service-account-prod.json", Added: 8})
	w.humanRequired(w.prRequest(classes.RequestPR, 70, secret), "permissoes_e_segredos", "config/service-account-prod.json")
	doc := green(1)
	doc.Files = append(doc.Files, chain.PRFile{Path: "docs/conta.md", Added: 8})
	if out := w.decide(w.gm, w.prRequest(classes.RequestPR, 71, doc), chain.AnswerApprove, "só documentação"); out.State != chain.Approved {
		t.Fatalf("outcome = %+v", out)
	}
}

// The other conditions the engine checks: CI, review, size, deleted files.
func TestTheOtherConditionsOfThePolicyAreCheckedBeforeTheApproval(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	noCI := green(1)
	noCI.CIGreen = false
	w.humanRequired(w.prRequest(classes.RequestPR, 80, noCI), "CI")
	noReview := green(1)
	noReview.ReviewDone = false
	w.humanRequired(w.prRequest(classes.RequestMerge, 81, noReview), "revisão")
	many := green(26)
	w.humanRequired(w.prRequest(classes.RequestPR, 82, many), "26 arquivos")
	del := green(1)
	for i := range 6 {
		del.Files = append(del.Files, chain.PRFile{Path: fmt.Sprintf("old/f%d.go", i), Deleted: true, Removed: 3})
	}
	w.humanRequired(w.prRequest(classes.RequestPR, 83, del), "6 arquivos apagados")
}

// The label and a diff the engine could not read hold for every risk, a rename
// out of a sensitive area is seen, and the commands are matched by segment.
func TestTheLabelTheRenamesAndTheCompoundCommandsAreNotWaysAround(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	mixed := green(1)
	mixed.Labels = []string{"Mestre:Humano"}
	w.humanRequired(w.prRequest(classes.RequestPR, 90, mixed), chain.HumanLabel)
	moved := green(1)
	moved.Files = append(moved.Files, chain.PRFile{Path: "docs/x.yaml", PreviousPath: ".mestre/regras/politica-de-aprovacao.yaml", Added: 1})
	w.humanRequired(w.prRequest(classes.RequestPR, 91, moved), "mestre")
	w.humanRequired(w.prRequest(classes.RequestPR, 92, chain.PRFacts{LabelsRead: true, CIGreen: true, ReviewDone: true}), chain.ReasonIncompleteDiff)
	rules := chain.DefaultRules()
	for _, cmd := range []string{"cd x && git push", "FOO=1 sudo git  push origin", `psql -c "drop table users"`} {
		got := rules.Conditions(chain.Request{Type: classes.RequestClassPermission, Action: &classes.Action{Command: cmd}}, chain.PRFacts{}, false)
		if len(got) == 0 {
			t.Errorf("%q passed the sensitive commands", cmd)
		}
	}
	if got := rules.Conditions(chain.Request{Type: classes.RequestClassPermission, Action: &classes.Action{Command: "npm test"}}, chain.PRFacts{}, false); len(got) != 0 {
		t.Errorf("npm test = %v", got)
	}
}

// A person below the minimum answers nothing, and the fixed types are the Owner's.
func TestAPersonBelowTheMinimumAnswersNothing(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	id := w.shell(w.recruit("Dev"), `psql -c "DROP TABLE users"`)
	for _, ans := range []chain.Answer{chain.AnswerDeny, chain.AnswerDenyAlways, chain.AnswerAskBack} {
		_, err := w.ch.DecideByPerson(w.ctx, chain.PersonDecision{Request: id, User: "ana", Level: "approver", Answer: ans, Instruction: "?"})
		if ref, ok := err.(*chain.Refusal); !ok || ref.Code != chain.CodeHumanRequired {
			t.Fatalf("%s by an Approver = %v, want human_required", ans, err)
		}
	}
	rules := chain.DefaultRules()
	if _, _, min := rules.Need(chain.Request{Type: classes.RequestGuildEntry}); min != classes.LevelOwner {
		t.Fatalf("entrada_na_guilda minimum = %s, want owner", min)
	}
}
