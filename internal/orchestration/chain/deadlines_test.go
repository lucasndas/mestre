package chain_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/orchestration/chain"
)

// The time of the people depends on the type of the request: 2 h for a
// permission of a class by default (docs/aprovacoes/fluxo.md#expiração), and
// the policy sets one for each type.
func TestThePeopleHaveATimeForEachTypeOfRequest(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		set  func(*chain.Settings)
		want time.Duration
	}{
		{"default", func(*chain.Settings) {}, 2 * time.Hour},
		{"of the policy", func(s *chain.Settings) {
			s.PeopleDeadlines = map[classes.RequestType]time.Duration{classes.RequestClassPermission: 45 * time.Minute}
		}, 45 * time.Minute},
		{"a type without one", func(s *chain.Settings) {
			s.PeopleDeadlines = map[classes.RequestType]time.Duration{}
			s.PeopleDeadline = 3 * time.Hour
		}, 3 * time.Hour},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newWorld(t)
			s := chain.DefaultSettings()
			tc.set(&s)
			w.policy.set(s)
			id := w.shell(w.recruit("Dev"), "npm install lodash")
			w.clk.Advance(12 * time.Minute) // the agent levels: 2 + 10 min
			if w.level(id) != chain.LevelPeople {
				t.Fatalf("the request is at %+v, want the people", w.request(id).Level)
			}
			w.clk.Advance(tc.want - time.Second)
			if w.request(id).State != chain.Open {
				t.Fatal("it expired before the time of the people")
			}
			w.clk.Advance(time.Second)
			if got := w.request(id).State; got != chain.Expired {
				t.Fatalf("at the end of the time the request is %s, want expired", got)
			}
		})
	}
}

// A decision whose effect fails gives the request back with the whole time of
// its level, not what was left.
// CA-CADEIA-19
func TestAnEffectThatFailsRestartsTheTimeOfTheLevel(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	id, err := w.ch.Open(w.ctx, w.hookRequest(w.recruit("Dev"), "npm install lodash"))
	if err != nil {
		t.Fatal(err)
	}
	w.clk.Advance(100 * time.Second)
	w.grants.fail = errors.New("the vault is busy")
	_ = w.refusal(chain.Decision{Request: id, By: w.master.ID, Answer: chain.AnswerApprove, Reason: "ok"})
	if got, want := w.request(id).Deadline, w.clk.Now().Add(2*time.Minute); !got.Equal(want) {
		t.Errorf("deadline = %v, want %v", got, want)
	}
}

// CA-CADEIA-20
func TestTheMestreDaMesaAndTheGrandMasterMayApproveForATimeAMedioRequest(t *testing.T) {
	t.Parallel()
	medio := chain.Request{Type: classes.RequestClassPermission}
	medio.Risk.Risk = classes.RiskMedium
	for level, want := range map[chain.LevelKind]bool{chain.LevelMesa: true, chain.LevelGrandMaster: true, chain.LevelAgent: false} {
		got := slices.Contains(chain.AnswersFor(level, medio), chain.AnswerApproveFor)
		if got != want {
			t.Errorf("%s: approve_for listed = %v, want %v", level, got, want)
		}
	}
}

// CA-RTM-10
func TestTheDurationsOfThePolicyAcceptDays(t *testing.T) {
	t.Parallel()
	s, err := chain.ParseSection([]byte("regra_temporaria_max: 7d\nresumo_mesas: 30d\n"), 0)
	if err != nil || s.TempRuleMax != 7*24*time.Hour || s.SummaryEvery != 30*24*time.Hour {
		t.Fatalf("section = %+v, %v", s, err)
	}
	if _, err := chain.ParseSection([]byte("regra_temporaria_max: 8d\n"), 0); err == nil {
		t.Error("8d is above the 7 days of a temporary rule")
	}
	if _, err := chain.ParseSection([]byte("resumo_mesas: xd\n"), 0); err == nil {
		t.Error("xd is not a duration")
	}
}

// The wait of the requester for the approval has the time limit the table of
// waits promises: the sum of the times of the levels of the chain, the one of
// the people being that of the type (docs/orquestracao/esperas-e-caixa-de-entrada.md).
// CA-ESP-13
func TestTheWaitOfTheRequesterForAnApprovalHasTheSumOfTheTimesOfTheLevels(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	dev := w.recruit("Dev")
	w.shell(dev, "npm install lodash")
	ws, err := w.orc.Waits.Store().ActiveOf(w.ctx, dev.ID)
	if err != nil || len(ws) != 1 {
		t.Fatalf("waits of the requester = %+v, %v", ws, err)
	}
	want := 2*time.Minute + 10*time.Minute + 2*time.Hour + time.Minute // and a minute of grace after the last level
	if got := ws[0].TimeoutAt.Sub(ws[0].CreatedAt); got != want {
		t.Errorf("the time limit of the wait is %v, want %v", got, want)
	}
}
