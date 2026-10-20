package chain_test

import (
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
// (here, the merge touches a sensitive area of the policy): his approval is
// human_required and nothing is approved; taking it to the people (`approval
// ask`, the answer climb) puts the question to the users.
func TestAMergeThatThePolicyDoesNotLetTheGrandMasterApproveAloneIsHumanRequiredAndAskingTakesItToThePeople(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	f := green(2)
	f.Files = append(f.Files, chain.PRFile{Path: "src/billing/charge.go", Added: 5})
	id := w.prRequest(classes.RequestMerge, 60, f)

	w.humanRequired(id, "pagamentos")
	if r := w.request(id); r.State != chain.Open || r.Decision != nil {
		t.Fatalf("after the refusal the request is %s with decision %+v; nothing may be approved", r.State, r.Decision)
	}
	w.decide(w.gm, id, chain.AnswerClimb, "mexe em pagamentos")
	if w.level(id) != chain.LevelPeople {
		t.Fatalf("after asking, the request is at %+v; want the people", w.request(id).Level)
	}
	if asked := w.users.askedFor(id); len(asked) == 0 {
		t.Fatal("the users were not asked")
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
