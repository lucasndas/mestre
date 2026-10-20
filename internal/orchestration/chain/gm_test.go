package chain_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/orchestration/chain"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/inbox"
	"github.com/lucasndas/mestre/internal/orchestration/tablerules"
)

// CA-GM-07: a policy that does not let the Grand Master approve a merge alone
// (the merge touches a sensitive area, lacks the review or the CI, or a quorum
// rule covers it): his approval is human_required and nothing is approved;
// taking it to the people (`approval ask`, the answer climb, or the question
// the quorum rule already put) puts the question to the users.
func TestAMergeThatThePolicyDoesNotLetTheGrandMasterApproveAloneIsHumanRequiredAndAskingTakesItToThePeople(t *testing.T) {
	t.Parallel()
	sensitive := green(2)
	sensitive.Files = append(sensitive.Files, chain.PRFile{Path: "src/billing/charge.go", Added: 5})
	noReview := green(2)
	noReview.ReviewDone = false
	noCI := green(2)
	noCI.CIGreen = false
	for _, tc := range []struct {
		name   string
		facts  chain.PRFacts
		quorum bool
		why    string
	}{
		{"sensitive area", sensitive, false, "pagamentos"},
		{"missing review", noReview, false, "revisão"},
		{"missing CI", noCI, false, "CI"},
		{"quorum rule", green(2), true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newWorld(t)
			if tc.quorum {
				rules := chain.DefaultRules()
				rules.QuorumRules = []chain.QuorumRule{{Type: classes.RequestMerge, Approvals: 2}}
				w.policy.setRules(rules)
			}
			id := w.prRequest(classes.RequestMerge, 60, tc.facts)

			if tc.quorum {
				// born at the people: the Grand Master's approval is refused
				ref := w.refusal(chain.Decision{Request: id, By: w.gm.ID, Answer: chain.AnswerApprove, Reason: "parece certo"})
				if ref.Code != chain.CodeHumanRequired {
					t.Fatalf("approval = %+v, want human_required", ref)
				}
			} else {
				w.humanRequired(id, tc.why)
			}
			if r := w.request(id); r.State != chain.Open || r.Decision != nil {
				t.Fatalf("after the refusal the request is %s with decision %+v; nothing may be approved", r.State, r.Decision)
			}
			if !tc.quorum {
				w.decide(w.gm, id, chain.AnswerClimb, "a política não me deixa aprovar")
			}
			if w.level(id) != chain.LevelPeople {
				t.Fatalf("after asking, the request is at %+v; want the people", w.request(id).Level)
			}
			asked := w.users.askedFor(id)
			if len(asked) == 0 {
				t.Fatal("the users were not asked")
			}
			if tc.quorum && asked[0].Approvals != 2 {
				t.Errorf("question = %+v; want the 2 approvals of the quorum rule", asked)
			}
			if r := w.request(id); r.State != chain.Open || r.Decision != nil {
				t.Errorf("after asking the request is %s with decision %+v; nothing may be approved", r.State, r.Decision)
			}
		})
	}
}

// CA-GM-08: an alto request of a character, with no delegation decree, is not
// the Grand Master's (he gets one informative event, and his decision is
// human_required); a medio of the table master with everything green in the
// policy is born with him, and his decision is accepted and recorded with the
// level and the risk; a medio of a character that passes the conditions of the
// table is the table master's, and a decision of the Grand Master is not_at_level.
func TestWhatTheGrandMasterDecidesAndWhatItIsNot(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	dev := w.recruit("Dev")

	high := w.shell(dev, `psql -c "DROP TABLE users"`)
	if w.level(high) != chain.LevelPeople {
		t.Fatalf("alto without delegation is at %+v", w.request(high).Level)
	}
	informed := 0
	for _, e := range w.itemsOf(w.gm, inbox.KindSystem) {
		if e.Refs.Approval == high {
			informed++
		}
	}
	if informed != 1 {
		t.Errorf("the Grand Master got %d informative events, want 1", informed)
	}
	if ref := w.refusal(chain.Decision{Request: high, By: w.gm.ID, Answer: chain.AnswerApprove, Reason: "ok"}); ref.Code != chain.CodeHumanRequired {
		t.Errorf("the Grand Master deciding an alto = %+v, want human_required", ref)
	}
	if r := w.request(high); r.State != chain.Open {
		t.Errorf("the alto request is %s after his decision", r.State)
	}

	own := w.shell(w.master, "curl https://exemplo.io")
	if r := w.request(own); r.Level.Kind != chain.LevelGrandMaster || r.Risk.Risk != classes.RiskMedium {
		t.Fatalf("a medio of the table master is %s at %+v, want the Grand Master", r.Risk.Risk, r.Level)
	}
	if out := w.decide(w.gm, own, chain.AnswerApprove, "domínio conhecido do projeto"); out.State != chain.Approved {
		t.Fatalf("outcome = %+v", out)
	}
	audit, err := w.ch.Audit(w.ctx, own)
	if err != nil {
		t.Fatal(err)
	}
	last := audit[len(audit)-1]
	if last.Outcome != "accepted" || last.Approver != w.gm.ID || last.Level != chain.LevelGrandMaster || last.Risk != string(classes.RiskMedium) || last.Reason == "" {
		t.Errorf("audit = %+v, want the accepted decision of the Grand Master with level and risk", last)
	}

	w.cls.domains[dev.ID] = []string{"pypi.org"}
	passes := w.shell(dev, "curl https://pypi.org/x")
	if w.level(passes) != chain.LevelMesa {
		t.Fatalf("a medio that passes the conditions of the table is at %+v, want the table master", w.request(passes).Level)
	}
	if ref := w.refusal(chain.Decision{Request: passes, By: w.gm.ID, Answer: chain.AnswerApprove, Reason: "ok"}); ref.Code != chain.CodeNotAtLevel {
		t.Errorf("the Grand Master deciding it = %+v, want not_at_level", ref)
	}
}

// CA-GM-09: the summary of the decisions of the tables has, in the inbox of the
// Grand Master, a temporary rule of the table; he revokes the rule and pauses
// the autonomy of the table with `approval autonomy pause`: the rule is
// revoked, the table is paused and the next medio of the table is born with him.
func TestTheGrandMasterRevokesARuleOfTheSummaryAndPausesTheAutonomyOfTheTable(t *testing.T) {
	t.Parallel()
	w := newSup(t)
	dev1, dev2 := w.recruit("Dev1"), w.recruit("Dev2")
	for _, d := range []characters.Character{dev1, dev2} {
		w.cls.domains[d.ID] = []string{"registry.npmjs.org", "pypi.org"}
	}
	first := w.shell(dev1, "curl https://pypi.org/x")
	w.approvePattern(first, "curl *", 2*time.Hour)
	rule := w.rules()[0]
	w.clk.Advance(30 * time.Minute)
	w.tick()
	sums := w.summaries()
	if len(sums) != 1 || !strings.Contains(sums[0].Text, "Mesa "+w.table.ID) || !strings.Contains(sums[0].Text, fmt.Sprintf("#%d", first)) {
		t.Fatalf("the summary in the inbox of the Grand Master: %d, %v", len(sums), sums)
	}

	gm := w.get(w.gm.ID)
	if err := w.orc.TableRules.Revoke(w.ctx, rule.ID, tablerules.Revoker{Character: &gm}); err != nil {
		t.Fatal(err)
	}
	resp := w.fwd.Forward(w.ctx, &localchan.Call{Request: localchan.Request{Cmd: "approval autonomy", Args: localchan.TextArgs("pause"), Flags: map[string]any{"table": w.table.ID, "reason": "revisar"}},
		Caller: localchan.Caller{Identified: true, AgentID: gm.ID}})
	if !resp.OK {
		t.Fatalf("approval autonomy pause: %+v", resp.Error)
	}
	if got := w.rules()[0]; got.ID != rule.ID || got.State != tablerules.Revoked {
		t.Errorf("the rule is %+v; want it revoked", got)
	}
	if paused, _ := w.supervisor.AutonomyPaused(w.ctx, w.slug, w.table.ID); !paused {
		t.Error("the table is not paused")
	}
	if lvl := w.level(w.shell(dev2, "pip install y")); lvl != chain.LevelGrandMaster {
		t.Errorf("the next medio of the table was born at %s, want the Grand Master", lvl)
	}
}

// repoSettings opens a request of a change of the repository settings, born at
// the Grão-Mestre when a delegation covers it.
func (w *world) repoSettings(about string) int64 {
	w.t.Helper()
	id, err := w.ch.Request(w.ctx, chain.OpenRequest{Type: classes.RequestRepoSettings, Workspace: w.slug, About: about,
		Target: json.RawMessage(`{"kind":"ruleset","repo":"lucas/Meu_App","branch":"main"}`)})
	if err != nil {
		w.t.Fatal(err)
	}
	return id
}

// CA-APROV-POL-06: a change of the repository settings is the Owner's: without a
// delegation decree the approval of the Grand Master is human_required; with one
// that covers it the approval is accepted, needs no PIN, and the use of the
// decree is registered.
func TestTheGrandMasterApprovesRepositorySettingsOnlyWithADelegationOfTheOwner(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	id := w.repoSettings("regra da branch main")
	if w.level(id) != chain.LevelPeople {
		t.Fatalf("without a delegation the request is at %+v, want the people", w.request(id).Level)
	}
	if ref := w.refusal(chain.Decision{Request: id, By: w.gm.ID, Answer: chain.AnswerApprove, Reason: "ok"}); ref.Code != chain.CodeHumanRequired {
		t.Fatalf("approval without a delegation = %+v, want human_required", ref)
	}
	w.deleg.set(true)
	covered := w.repoSettings("outra regra")
	if w.level(covered) != chain.LevelGrandMaster {
		t.Fatalf("with a delegation the request is at %+v, want the Grand Master", w.request(covered).Level)
	}
	if out := w.decide(w.gm, covered, chain.AnswerApprove, "dentro da delegação"); out.State != chain.Approved {
		t.Fatalf("outcome = %+v, want approved", out)
	}
	if got := w.deleg.usedBy(); len(got) != 1 || got[0] != covered {
		t.Fatalf("uses of the delegation = %v, want the request %d", got, covered)
	}
}

// workflowsRequest opens a request of sending workflows for a table.
func (w *world) workflowsRequest(table string) int64 {
	w.t.Helper()
	id, err := w.ch.Request(w.ctx, chain.OpenRequest{Type: classes.RequestWorkflows, Workspace: w.slug, Table: table, About: "workflows da mesa " + table,
		Target: json.RawMessage(`{"repo":"lucas/Meu_App","branch":"mestre/x","head":"abc","files":[".github/workflows/ci.yml"]}`)})
	if err != nil {
		w.t.Fatal(err)
	}
	return id
}

// CA-APROV-POL-14: with a delegation of the Owner for envio_workflows in the
// table 42-login, the Grand Master's decision on a request of that table is
// accepted; on a request of another table it is human_required; and a quorum rule
// for the type sends the request to the people even inside the delegation.
func TestADelegationForWorkflowsCoversOnlyItsTableAndAQuorumRuleStillSendsItToThePeople(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.deleg.set(true)
	w.deleg.mu.Lock()
	w.deleg.onlyTable = "42-login"
	w.deleg.mu.Unlock()
	inside := w.workflowsRequest("42-login")
	if w.level(inside) != chain.LevelGrandMaster {
		t.Fatalf("the request of the table is at %+v, want the Grand Master", w.request(inside).Level)
	}
	if out := w.decide(w.gm, inside, chain.AnswerApprove, "dentro da delegação"); out.State != chain.Approved {
		t.Fatalf("outcome = %+v, want approved", out)
	}
	outside := w.workflowsRequest("43-busca")
	if w.level(outside) != chain.LevelPeople {
		t.Fatalf("the request of another table is at %+v, want the people", w.request(outside).Level)
	}
	if ref := w.refusal(chain.Decision{Request: outside, By: w.gm.ID, Answer: chain.AnswerApprove, Reason: "ok"}); ref.Code != chain.CodeHumanRequired {
		t.Fatalf("approval outside the delegation = %+v, want human_required", ref)
	}
	rules := chain.DefaultRules()
	rules.QuorumRules = []chain.QuorumRule{{Type: classes.RequestWorkflows, Approvals: 1}}
	w.policy.setRules(rules)
	quorum := w.workflowsRequest("42-login")
	if w.level(quorum) != chain.LevelPeople {
		t.Fatalf("with the quorum rule the request is at %+v, want the people", w.request(quorum).Level)
	}
}

// CA-APROV-POL-05: with a decree of policy active that loosens "migracoes", a PR
// on **/migrations/** with everything green is the Grand Master's to approve alone;
// without it, human_required. A decree never opens the areas of the engine and the
// secrets, nor a type of risk alto.
func TestADecreeOfPolicyThatLoosensMigrationsLetsTheGrandMasterApproveAMigrationPR(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	f := green(2)
	f.Files = append(f.Files, chain.PRFile{Path: "db/migrations/0003.sql", Added: 12})
	before := w.prRequest(classes.RequestPR, 70, f)
	w.humanRequired(before, "migracoes")

	w.pdec.set([]string{"migracoes"}, nil)
	id := w.prRequest(classes.RequestPR, 71, f)
	if out := w.decide(w.gm, id, chain.AnswerApprove, "o decreto libera migrações"); out.State != chain.Approved {
		t.Fatalf("outcome = %+v, want approved", out)
	}
	// the decree is read when the Grand Master decides: the one opened before is his now too
	if out := w.decide(w.gm, before, chain.AnswerApprove, "o decreto libera migrações"); out.State != chain.Approved {
		t.Fatalf("outcome of the request opened before = %+v, want approved", out)
	}

	// what no decree loosens
	w.pdec.set([]string{"mestre", "permissoes_e_segredos", "destrutiva", string(classes.RequestDestructive), string(classes.RequestRepoSettings)}, nil)
	sensitive := green(1)
	sensitive.Files = append(sensitive.Files, chain.PRFile{Path: classes.DefaultMestrePaths[0], Added: 3})
	m := w.prRequest(classes.RequestPR, 72, sensitive)
	w.humanRequired(m)
}

// Part of CA-APROV-POL-13 (the proposal and the reach of the decree are in
// internal/decrees): a decree of policy "me pergunte tudo" sends to the people a
// green PR the Grand Master would approve alone, and it holds for the types alto
// too: a request of a type that a delegation of the Owner covers is asked of the
// people while it stands. (The proposal that loosens an alto type is refused before
// the confirmation: internal/decrees.)
func TestADecreeOfPolicyThatTightensEverythingSendsToThePeopleEvenWhatADelegationCovers(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.deleg.set(true)
	w.pdec.set(nil, []string{"*"})
	pr := w.prRequest(classes.RequestPR, 80, green(2))
	if w.level(pr) != chain.LevelPeople {
		t.Fatalf("a green PR is at %+v, want the people", w.request(pr).Level)
	}
	alto := w.workflowsRequest("42-login")
	if w.level(alto) != chain.LevelPeople {
		t.Fatalf("an alto covered by a delegation is at %+v, want the people", w.request(alto).Level)
	}
	// only one type tightened: the others keep their rules
	w.pdec.set(nil, []string{string(classes.RequestWorkflows)})
	other := w.prRequest(classes.RequestPR, 81, green(2))
	if w.level(other) != chain.LevelGrandMaster {
		t.Fatalf("a PR with only workflows tightened is at %+v, want the Grand Master", w.request(other).Level)
	}
	covered := w.workflowsRequest("42-login")
	if w.level(covered) != chain.LevelPeople {
		t.Fatalf("workflows tightened: %+v, want the people", w.request(covered).Level)
	}
}
