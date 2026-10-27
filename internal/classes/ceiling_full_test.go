//go:build full

package classes_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/store"
	"github.com/mestre-ia/mestre/internal/testkit/guildtpl"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// realKeeper keeps the ceiling of a workspace in a real teto.yaml and the
// real Guild database, with the fake clock, as the Citadel does.
func realKeeper(t *testing.T, alerts *alertLog) (*classes.CeilingKeeper, string, *clock.Fake) {
	t.Helper()
	dir := t.TempDir()
	db, err := guildtpl.Open(context.Background(), filepath.Join(dir, "mestre.db"), store.Options{Synchronous: "OFF"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	path := filepath.Join(dir, "loja", filepath.FromSlash(classes.CeilingFile))
	clk := clock.NewFake(now)
	return &classes.CeilingKeeper{
		Workspace: "loja", File: classes.CeilingPath(path), Records: classes.CeilingDB{DB: db}, Alerts: alerts, Clock: clk,
	}, path, clk
}

// CA-TETO-03
func TestAHandEditThatFreesDockerHoldsOnlyAfterADecree(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	alerts := &alertLog{c: make(chan classes.CeilingAlert, 4)}
	k, path, clk := realKeeper(t, alerts)
	if err := k.Change(ctx, []byte(docCeiling), classes.Authority{Level: classes.LevelAdmin, Via: classes.ViaPanel}); err != nil {
		t.Fatal(err)
	}
	approved := ceiling(t, docCeiling).Hash
	class := parse(t, "version: 1\ncommands: {allow: [\"docker *\"], default: ask}\n")
	m := newMachine()
	dockerUnder := func() classes.Decision {
		t.Helper()
		c, err := k.Current(ctx)
		if err != nil {
			t.Fatal(err)
		}
		r, err := classes.Build(classes.Layers{AgentID: agentID, Class: "ops", Permissions: class, Ceiling: c.Maximum(), Now: now})
		if err != nil {
			t.Fatal(err)
		}
		return decide(m.env(), r, m.shell("docker run alpine"))
	}
	want(t, dockerUnder(), classes.Deny, classes.LayerCeiling, "teto da campanha")

	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); k.Run(ctx, func(err error) { t.Error(err) }) }()
	defer wg.Wait()
	defer cancel()
	clk.BlockUntil(1) // the watch is armed; its first check saw the approved file

	edited := strings.Replace(docCeiling, `"docker *", `, "", 1)
	if err := os.WriteFile(path, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	clk.Advance(classes.DefaultCeilingInterval)
	a := <-alerts.c
	if a.Kind != classes.CeilingWidens || a.Approved != approved || !strings.Contains(strings.Join(a.Diff.Widens, ";"), "docker *") {
		t.Fatalf("alert = %+v; want the widening that frees docker *", a)
	}
	want(t, dockerUnder(), classes.Deny, classes.LayerCeiling, "teto da campanha")

	pending := ceiling(t, edited).Hash
	if err := k.ApprovePending(ctx, pending, classes.Authority{Level: classes.LevelAdmin, Via: classes.ViaPanel}); !errors.Is(err, classes.ErrCeilingNeedsDecree) {
		t.Fatalf("approving on the panel: %v; want ErrCeilingNeedsDecree", err)
	}
	want(t, dockerUnder(), classes.Deny, classes.LayerCeiling)
	if err := k.ApprovePending(ctx, pending, classes.Authority{Level: classes.LevelAdmin, Via: classes.ViaDecree, Decree: 31}); err != nil {
		t.Fatal(err)
	}
	if d := dockerUnder(); d.Verdict != classes.Allow {
		t.Fatalf("after the decree: %+v; want docker allowed by the class", d)
	}
	if len(alerts.all()) != 1 {
		t.Fatalf("alerts = %+v; want one", alerts.all())
	}
}

// CA-TETO-04
func TestTheGrandMasterCannotApproveSpendBeyondTheCostCeiling(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	k, _, _ := realKeeper(t, &alertLog{})
	if err := k.Change(ctx, []byte(docCeiling), classes.Authority{Level: classes.LevelAdmin, Via: classes.ViaPanel}); err != nil {
		t.Fatal(err)
	}
	users := &askLog{}
	gate := &classes.CostGate{Ceiling: k, Users: users}
	atCeiling := classes.ExtraSpend{Request: 9, Workspace: "loja", Table: "42-login", Unit: classes.UnitUSD, Scope: classes.ScopeTable, Used: 50, Limit: 50, Extra: 25}

	err := gate.CheckApproval(ctx, atCeiling, classes.LevelGrandMaster)
	if !errors.Is(err, classes.ErrHumanRequired) {
		t.Fatalf("the Grand Master approving: %v; want ErrHumanRequired", err)
	}
	if len(users.asked) != 1 || users.asked[0].min != classes.LevelAdmin || users.asked[0].r.Request != 9 {
		t.Fatalf("asked = %+v; want the request taken to Administrators and above", users.asked)
	}
	if err := gate.CheckApproval(ctx, atCeiling, classes.LevelApprover); !errors.Is(err, classes.ErrLevelTooLow) {
		t.Fatalf("an Approver: %v; want ErrLevelTooLow", err)
	}
	for _, l := range []classes.Level{classes.LevelAdmin, classes.LevelOwner} {
		if err := gate.CheckApproval(ctx, atCeiling, l); err != nil {
			t.Fatalf("%s: %v", l, err)
		}
	}
	within := atCeiling
	within.Used, within.Limit, within.Extra = 20, 20, 10
	if err := gate.CheckApproval(ctx, within, classes.LevelGrandMaster); err != nil || len(users.asked) != 1 {
		t.Fatalf("within the ceiling: %v, asked %d; the ceiling leaves it to the policy", err, len(users.asked))
	}
}

type asked struct {
	r   classes.ExtraSpend
	min classes.Level
}

type askLog struct{ asked []asked }

func (a *askLog) AskUsers(_ context.Context, r classes.ExtraSpend, min classes.Level, _ string) error {
	a.asked = append(a.asked, asked{r, min})
	return nil
}
