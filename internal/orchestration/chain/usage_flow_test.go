package chain_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/orchestration/chain"
	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/orchestration/inbox"
	"github.com/mestre-ia/mestre/internal/orchestration/usage"
	"github.com/mestre-ia/mestre/internal/providers"
	"github.com/mestre-ia/mestre/internal/store"
	"github.com/mestre-ia/mestre/internal/testkit/guildtpl"
)

// usageCeilingYAML is the cost ceiling of the tests of the extra cost: a table
// may spend US$ 12 at most.
const usageCeilingYAML = `version: 1
cost:
  usd:
    per_table: 12
    per_day: 30
`

type fixedCeilings struct{ c *classes.Ceiling }

func (f fixedCeilings) Ceiling(context.Context, string) (*classes.Ceiling, error) { return f.c, nil }

// chainExtras opens the custo_extra request of a limit in the chain, as the
// Citadel wires the usage service to it.
type chainExtras struct{ ch *chain.Service }

func (e chainExtras) OpenExtra(ctx context.Context, r usage.ExtraRequest) (int64, error) {
	return e.ch.Request(ctx, chain.OpenRequest{Type: classes.RequestExtraCost, Workspace: r.Workspace, Table: r.Table, AboutMsg: r.About, Risk: r.Risk, Target: r.Target})
}

type noDirectory struct{}

func (noDirectory) Character(context.Context, string) (usage.Subject, bool, error) {
	return usage.Subject{}, false, nil
}
func (noDirectory) Alternatives(context.Context, usage.Subject) ([]string, error) { return nil, nil }
func (noDirectory) PendingSteps(context.Context, string, string) (int, error)     { return 0, nil }

type noPauser struct{}

func (noPauser) PauseQuest(context.Context, string, string) error         { return nil }
func (noPauser) ResumeQuest(context.Context, string) error                { return nil }
func (noPauser) PauseTable(context.Context, string, string, string) error { return nil }
func (noPauser) ResumeTable(context.Context, string, string) error        { return nil }
func (noPauser) PauseWorkspace(context.Context, string) (bool, error)     { return true, nil }
func (noPauser) ResumeWorkspace(context.Context, string) error            { return nil }
func (noPauser) Workspaces(context.Context) ([]string, error)             { return nil, nil }

// extraCost makes the table of the world reach its limit through the real usage
// service, whose cost ceiling is usageCeilingYAML and whose extra goes to the
// real chain of the world; it returns the request that service opened.
func (w *world) extraCost(limit, spent float64) chain.Request {
	w.t.Helper()
	db, err := guildtpl.Open(w.ctx, filepath.Join(w.t.TempDir(), "usage.db"), store.Options{Synchronous: "OFF", Clock: w.clk})
	if err != nil {
		w.t.Fatal(err)
	}
	w.t.Cleanup(func() { _ = db.Close() })
	ceiling, err := classes.ParseCeiling([]byte(usageCeilingYAML))
	if err != nil {
		w.t.Fatal(err)
	}
	svc, err := usage.NewService(usage.Options{Store: &usage.Store{DB: db}, Clock: w.clk, Directory: noDirectory{}, Pauser: noPauser{}, Ceilings: fixedCeilings{ceiling}, Extras: chainExtras{w.ch}})
	if err != nil {
		w.t.Fatal(err)
	}
	if err := svc.SetLimit(w.ctx, classes.LevelAdmin, w.slug, usage.Limit{Scope: usage.ScopeTable, Key: usage.TableKey(w.slug, w.table.ID), Unit: classes.UnitUSD, Value: limit}); err != nil {
		w.t.Fatal(err)
	}
	if err := svc.Record(w.ctx, usage.Record{Character: w.master.ID, Step: "q1/dev", Quest: "q1", Workspace: w.slug, Table: w.table.ID, Profile: "pessoal",
		Provider: providers.ClaudeCode, At: w.clk.Now(), Source: "statusline", Origin: usage.OriginStep, CostUSD: &spent,
		Tokens: usage.Tokens{Input: 1000, Output: 200, Known: true}}); err != nil {
		w.t.Fatal(err)
	}
	var found []chain.Request
	for id := int64(1); id < 10; id++ {
		if r, err := w.ch.Get(w.ctx, id); err == nil && r.Type == classes.RequestExtraCost {
			found = append(found, r)
		}
	}
	if len(found) != 1 {
		w.t.Fatalf("the usage service opened %d custo_extra requests in the chain, want 1", len(found))
	}
	return found[0]
}

// CA-USO-07: the custo_extra request that would pass the ceiling is opened by the
// usage service, the Grand Master tries to approve it (by the service and by the
// command) and is refused, the request goes to the users and only an
// Administrator or above approves it. A request within the ceiling is the
// Grand Master's.
func TestAnExtraCostAboveTheCeilingFromTheUsageServiceIsRefusedToTheGrandMasterAndDecidedByAnAdministrator(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	// the limit is 10 and 50% more is 15, above the ceiling of 12
	r := w.extraCost(10, 10)
	if r.Risk.Risk != classes.RiskHigh || !r.Risk.NoDelegation || r.Level.Kind != chain.LevelPeople || r.State != chain.Open || r.Table != w.table.ID {
		t.Fatalf("the request = risk %+v at %+v (%s), want alto without delegation at the people", r.Risk, r.Level, r.State)
	}
	var target map[string]any
	if err := json.Unmarshal(r.Target, &target); err != nil || target["extra"] != float64(5) || target["limit"] != float64(10) {
		t.Fatalf("the target = %s (%v), want the extra of 5 over the limit of 10", r.Target, err)
	}
	if ref := w.refusal(chain.Decision{Request: r.ID, By: w.gm.ID, Answer: chain.AnswerApprove, Reason: "parece certo"}); ref.Code != chain.CodeHumanRequired {
		t.Errorf("the Grand Master approving = %+v, want human_required", ref)
	}
	if got := w.cli(w.gm, "approval decide", map[string]any{"reason": "parece certo"}, itoa(r.ID), "approve"); got.OK || got.Code != chain.CodeHumanRequired {
		t.Errorf("the Grand Master approving by the command = %+v, want human_required", got)
	}
	if got := w.request(r.ID); got.State != chain.Open || got.Level.Kind != chain.LevelPeople {
		t.Fatalf("after the refusals the request is %s at %+v, want open at the people", got.State, got.Level)
	}
	// the users decide it: an Approver is below the minimum, an Administrator is not
	if _, err := w.ch.DecideByPerson(w.ctx, chain.PersonDecision{Request: r.ID, User: "u2", Level: "approver", Answer: chain.AnswerApprove}); err == nil {
		t.Fatal("an Approver approved an extra cost above the ceiling")
	} else if ref, ok := err.(*chain.Refusal); !ok || ref.Code != chain.CodeHumanRequired {
		t.Errorf("an Approver approving = %v, want human_required", err)
	}
	if got := w.request(r.ID); got.State != chain.Open {
		t.Fatalf("an Approver changed the request to %s", got.State)
	}
	out, err := w.ch.DecideByPerson(w.ctx, chain.PersonDecision{Request: r.ID, User: "u1", Level: "administrator", Answer: chain.AnswerApprove, Reason: "vale"})
	if err != nil || out.State != chain.Approved {
		t.Fatalf("an Administrator approving = %+v, %v, want approved", out, err)
	}

	// within the ceiling (limit 6, extra 3, 9 < 12) the request is the Grand Master's
	w2 := newWorld(t)
	in := w2.extraCost(6, 6)
	if in.Risk.Risk != classes.RiskMedium || in.Risk.NoDelegation || in.Level.Kind != chain.LevelGrandMaster {
		t.Fatalf("the request within the ceiling = risk %+v at %+v, want medio at the Grand Master", in.Risk, in.Level)
	}
	if out := w2.decide(w2.gm, in.ID, chain.AnswerApprove, "dentro do teto"); out.State != chain.Approved {
		t.Errorf("the Grand Master approving within the ceiling = %+v, want approved", out)
	}
}

// CA-USO-05: a character that waits for the quota until 14:00 (fake clock)
// leaves blocked when it passes 14:00 and receives the queue that waited, not
// before. The characters, the inbox and the usage service are the real ones.
func TestAfterTheQuotaIsBackTheCharacterLeavesBlockedAndReceivesTheQueue(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	dev := w.recruit("Dev")
	w.state(dev, characters.Idle)
	until := w.clk.Now().Add(2 * time.Hour)
	w.orc.Usage.OnEvent(w.ctx, providers.Event{AgentID: dev.ID, Event: providers.EventUsage, Provider: providers.ClaudeCode, ReceivedAt: w.clk.Now(),
		Usage: &providers.Usage{LimitsReached: true, LimitsResetAt: until}})
	if got := w.get(dev.ID); got.State != characters.Blocked || got.Reason != characters.ReasonQuota {
		t.Fatalf("the character is %s (%s), want blocked for quota", got.State, got.Reason)
	}
	const task = "rode os testes depois da cota"
	if _, err := w.orc.Inbox.Enqueue(w.ctx, inbox.NewItem{Workspace: w.slug, Target: dev.ID, Kind: inbox.KindAgent, Sender: "Mestre", Text: task}); err != nil {
		t.Fatal(err)
	}
	delivered := len(w.machines.Delivered(w.get(dev.ID)))
	w.clk.Advance(2*time.Hour - time.Second)
	if err := w.orc.Usage.Sweep(w.ctx); err != nil {
		t.Fatal(err)
	}
	if got := w.get(dev.ID); got.State != characters.Blocked || len(w.machines.Delivered(got)) != delivered {
		t.Fatalf("before the quota is back: %s, %d items typed (had %d); want blocked and nothing typed", got.State, len(w.machines.Delivered(got)), delivered)
	}
	w.clk.Advance(2 * time.Second)
	if err := w.orc.Usage.Sweep(w.ctx); err != nil {
		t.Fatal(err)
	}
	got := w.get(dev.ID)
	if got.State == characters.Blocked {
		t.Fatalf("the character is still blocked after the quota came back")
	}
	typed := w.machines.Delivered(got)
	if len(typed) != delivered+1 || !strings.Contains(typed[len(typed)-1], task) {
		t.Errorf("typed after the quota = %q (had %d), want the queued item %q delivered", typed, delivered, task)
	}
}
