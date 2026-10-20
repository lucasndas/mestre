//go:build full

package chain_test

import (
	"strings"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/orchestration/chain"
	"github.com/lucasndas/mestre/internal/orchestration/inbox"
)

// Part of CA-CADEIA-03
func TestTheMedioRequestsGoToTheLevelThatCanDecideThemAndTheHighOnesToThePeople(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	dev := w.recruit("Dev")
	// the class lists the registry; the ceiling adds pypi.org
	w.cls.domains[dev.ID] = []string{"registry.npmjs.org", "pypi.org"}
	listed := w.shell(dev, "curl https://pypi.org/x")
	unlisted := w.shell(dev, "curl https://exemplo.io")
	high := w.shell(dev, `psql -c "DROP TABLE users"`)

	if r := w.request(listed); r.Risk.Risk != classes.RiskMedium || r.Level.Kind != chain.LevelMesa {
		t.Errorf("curl pypi.org: %s at %+v, want medio at the table master", r.Risk.Risk, r.Level)
	}
	r := w.request(unlisted)
	if r.Risk.Risk != classes.RiskMedium || r.Level.Kind != chain.LevelGrandMaster || !contains(strings.Join(r.Why, " "), "rede", "exemplo.io") {
		t.Errorf("curl exemplo.io: %s at %+v why %q, want medio at the Grand Master citing the network condition", r.Risk.Risk, r.Level, r.Why)
	}
	if r := w.request(high); r.Risk.Risk != classes.RiskHigh || r.Level.Kind != chain.LevelPeople {
		t.Errorf("DROP TABLE: %s at %+v, want alto at the people", r.Risk.Risk, r.Level)
	}
	if items := w.approvalItems(w.master); len(items) != 1 || !strings.Contains(items[0].Text, "pypi.org") {
		t.Errorf("the table master has %d approval items, want only the pypi.org one", len(items))
	}
	if items := w.approvalItems(w.gm); len(items) != 1 || !strings.Contains(items[0].Text, "exemplo.io") {
		t.Errorf("the Grand Master has %d approval items, want only the exemplo.io one", len(items))
	}
	events := w.itemsOf(w.gm, inbox.KindSystem)
	var informed int
	for _, e := range events {
		if e.Refs.Approval == high {
			informed++
		}
	}
	if informed != 1 {
		t.Errorf("the Grand Master got %d informative events of the alto request, want 1", informed)
	}
	if len(w.users.askedFor(high)) != 1 {
		t.Errorf("the people were asked %d times about the alto request", len(w.users.askedFor(high)))
	}
}

// Part of CA-CADEIA-09: the approval for a time of the exact action is a rule
// of baixo or medio inside the maximum, and the Grand Master, who decides the
// medio of any group, makes the rule of the pattern its request needs.
func TestApprovingForATimeMakesARuleOfTheExactActionAndTheGrandMasterMakesTheMedioOnes(t *testing.T) {
	t.Parallel()
	w := newRTM(t)
	dev1, dev2 := w.recruit("Dev1"), w.recruit("Dev2")
	id := w.shell(dev1, "npm install lodash")
	out, err := w.ch.Decide(w.ctx, chain.Decision{Request: id, By: w.master.ID, Answer: chain.AnswerApproveFor, For: time.Hour, Reason: "por uma hora"})
	if err != nil || out.Applied != "temporary_rule" {
		t.Fatalf("approve for = %+v, %v", out, err)
	}
	if d := w.engine(dev2, "npm install lodash"); d.Verdict != classes.Allow {
		t.Errorf("the same action by another dev = %+v, want the rule", d)
	}
	if d := w.engine(dev2, "npm install other"); d.Verdict != classes.Ask {
		t.Errorf("another action = %+v, want ask: the rule is of the exact action", d)
	}
	if ref := w.refusal(chain.Decision{Request: w.shell(dev1, "npm install z"), By: w.master.ID, Answer: chain.AnswerApproveFor, Reason: "sem tempo"}); ref.Code != chain.CodeBadAnswer {
		t.Errorf("approve for without a time = %+v, want bad_answer", ref)
	}
	// the Grand Master's rule of a medio of any group
	g := w.shell(dev1, "npm install -g x")
	if _, err := w.ch.Decide(w.ctx, chain.Decision{Request: g, By: w.gm.ID, Answer: chain.AnswerApprovePattern, Pattern: "npm install -g *", For: time.Hour, Reason: "global"}); err != nil {
		t.Fatalf("the Grand Master's pattern: %v", err)
	}
	if rs := w.rules(); len(rs) != 2 || rs[1].Level != chain.LevelGrandMaster || rs[1].Risk != classes.RiskMedium {
		t.Errorf("rules = %+v", rs)
	}
}
