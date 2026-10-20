package usage_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/usage"
	"github.com/lucasndas/mestre/internal/providers"
)

// CA-USO-01
func TestSumsStatusLineByTableAndWorkspace(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.character("c1", "Dev", characters.RoleCharacter, providers.ClaudeCode)
	ev := providers.Event{AgentID: "c1", Event: providers.EventUsage, Provider: providers.ClaudeCode, SessionID: "s1", ReceivedAt: w.clk.Now(),
		Usage: &providers.Usage{InputTokens: 1000, OutputTokens: 200, CostUSD: 0.5}}
	w.svc.OnEvent(w.ctx, ev)
	// the statusLine gives totals: the next reading is read as the difference
	ev.Usage = &providers.Usage{InputTokens: 1500, OutputTokens: 300, CostUSD: 0.75}
	w.svc.OnEvent(w.ctx, ev)

	for _, c := range []struct {
		scope usage.Scope
		key   string
	}{{usage.ScopeTable, "loja/t1"}, {usage.ScopeWorkspace, "loja"}, {usage.ScopeQuest, "q1"}} {
		got, err := w.svc.Consumption(w.ctx, c.scope, c.key)
		if err != nil {
			t.Fatal(err)
		}
		if got.CostUSD != 0.75 || got.Tokens != 1800 || !got.HasCost || !got.HasTokens {
			t.Errorf("%s %s = %+v, want US$ 0.75 and 1800 tokens (the totals of the session, once)", c.scope, c.key, got)
		}
	}
	// /clear zeroes the session: a total that went back is a new count
	ev.Usage = &providers.Usage{InputTokens: 100, OutputTokens: 20, CostUSD: 0.1}
	w.svc.OnEvent(w.ctx, ev)
	if got, _ := w.svc.Consumption(w.ctx, usage.ScopeTable, "loja/t1"); got.CostUSD < 0.849 || got.CostUSD > 0.851 {
		t.Errorf("after a count that started again, cost = %v, want 0.85", got.CostUSD)
	}
	if got, _ := w.svc.OfCharacter(w.ctx, "c1"); got.Tokens != 1920 {
		t.Errorf("the character used %d tokens, want 1920", got.Tokens)
	}
}

// CA-USO-02
func TestWarnsOnceAt80Percent(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	if err := w.svc.SetLimit(w.ctx, classes.LevelAdmin, "loja", usage.Limit{Scope: usage.ScopeTable, Key: "loja/t1", Unit: classes.UnitUSD, Value: 10}); err != nil {
		t.Fatal(err)
	}
	if err := w.svc.Record(w.ctx, stepRecord(w, 7)); err != nil {
		t.Fatal(err)
	}
	if n := w.said.alertsWith("Uso em"); n != 0 {
		t.Fatalf("%d warnings at 70%%, want none", n)
	}
	if err := w.svc.Record(w.ctx, stepRecord(w, 1)); err != nil { // 8 of 10
		t.Fatal(err)
	}
	if n := w.said.alertsWith("Uso em 80%"); n != 1 {
		t.Fatalf("%d warnings at 80%%, want 1: %v", n, w.said.alerts)
	}
	if got := w.said.noteTo(characters.ToGrandMaster, "Uso em 80%"); len(got) != 1 {
		t.Errorf("the Grand Master heard %d times, want once", len(got))
	}
	if err := w.svc.Record(w.ctx, stepRecord(w, 1.5)); err != nil { // 9.5 of 10: still below 100%
		t.Fatal(err)
	}
	if n := w.said.alertsWith("Uso em"); n != 1 {
		t.Errorf("a second record below 100%% warned again (%d warnings)", n)
	}
	if got := w.pause.got(); len(got) != 0 {
		t.Errorf("paused before 100%%: %v", got)
	}
}

// CA-USO-03
func TestPausesAt100PercentWithoutCuttingTheTurn(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.character("c1", "Dev", characters.RoleCharacter, providers.ClaudeCode)
	if err := w.svc.SetLimit(w.ctx, classes.LevelAdmin, "loja", usage.Limit{Scope: usage.ScopeTable, Key: "loja/t1", Unit: classes.UnitUSD, Value: 10}); err != nil {
		t.Fatal(err)
	}
	w.svc.OnEvent(w.ctx, providers.Event{AgentID: "c1", Event: providers.EventTurnStart, ReceivedAt: w.clk.Now()})
	w.clk.Advance(time.Minute)
	if err := w.svc.Record(w.ctx, stepRecord(w, 10.5)); err != nil { // 100% in the middle of the turn
		t.Fatal(err)
	}
	if got := w.pause.got(); !slices.Equal(got, []string{"pause table loja/t1"}) {
		t.Fatalf("paused %v, want only the table", got)
	}
	if len(w.chars.blocks) != 0 || len(w.chars.frees) != 0 {
		t.Error("the limit touched the character: a turn is never interrupted")
	}
	reqs := w.extra.all()
	if len(reqs) != 1 {
		t.Fatalf("%d custo_extra requests, want 1", len(reqs))
	}
	if r := reqs[0]; r.Used != 10.5 || r.Limit != 10 || r.Extra != 5 || r.Table != "t1" || r.Scope != usage.ScopeTable {
		t.Errorf("request = %+v, want the use so far, the limit and 50%% more", r)
	}
	// the turn ends normally, and nothing asks again
	w.clk.Advance(time.Minute)
	w.svc.OnEvent(w.ctx, providers.Event{AgentID: "c1", Event: providers.EventTurnEnd, ReceivedAt: w.clk.Now()})
	if got := len(w.extra.all()); got != 1 {
		t.Errorf("%d requests after the turn ended, want 1", got)
	}
	// approved: the limit of the period goes up and the pause is lifted
	if err := w.svc.Decided(w.ctx, usage.Decision{Request: 1, Approved: true}); err != nil {
		t.Fatal(err)
	}
	if got := w.pause.got(); !slices.Equal(got, []string{"pause table loja/t1", "resume table loja/t1"}) {
		t.Errorf("after the approval: %v", got)
	}
	v, err := w.svc.View(w.ctx, usage.Limit{Scope: usage.ScopeTable, Key: "loja/t1", Unit: classes.UnitUSD, Value: 10}, "loja")
	if err != nil {
		t.Fatal(err)
	}
	if v.Effective != 15 {
		t.Errorf("effective limit = %v, want 15", v.Effective)
	}
	// a decision comes once: the same answer again does nothing
	if err := w.svc.Decided(w.ctx, usage.Decision{Request: 1, Approved: true}); err != nil {
		t.Fatal(err)
	}
	if got := w.pause.got(); len(got) != 2 {
		t.Errorf("a repeated decision resumed again: %v", got)
	}
}

func TestDeniedExtraKeepsItPausedAndTellsPeople(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	if err := w.svc.SetLimit(w.ctx, classes.LevelOwner, "loja", usage.Limit{Scope: usage.ScopeQuest, Key: "q1", Unit: classes.UnitUSD, Value: 4}); err != nil {
		t.Fatal(err)
	}
	if err := w.svc.Record(w.ctx, stepRecord(w, 4)); err != nil {
		t.Fatal(err)
	}
	if got := w.pause.got(); !slices.Equal(got, []string{"pause quest q1"}) {
		t.Fatalf("paused %v, want the quest", got)
	}
	if err := w.svc.Decided(w.ctx, usage.Decision{Request: 1, Approved: false, Reason: "expirou"}); err != nil {
		t.Fatal(err)
	}
	if got := w.pause.got(); len(got) != 1 {
		t.Errorf("a denied extra resumed: %v", got)
	}
	if n := w.said.alertsWith("não foi aprovado"); n != 1 {
		t.Errorf("the users heard %d times, want once: %v", n, w.said.alerts)
	}
	if got := w.said.noteTo(characters.ToGrandMaster, "não foi aprovado"); len(got) != 1 {
		t.Errorf("the Grand Master heard %d times, want once", len(got))
	}
}

func TestWorkspaceAndDayLimitsPauseTheWorkspace(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	for _, l := range []usage.Limit{
		{Scope: usage.ScopeWorkspace, Key: "loja", Unit: classes.UnitTokens, Value: 5000},
		{Scope: usage.ScopeDay, Key: "loja", Unit: classes.UnitHours, Value: 1},
	} {
		if err := w.svc.SetLimit(w.ctx, classes.LevelAdmin, "loja", l); err != nil {
			t.Fatal(err)
		}
	}
	r := stepRecord(w, 0.1)
	r.Tokens = usage.Tokens{Input: 5000, Known: true}
	if err := w.svc.Record(w.ctx, r); err != nil {
		t.Fatal(err)
	}
	if got := w.pause.got(); !slices.Equal(got, []string{"pause workspace loja"}) {
		t.Fatalf("workspace limit: %v", got)
	}
	// the day: an hour of active time
	r = stepRecord(w, 0)
	r.CostUSD, r.Tokens, r.Active, r.Turns = nil, usage.Tokens{}, 61*time.Minute, 1
	if err := w.svc.Record(w.ctx, r); err != nil {
		t.Fatal(err)
	}
	if got := w.pause.got(); !slices.Equal(got, []string{"pause workspace loja", "pause workspace loja"}) {
		t.Errorf("day limit: %v", got)
	}
	if n := len(w.extra.all()); n != 2 {
		t.Errorf("%d requests, want one per limit", n)
	}
}

// CA-USO-04
func TestCodexReachedLimitBlocksForQuotaAndOffersAlternatives(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.character("mestre", "Mestre", characters.RoleTableMaster, providers.ClaudeCode)
	sub := w.character("c2", "Revisor", characters.RoleCharacter, providers.Codex)
	w.dir.alts["c2"] = []string{"Revisor Claude", "Revisor (conta trabalho)"}
	back := w.clk.Now().Add(3 * time.Hour)
	ev := providers.Event{AgentID: "c2", Event: providers.EventUsage, Provider: providers.Codex, ReceivedAt: w.clk.Now(),
		Usage: &providers.Usage{InputTokens: 50, OutputTokens: 10, LimitsReached: true, LimitsResetAt: back,
			LimitWindows: []providers.LimitWindow{{Window: "5h", UsedPct: 100, ResetsAt: back}}}}
	w.svc.OnEvent(w.ctx, ev)

	if !slices.Equal(w.chars.blocks, []string{"c2"}) {
		t.Fatalf("blocked %v, want c2 (blocked, reason quota)", w.chars.blocks)
	}
	got := w.said.noteTo(characters.ToTableMaster, "aguardando cota")
	if len(got) != 1 {
		t.Fatalf("the table master heard %d times, want once: %+v", len(got), w.said.notes)
	}
	for _, want := range []string{"Revisor", "codex", "pessoal", "15:00", "Revisor Claude", "Revisor (conta trabalho)"} {
		if !strings.Contains(got[0].Text, want) {
			t.Errorf("the event lacks %q: %s", want, got[0].Text)
		}
	}
	// the same limit read again is not a second event
	w.svc.OnEvent(w.ctx, ev)
	if n := len(w.said.noteTo(characters.ToTableMaster, "aguardando cota")); n != 1 || len(w.chars.blocks) != 1 {
		t.Errorf("a second reading told %d times and blocked %d times", n, len(w.chars.blocks))
	}
	_ = sub

	// if the one without quota is the table master, the Grand Master hears it; if it is the Grand Master, the users
	w.character("m1", "Mestre", characters.RoleTableMaster, providers.Codex)
	w.character("g1", "Grão-Mestre", characters.RoleGrandMaster, providers.Codex)
	for _, id := range []string{"m1", "g1"} {
		e := ev
		e.AgentID = id
		w.svc.OnEvent(w.ctx, e)
	}
	if n := len(w.said.noteTo(characters.ToGrandMaster, "Mestre (codex")); n != 1 {
		t.Errorf("the Grand Master heard %d times about the table master, want once", n)
	}
	if n := w.said.alertsWith("Grão-Mestre (codex"); n != 1 {
		t.Errorf("the users heard %d times about the Grand Master, want once: %v", n, w.said.alerts)
	}
}

// Part of CA-USO-05 (the delivery of the queue is closed over the real characters and inbox in internal/orchestration/chain)
func TestQuotaBackUnblocksAndDeliversTheQueue(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.character("c2", "Revisor", characters.RoleCharacter, providers.Codex)
	until := w.clk.Now().Add(2 * time.Hour) // 14:00
	w.svc.OnEvent(w.ctx, providers.Event{AgentID: "c2", Event: providers.EventUsage, Provider: providers.Codex, ReceivedAt: w.clk.Now(),
		Usage: &providers.Usage{LimitsReached: true, LimitsResetAt: until}})
	if len(w.chars.blocks) != 1 {
		t.Fatal("the character was not blocked")
	}
	w.clk.Advance(2*time.Hour - time.Second)
	if err := w.svc.Sweep(w.ctx); err != nil {
		t.Fatal(err)
	}
	if len(w.chars.frees) != 0 {
		t.Fatal("unblocked before 14:00")
	}
	w.clk.Advance(2 * time.Second)
	if err := w.svc.Sweep(w.ctx); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(w.chars.frees, []string{"c2"}) {
		t.Fatalf("unblocked %v after 14:00, want c2", w.chars.frees)
	}
	if err := w.svc.Sweep(w.ctx); err != nil {
		t.Fatal(err)
	}
	if len(w.chars.frees) != 1 {
		t.Error("unblocked twice")
	}
}

func TestQuotaWithNoKnownTimeEndsWhenTheProviderAnswersAgain(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.character("c1", "Dev", characters.RoleCharacter, providers.ClaudeCode)
	w.svc.OnEvent(w.ctx, providers.Event{AgentID: "c1", Event: providers.EventTurnStart, ReceivedAt: w.clk.Now()})
	w.svc.OnEvent(w.ctx, providers.Event{AgentID: "c1", Event: providers.EventTurnFailure, StopReason: providers.StopRateLimit, ReceivedAt: w.clk.Now()})
	if len(w.chars.blocks) != 1 {
		t.Fatal("a turn that failed for lack of quota did not wait for it")
	}
	// a reading with room in every window brings it back
	w.svc.OnEvent(w.ctx, providers.Event{AgentID: "c1", Event: providers.EventUsage, ReceivedAt: w.clk.Now(),
		Usage: &providers.Usage{LimitWindows: []providers.LimitWindow{{Window: "5h", UsedPct: 10, ResetsAt: w.clk.Now().Add(time.Hour)}}}})
	if !slices.Equal(w.chars.frees, []string{"c1"}) {
		t.Errorf("unblocked %v, want c1 when the window shows room", w.chars.frees)
	}
}

// CA-USO-06
func TestProviderWithoutTokensKeepsActiveTimeAndOneTurn(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.character("c3", "Dev", characters.RoleCharacter, providers.Antigravity)
	w.svc.OnEvent(w.ctx, providers.Event{AgentID: "c3", Event: providers.EventTurnStart, ReceivedAt: w.clk.Now()})
	w.clk.Advance(90 * time.Second)
	w.svc.OnEvent(w.ctx, providers.Event{AgentID: "c3", Event: providers.EventTurnEnd, ReceivedAt: w.clk.Now()})
	got, err := w.svc.OfCharacter(w.ctx, "c3")
	if err != nil {
		t.Fatal(err)
	}
	if got.Active != 90*time.Second || got.Turns != 1 || got.HasTokens || got.HasCost {
		t.Errorf("record = %+v, want 90 s of active time, one turn and no tokens or cost", got)
	}
	// a limit in USD does not count this use, and the gap shows
	v, err := w.svc.View(w.ctx, usage.Limit{Scope: usage.ScopeTable, Key: "loja/t1", Unit: classes.UnitUSD, Value: 10}, "loja")
	if err != nil {
		t.Fatal(err)
	}
	if v.Used != 0 || v.HasData {
		t.Errorf("view = %+v, want no use and the gap in USD", v)
	}
	if h, _ := w.svc.View(w.ctx, usage.Limit{Scope: usage.ScopeTable, Key: "loja/t1", Unit: classes.UnitHours, Value: 1}, "loja"); !h.HasData || h.Used < 0.0249 || h.Used > 0.0251 {
		t.Errorf("hours view = %+v, want 0.025 h with data", h)
	}
}

// Part of CA-USO-07 (the join with the real chain is in internal/orchestration/chain)
func TestExtraAboveTheCeilingIsNotForTheGrandMaster(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	// the ceiling of a table is US$ 12; the limit is at 10 and 50% more is 15
	if err := w.svc.SetLimit(w.ctx, classes.LevelAdmin, "loja", usage.Limit{Scope: usage.ScopeTable, Key: "loja/t1", Unit: classes.UnitUSD, Value: 10}); err != nil {
		t.Fatal(err)
	}
	if err := w.svc.Record(w.ctx, stepRecord(w, 10)); err != nil {
		t.Fatal(err)
	}
	reqs := w.extra.all()
	if len(reqs) != 1 {
		t.Fatalf("%d requests, want 1", len(reqs))
	}
	if r := reqs[0].Risk; r.Risk != classes.RiskHigh || !r.NoDelegation {
		t.Errorf("risk = %+v, want alto without delegation: the chain then puts it at the people, and only an Administrator or above decides it", r)
	}
	// within the ceiling it is a medium request the Grand Master may decide
	w2 := newWorld(t)
	if err := w2.svc.SetLimit(w2.ctx, classes.LevelAdmin, "loja", usage.Limit{Scope: usage.ScopeTable, Key: "loja/t1", Unit: classes.UnitUSD, Value: 6}); err != nil {
		t.Fatal(err)
	}
	if err := w2.svc.Record(w2.ctx, stepRecord(w2, 6)); err != nil {
		t.Fatal(err)
	}
	if r := w2.extra.all()[0].Risk; r.Risk != classes.RiskMedium || r.NoDelegation {
		t.Errorf("risk within the ceiling = %+v, want medio", r)
	}
	// the gate of the classes agrees: the Grand Master is refused above the ceiling
	c, err := classes.ParseCeiling([]byte(ceilingYAML))
	if err != nil {
		t.Fatal(err)
	}
	spend := classes.ExtraSpend{Unit: classes.UnitUSD, Scope: classes.ScopeTable, Used: 10, Limit: 10, Extra: 5}
	if lvl := c.MinExtraSpendLevel(spend); lvl != classes.LevelAdmin {
		t.Errorf("minimum level = %s, want admin", lvl)
	}
}

// CA-USO-09
func TestSubagentTokensCountForTheCharacterQuestAndTable(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.character("c1", "Dev", characters.RoleCharacter, providers.ClaudeCode)
	// the usage of a turn in which the character used a native subagent: the
	// totals of the source already include it, and the character stays busy
	// meanwhile
	w.svc.OnEvent(w.ctx, providers.Event{AgentID: "c1", Event: providers.EventTurnStart, ReceivedAt: w.clk.Now()})
	w.clk.Advance(time.Minute)
	w.svc.OnEvent(w.ctx, providers.Event{AgentID: "c1", Event: providers.EventUsage, SessionID: "s1", ReceivedAt: w.clk.Now(),
		Usage: &providers.Usage{InputTokens: 700, OutputTokens: 100, CostUSD: 0.2}})
	w.clk.Advance(time.Minute)
	w.svc.OnEvent(w.ctx, providers.Event{AgentID: "c1", Event: providers.EventUsage, SessionID: "s1", ReceivedAt: w.clk.Now(),
		Usage: &providers.Usage{InputTokens: 1700, OutputTokens: 400, CostUSD: 0.9}}) // the subagent's 1000+300 tokens came in
	w.svc.OnEvent(w.ctx, providers.Event{AgentID: "c1", Event: providers.EventTurnEnd, ReceivedAt: w.clk.Now()})

	char, _ := w.svc.OfCharacter(w.ctx, "c1")
	quest, _ := w.svc.Consumption(w.ctx, usage.ScopeQuest, "q1")
	table, _ := w.svc.Consumption(w.ctx, usage.ScopeTable, "loja/t1")
	for name, got := range map[string]usage.Totals{"character": char, "quest": quest, "table": table} {
		if got.Tokens != 2100 || got.Active != 2*time.Minute {
			t.Errorf("%s = %+v, want 2100 tokens (the subagent's included) and the 2 minutes of the turn", name, got)
		}
	}
}

// CA-USO-10
func TestStartThresholdHoldsUntilTheReset(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.dir.steps = 2
	w.character("c1", "Dev", characters.RoleCharacter, providers.ClaudeCode)
	read := w.clk.Now().Add(-20 * time.Minute)
	resets := w.clk.Now().Add(time.Hour)
	sub := w.dir.by["c1"]
	if err := w.svc.Reading(w.ctx, sub, providers.Usage{LimitWindows: []providers.LimitWindow{{Window: "5h", UsedPct: 96, ResetsAt: resets}}}, usage.Reading{At: read}); err != nil {
		t.Fatal(err)
	}
	held, err := w.svc.Held(w.ctx, "c1")
	if err != nil || !held {
		t.Fatalf("Held = %v, %v for 96%% read 20 min ago (old, but above the threshold, and the reset is in an hour); want held", held, err)
	}
	wantLine := "⏸ Mesa Login pausada · 5h 96% · volta às 13:00 · faltam 2 etapas"
	if n := w.said.alertsWith(wantLine); n != 1 {
		t.Errorf("the users heard %d times %q: %v", n, wantLine, w.said.alerts)
	}
	if n := len(w.said.noteTo(characters.ToGrandMaster, wantLine)); n != 1 {
		t.Errorf("the Grand Master heard %d times, want once", n)
	}
	// asked again, it holds and does not repeat itself
	if held, _ := w.svc.Held(w.ctx, "c1"); !held {
		t.Error("not held the second time")
	}
	if n := w.said.alertsWith("⏸"); n != 1 {
		t.Errorf("the line came %d times, want once", n)
	}
	if v, _ := w.svc.Verdict(w.ctx, providers.ClaudeCode, "pessoal", "loja"); !v.Hold || v.Until != resets {
		t.Errorf("verdict = %+v, want held until the reset", v)
	}

	// before the reset the sweep delivers nothing; after it, what waited goes
	w.clk.Advance(30 * time.Minute)
	if err := w.svc.Sweep(w.ctx); err != nil || w.pump.count() != 0 {
		t.Fatalf("pumped %d times before the reset (err %v)", w.pump.count(), err)
	}
	w.clk.Advance(31 * time.Minute)
	if held, _ := w.svc.Held(w.ctx, "c1"); held {
		t.Error("still held after the reset")
	}
	if err := w.svc.Sweep(w.ctx); err != nil || w.pump.count() != 1 {
		t.Fatalf("pumped %d times after the reset (err %v), want 1", w.pump.count(), err)
	}
	if err := w.svc.Sweep(w.ctx); err != nil || w.pump.count() != 1 {
		t.Errorf("pumped again: %d", w.pump.count())
	}
}

// CA-USO-10, the half of the old reading below the threshold
func TestOldReadingBelowTheThresholdIsUnknownAndStepGoesWithAWarning(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.character("mestre", "Mestre", characters.RoleTableMaster, providers.ClaudeCode)
	w.character("c1", "Dev", characters.RoleCharacter, providers.ClaudeCode)
	read := w.clk.Now().Add(-20 * time.Minute)
	if err := w.svc.Reading(w.ctx, w.dir.by["c1"], providers.Usage{LimitWindows: []providers.LimitWindow{{Window: "5h", UsedPct: 40, ResetsAt: w.clk.Now().Add(time.Hour)}}}, usage.Reading{At: read}); err != nil {
		t.Fatal(err)
	}
	held, err := w.svc.Held(w.ctx, "c1")
	if err != nil || held {
		t.Fatalf("Held = %v, %v for 40%% read 20 min ago, want delivered", held, err)
	}
	if got := w.said.noteTo(characters.ToTableMaster, "não está confirmado"); len(got) != 1 {
		t.Fatalf("the table master heard %d warnings, want 1: %+v", len(got), w.said.notes)
	}
	if v, _ := w.svc.Verdict(w.ctx, providers.ClaudeCode, "pessoal", "loja"); v.Hold || !v.Unconfirmed {
		t.Errorf("verdict = %+v, want unconfirmed and not held", v)
	}
	// the warning is given once per reading
	if _, err := w.svc.Held(w.ctx, "c1"); err != nil {
		t.Fatal(err)
	}
	if got := w.said.noteTo(characters.ToTableMaster, "não está confirmado"); len(got) != 1 {
		t.Errorf("warned %d times for one reading", len(got))
	}
	// a recent reading below the threshold is fine, no warning
	if err := w.svc.Reading(w.ctx, w.dir.by["c1"], providers.Usage{LimitWindows: []providers.LimitWindow{{Window: "5h", UsedPct: 50, ResetsAt: w.clk.Now().Add(time.Hour)}}}, usage.Reading{}); err != nil {
		t.Fatal(err)
	}
	if v, _ := w.svc.Verdict(w.ctx, providers.ClaudeCode, "pessoal", "loja"); v.Hold || v.Unconfirmed {
		t.Errorf("verdict = %+v, want plain go", v)
	}
}

func TestWindowWithoutResetOnlyHoldsWhileTheReadingIsRecent(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.character("c1", "Dev", characters.RoleCharacter, providers.ClaudeCode)
	if err := w.svc.Reading(w.ctx, w.dir.by["c1"], providers.Usage{LimitWindows: []providers.LimitWindow{{Window: "7d", UsedPct: 97}}}, usage.Reading{}); err != nil {
		t.Fatal(err)
	}
	if held, _ := w.svc.Held(w.ctx, "c1"); !held {
		t.Fatal("a recent reading above the threshold did not hold")
	}
	w.clk.Advance(16 * time.Minute)
	if held, _ := w.svc.Held(w.ctx, "c1"); held {
		t.Error("an old reading without a reset still held")
	}
	if err := w.svc.Sweep(w.ctx); err != nil || w.pump.count() != 1 {
		t.Errorf("what waited was not delivered when the reading went stale (%d pumps, err %v)", w.pump.count(), err)
	}
	// the threshold of the workspace is the Administrator's
	if err := w.svc.SetThreshold(w.ctx, classes.LevelApprover, "loja", 90); !errors.Is(err, usage.ErrForbidden) {
		t.Errorf("an approver set the threshold: %v", err)
	}
	if err := w.svc.SetThreshold(w.ctx, classes.LevelAdmin, "loja", 99); err != nil {
		t.Fatal(err)
	}
	if err := w.svc.Reading(w.ctx, w.dir.by["c1"], providers.Usage{LimitWindows: []providers.LimitWindow{{Window: "7d", UsedPct: 97, ResetsAt: w.clk.Now().Add(time.Hour)}}}, usage.Reading{}); err != nil {
		t.Fatal(err)
	}
	if held, _ := w.svc.Held(w.ctx, "c1"); held {
		t.Error("97% held with the threshold at 99%")
	}
}

// CA-USO-11
func TestLimitAboveTheCeilingAndTheTotalOfTheDayAreRefused(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	over := usage.Limit{Scope: usage.ScopeTable, Key: "loja/t1", Unit: classes.UnitUSD, Value: 13}
	if err := w.svc.SetLimit(w.ctx, classes.LevelAdmin, "loja", over); !errors.Is(err, classes.ErrAboveCostCeiling) {
		t.Errorf("a table limit of 13 over a ceiling of 12 = %v, want refused", err)
	}
	if err := w.svc.SetLimit(w.ctx, classes.LevelAdmin, "loja", usage.Limit{Scope: usage.ScopeDay, Key: "loja", Unit: classes.UnitUSD, Value: 31}); !errors.Is(err, classes.ErrAboveCostCeiling) {
		t.Errorf("a day limit over the ceiling = %v, want refused", err)
	}
	over.Value = 12
	if err := w.svc.SetLimit(w.ctx, classes.LevelAdmin, "loja", over); err != nil {
		t.Errorf("a limit at the ceiling: %v", err)
	}
	total := usage.Limit{Scope: usage.ScopeDayTotal, Unit: classes.UnitUSD, Value: 50}
	if err := w.svc.SetLimit(w.ctx, classes.LevelAdmin, "", total); !errors.Is(err, usage.ErrForbidden) {
		t.Errorf("the total of the day saved by an Administrator = %v, want refused", err)
	}
	if err := w.svc.SetLimit(w.ctx, classes.LevelOwner, "", total); err != nil {
		t.Errorf("the Owner could not set the total of the day: %v", err)
	}
	if err := w.svc.SetLimit(w.ctx, classes.LevelApprover, "loja", over); !errors.Is(err, usage.ErrForbidden) {
		t.Errorf("an approver set a limit: %v", err)
	}
	if err := w.svc.SetLimit(w.ctx, classes.LevelOwner, "loja", usage.Limit{Scope: usage.ScopeTable, Key: "loja/t1", Unit: classes.UnitUSD, Value: -1}); !errors.Is(err, usage.ErrInvalid) {
		t.Errorf("a negative limit = %v, want invalid", err)
	}
}

// CA-USO-12
func TestConformanceCountsOnlyInTheTotalOfTheDay(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	for _, l := range []usage.Limit{
		{Scope: usage.ScopeTable, Key: "loja/t1", Unit: classes.UnitUSD, Value: 10},
		{Scope: usage.ScopeWorkspace, Key: "loja", Unit: classes.UnitUSD, Value: 10},
		{Scope: usage.ScopeDay, Key: "loja", Unit: classes.UnitUSD, Value: 10},
	} {
		if err := w.svc.SetLimit(w.ctx, classes.LevelOwner, "loja", l); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.svc.SetLimit(w.ctx, classes.LevelOwner, "", usage.Limit{Scope: usage.ScopeDayTotal, Unit: classes.UnitUSD, Value: 10}); err != nil {
		t.Fatal(err)
	}
	if err := w.svc.Record(w.ctx, stepRecord(w, 1)); err != nil {
		t.Fatal(err)
	}
	if err := w.svc.Record(w.ctx, usage.Record{Provider: providers.ClaudeCode, Profile: "pessoal", Source: "conformance", Origin: usage.OriginConformance, CostUSD: usd(2), At: w.clk.Now()}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		scope usage.Scope
		key   string
		want  float64
	}{{usage.ScopeTable, "loja/t1", 1}, {usage.ScopeWorkspace, "loja", 1}, {usage.ScopeDay, "loja", 1}, {usage.ScopeDayTotal, "", 3}} {
		got, err := w.svc.Consumption(w.ctx, c.scope, c.key)
		if err != nil || got.CostUSD != c.want {
			t.Errorf("%s = %v (%v), want %v", c.scope, got.CostUSD, err, c.want)
		}
	}
	if acc, _ := w.svc.OfAccount(w.ctx, "claude-code", "pessoal"); acc.CostUSD != 3 {
		t.Errorf("the account used US$ %v, want 3 (the conformance counts there)", acc.CostUSD)
	}
	// a record of the conformance with a table is a mistake of the caller
	bad := usage.Record{Provider: providers.Codex, Source: "conformance", Origin: usage.OriginClassTest, Workspace: "loja", CostUSD: usd(1)}
	if err := w.svc.Record(w.ctx, bad); err == nil {
		t.Error("a class test with a workspace was recorded")
	}
	// the total of the day pauses every workspace, and the extra is asked for
	w.pause.spaces = []string{"loja", "blog"}
	if err := w.svc.Record(w.ctx, usage.Record{Provider: providers.Codex, Source: "conformance", Origin: usage.OriginConformance, CostUSD: usd(8), At: w.clk.Now()}); err != nil {
		t.Fatal(err)
	}
	if got := w.pause.got(); !slices.Equal(got, []string{"pause workspace loja", "pause workspace blog"}) {
		t.Errorf("the total of the day paused %v", got)
	}
	if err := w.svc.Decided(w.ctx, usage.Decision{Request: int64(len(w.extra.all())), Approved: true}); err != nil {
		t.Fatal(err)
	}
	if got := w.pause.got(); !slices.Equal(got[2:], []string{"resume workspace loja", "resume workspace blog"}) {
		t.Errorf("after the approval: %v", got)
	}
}

// CA-USO-12, the half of the source that is not available
func TestUnavailableSourceKeepsTimeAndTurnsAndOpensALL23(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.character("c1", "Dev", characters.RoleCharacter, providers.Codex)
	w.svc.OnEvent(w.ctx, providers.Event{AgentID: "c1", Event: providers.EventTurnStart, ReceivedAt: w.clk.Now()})
	w.clk.Advance(2 * time.Minute)
	w.svc.OnEvent(w.ctx, providers.Event{AgentID: "c1", Event: providers.EventTurnEnd, ReceivedAt: w.clk.Now()})
	if err := w.svc.SourceUnavailable(w.ctx, "c1", "token_count", "the transcript was not found"); err != nil {
		t.Fatal(err)
	}
	got, _ := w.svc.OfCharacter(w.ctx, "c1")
	if got.Active != 2*time.Minute || got.Turns != 1 || got.HasTokens || got.HasCost {
		t.Errorf("record = %+v, want only the time and one turn", got)
	}
	if !slices.Equal(w.check.codes, []string{"ALL-23 c1"}) {
		t.Errorf("the checklist got %v, want ALL-23 for c1", w.check.codes)
	}
}

func TestDayTurnsAtMidnightOfTheGuildZone(t *testing.T) {
	t.Parallel()
	zone := time.FixedZone("BRT", -3*3600)
	w := newWorld(t, func(o *usage.Options) { o.Zone = func(context.Context) *time.Location { return zone } })
	w.clk.Set(time.Date(2026, 10, 20, 2, 30, 0, 0, time.UTC)) // 23:30 of the 19th in the zone of the Guild
	if got := w.svc.Today(w.ctx); got != "2026-10-19" {
		t.Fatalf("today = %s, want the 19th in the zone of the Guild", got)
	}
	if err := w.svc.SetLimit(w.ctx, classes.LevelOwner, "loja", usage.Limit{Scope: usage.ScopeDay, Key: "loja", Unit: classes.UnitUSD, Value: 10}); err != nil {
		t.Fatal(err)
	}
	if err := w.svc.Record(w.ctx, stepRecord(w, 9)); err != nil {
		t.Fatal(err)
	}
	if got, _ := w.svc.Consumption(w.ctx, usage.ScopeDay, "loja"); got.CostUSD != 9 {
		t.Fatalf("the day used %v, want 9", got.CostUSD)
	}
	w.clk.Advance(time.Hour) // 00:30 of the 20th
	if got, _ := w.svc.Consumption(w.ctx, usage.ScopeDay, "loja"); got.CostUSD != 0 {
		t.Errorf("the new day started with %v, want 0", got.CostUSD)
	}
	if got, _ := w.svc.Consumption(w.ctx, usage.ScopeWorkspace, "loja"); got.CostUSD != 9 {
		t.Errorf("the workspace lost its use at midnight: %v", got.CostUSD)
	}
	// the warning of the new day is a new warning
	if err := w.svc.Record(w.ctx, stepRecord(w, 8.5)); err != nil {
		t.Fatal(err)
	}
	if n := w.said.alertsWith("Uso em 85%"); n != 1 {
		t.Errorf("%d warnings of the new day, want 1: %v", n, w.said.alerts)
	}
}
