package chain_test

import (
	"testing"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/orchestration/chain"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/quests"
)

// CA-MM-06: what the table master decides is what is at its level: the
// `baixo` of its party and the `medio` of the groups it was given; a request
// that went to the Grand Master, one of another table and one of its own are
// `not_at_level` and the request does not change. (The command
// `mestre approval decide` answers with what the service below answers: see
// handlers_test.go.)
func TestTheTableMasterDecidesTheBaixoAndTheMedioOfItsPartyAndNothingElse(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	dev := w.recruit("Dev")
	_, _, stranger := w.otherTable()
	approved := []int64{
		w.hookReq(t, dev, "npm install lodash", classes.RiskLow),
		w.hookReq(t, dev, "rm -r build", classes.RiskMedium),
		w.hookReq(t, dev, "docker compose up", classes.RiskMedium),
	}
	for _, id := range approved {
		if r := w.request(id); r.Level.Kind != chain.LevelMesa || r.Level.Agent != w.master.ID {
			t.Fatalf("#%d is at %+v, want the table master", id, r.Level)
		}
		if out, err := w.ch.Decide(w.ctx, chain.Decision{Request: id, By: w.master.ID, Answer: chain.AnswerApprove, Reason: "serve à tarefa"}); err != nil || out.State != chain.Approved {
			t.Errorf("#%d: the decision of the table master = %+v, %v; want it accepted", id, out, err)
		}
	}

	global := w.shell(dev, "npm install -g x")
	foreign := w.shell(stranger, "npm install lodash")
	own := w.shell(w.master, "npm install lodash")
	for name, id := range map[string]int64{"npm install -g (the Grand Master's)": global, "a request of another table": foreign, "a request of its own": own} {
		before := w.request(id)
		ref := w.refusal(chain.Decision{Request: id, By: w.master.ID, Answer: chain.AnswerApprove, Reason: "tento"})
		if ref.Code != chain.CodeNotAtLevel {
			t.Errorf("%s: %+v, want not_at_level", name, ref)
		}
		if after := w.request(id); after.State != chain.Open || after.Level != before.Level || after.Decision != nil && after.Decision.By != "" {
			t.Errorf("%s: the request changed: %+v", name, after)
		}
	}
}

// hookReq is a request for a command, checking the risk the engine gave it.
func (w *world) hookReq(t *testing.T, c characters.Character, command string, risk classes.Risk) int64 {
	t.Helper()
	id := w.shell(c, command)
	if got := w.request(id).Risk.Risk; got != risk {
		t.Fatalf("%q has risk %s, want %s", command, got, risk)
	}
	return id
}

// CA-MM-07: a mission the table master designed, with a point of approval:
// when the step arrives, the request is born at the Grand Master, and the
// decision of the table master in it is `not_at_level`.
func TestTheApprovalPointOfAMissionOfTheTableMasterIsBornAtTheGrandMasterWhoseDecisionOnlyCounts(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	v, err := w.orc.Quests.Create(w.ctx, quests.CreateRequest{Workspace: w.slug, Table: w.table.ID, Designer: w.master.ID,
		Data: []byte("name: Com portão\nsteps:\n  - {id: a, sheet: Dev Claude, instructions: \"Faça\", approval: before}\n")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.orc.Quests.Run(w.ctx, v.ID, ""); err != nil {
		t.Fatal(err)
	}
	if err := w.orc.Quests.Sweep(w.ctx); err != nil {
		t.Fatal(err)
	}
	var point chain.Request
	open, err := w.ch.Store().OpenOfTable(w.ctx, w.slug, w.table.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range open {
		if r.Type == classes.RequestMissionPoint {
			point = r
		}
	}
	if point.ID == 0 || point.Level.Kind != chain.LevelGrandMaster || point.Level.Agent != w.gm.ID {
		t.Fatalf("the point of the mission = %+v, want it born at the Grand Master", point)
	}
	ref := w.refusal(chain.Decision{Request: point.ID, By: w.master.ID, Answer: chain.AnswerApprove, Reason: "eu desenhei"})
	if ref.Code != chain.CodeNotAtLevel {
		t.Errorf("the table master deciding its point = %+v, want not_at_level", ref)
	}
	if got := w.request(point.ID); got.State != chain.Open {
		t.Errorf("the point after the refusal is %s, want open", got.State)
	}
	if out := w.decide(w.gm, point.ID, chain.AnswerApprove, "dentro da política"); out.State != chain.Approved {
		t.Errorf("the Grand Master's decision = %+v", out)
	}
}
