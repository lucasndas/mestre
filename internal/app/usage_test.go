package app_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/usage"
	"github.com/lucasndas/mestre/internal/orchestration/workspaces"
	"github.com/lucasndas/mestre/internal/providers"
)

// The limits of use are wired to the real services of the Citadel: the use of
// the table master of a table, taken from its events, pauses the real table at
// 100% of its limit, a provider that cut it puts it in blocked (reason quota)
// until the quota is back, and a window at the start threshold holds the
// deliveries of its inbox. (CA-USO-03, CA-USO-05 and CA-USO-10 have their own
// tests in internal/orchestration/usage, over fakes of the pieces.)
func TestTheLimitsOfUseActOnTheRealServices(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	w := newOrcWorld(t)
	orc := w.orc
	fwd := localchan.NewLocalForwarder()
	orc.Register(fwd, nil)
	ws, err := orc.Workspaces.Create(ctx, workspaces.CreateRequest{Repo: "lucas/Meu_App"})
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range workspaces.Stages {
		if st != workspaces.StageMirror {
			if err := orc.Workspaces.Register(st, func(context.Context, workspaces.Workspace) error { return nil }); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := orc.Workspaces.Prepare(ctx, ws.Slug); err != nil {
		t.Fatal(err)
	}
	gmRes, err := orc.Characters.Create(ctx, characters.CreateRequest{Sheet: "Grão-Mestre", Workspace: ws.Slug, Role: characters.RoleGrandMaster, Creator: characters.Creator{Kind: characters.CreatedByEngine}})
	if err != nil {
		t.Fatal(err)
	}
	r := call(t, fwd, gmRes.Character.ID, "table open", map[string]any{"epic": "42"})
	if !r.OK {
		t.Fatalf("table open = %+v", r.Error)
	}
	var opened struct{ Name string }
	if err := json.Unmarshal(r.Data, &opened); err != nil {
		t.Fatal(err)
	}
	master, ok, err := orc.Registry.TableMaster(ctx, ws.Slug, opened.Name)
	if err != nil || !ok {
		t.Fatalf("table master = %v, %v", ok, err)
	}

	// 100% of the limit of the table pauses the real table
	if err := orc.Usage.SetLimit(ctx, classes.LevelAdmin, ws.Slug, usage.Limit{Scope: usage.ScopeTable, Key: usage.TableKey(ws.Slug, opened.Name), Unit: classes.UnitUSD, Value: 5}); err != nil {
		t.Fatal(err)
	}
	orc.Usage.OnEvent(ctx, providers.Event{AgentID: master.ID, Event: providers.EventUsage, Provider: providers.ClaudeCode, SessionID: "s", ReceivedAt: w.clk.Now(),
		Usage: &providers.Usage{InputTokens: 100, OutputTokens: 10, CostUSD: 6}})
	if paused, err := orc.Tables.Paused(ctx, ws.Slug, opened.Name); err != nil || !paused {
		t.Fatalf("the table is paused = %v (%v), want paused by the limit", paused, err)
	}
	if tbl, _ := orc.Tables.Get(ctx, ws.Slug, opened.Name); tbl.PausedBy != "usage_limit" {
		t.Errorf("paused by %q, want usage_limit", tbl.PausedBy)
	}
	if got, _ := orc.Usage.Consumption(ctx, usage.ScopeTable, usage.TableKey(ws.Slug, opened.Name)); got.CostUSD != 6 || got.Tokens != 110 {
		t.Errorf("table use = %+v", got)
	}

	// a provider that cut the table master puts it in blocked, reason quota; the sweep brings it back
	until := w.clk.Now().Add(time.Hour)
	orc.Usage.OnEvent(ctx, providers.Event{AgentID: master.ID, Event: providers.EventUsage, Provider: providers.ClaudeCode, SessionID: "s", ReceivedAt: w.clk.Now(),
		Usage: &providers.Usage{LimitsReached: true, LimitsResetAt: until}})
	got, _ := orc.Characters.Get(ctx, master.ID)
	if got.State != characters.Blocked || got.Reason != characters.ReasonQuota {
		t.Fatalf("the table master is %s (%s), want blocked for quota", got.State, got.Reason)
	}
	w.clk.Advance(time.Hour + time.Second)
	if err := orc.Usage.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := orc.Characters.Get(ctx, master.ID); got.State == characters.Blocked {
		t.Errorf("the table master is still blocked after the quota came back")
	}

	// a window at the start threshold holds the deliveries
	read := w.clk.Now()
	orc.Usage.OnEvent(ctx, providers.Event{AgentID: master.ID, Event: providers.EventUsage, Provider: providers.ClaudeCode, SessionID: "s", ReceivedAt: read,
		Usage: &providers.Usage{LimitWindows: []providers.LimitWindow{{Window: "5h", UsedPct: 97, ResetsAt: read.Add(2 * time.Hour)}}}})
	if held, err := orc.Usage.Held(ctx, master.ID); err != nil || !held {
		t.Errorf("held = %v (%v), want held at 97%%", held, err)
	}
}
