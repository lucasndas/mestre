package characters_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/orchestration/connections"
	"github.com/mestre-ia/mestre/internal/providers"
)

// reasonsBetween are the reasons of the connection of two characters, sorted.
func (r *rig) reasonsBetween(a, b characters.Character) string {
	r.t.Helper()
	c, ok, err := r.conns.Store().Between(r.ctx, connections.Char(a.ID), connections.Char(b.ID))
	if err != nil {
		r.t.Fatal(err)
	}
	if !ok {
		return ""
	}
	names := c.ReasonNames()
	slices.Sort(names)
	return strings.Join(names, ",")
}

// A character is
// born connected to the table master and to whoever recruited it, and the
// table master to the Grand Master; a character that ends takes its
// connections with it, and the others stay.
func TestACharacterIsBornConnectedAndLeavesNoConnectionWhenItEnds(t *testing.T) {
	t.Parallel()
	r := newRig(t, rigOptions{connect: true})
	gm := r.makeGM()
	tm := r.makeTM("3-login")
	dev, err := r.create(characters.CreateRequest{Name: "Dev", Sheet: "Dev Claude", Table: "3-login", Creator: byChar(tm)})
	if err != nil {
		t.Fatal(err)
	}
	qa := r.make("Dev Claude", "QA", "3-login") // a step of a quest: the engine made it
	other := r.make("Dev Claude", "Outro", "4-api")
	if got := r.reasonsBetween(tm, gm); got != "mestre" {
		t.Errorf("table master and Grand Master: %q, want mestre", got)
	}
	if got := r.reasonsBetween(dev.Character, tm); got != "criador,mesa" {
		t.Errorf("Dev and the table master that recruited it: %q, want criador,mesa", got)
	}
	if got := r.reasonsBetween(qa, tm); got != "mesa" {
		t.Errorf("QA and the table master: %q, want mesa", got)
	}
	if got := r.reasonsBetween(dev.Character, qa); got != "" {
		t.Errorf("Dev and QA: %q, want no connection", got)
	}
	if got := r.reasonsBetween(other, tm); got != "" {
		t.Errorf("a character of another table and this table master: %q, want none", got)
	}

	r.ready(dev.Character)
	if res, err := r.svc.Dismiss(r.ctx, characters.DismissRequest{ID: dev.Character.ID, By: engine}); err != nil || res.Status != characters.Dismissed {
		t.Fatalf("dismiss: %+v, %v", res, err)
	}
	for _, other := range []characters.Character{tm, qa} {
		if got := r.reasonsBetween(dev.Character, other); got != "" {
			t.Errorf("Dev ended, yet connected to %s: %q", other.Name, got)
		}
	}
	if got := r.reasonsBetween(qa, tm); got != "mesa" {
		t.Errorf("QA lost its connection when Dev ended: %q", got)
	}
}

// A character whose start fails is removed with its connections.
func TestACharacterThatDoesNotStartLeavesNoConnection(t *testing.T) {
	t.Parallel()
	r := newRig(t, rigOptions{connect: true})
	tm := r.makeTM("3-login")
	r.tables.failWorktree = true
	if _, err := r.create(characters.CreateRequest{Name: "Dev", Sheet: "Dev Claude", Table: "3-login", Creator: byChar(tm)}); err == nil {
		t.Fatal("the start did not fail")
	}
	cs, err := r.conns.Store().Of(r.ctx, connections.Char(tm.ID))
	if err != nil || len(cs) != 0 {
		t.Fatalf("connections of the table master after a failed start: %+v, %v", cs, err)
	}
}

// `recruit --replace` keeps the connections of the old character, the manual
// ones included, even when the old one is busy and ends later.
func TestReplaceKeepsTheConnectionsOfTheOldCharacter(t *testing.T) {
	t.Parallel()
	r := newRig(t, rigOptions{machines: []machineSpec{linux("a")}, connect: true})
	tm := r.makeTM("3-login")
	old, err := r.create(characters.CreateRequest{Name: "Dev-1", Sheet: "Dev Claude", Table: "3-login", Creator: byChar(tm)})
	if err != nil {
		t.Fatal(err)
	}
	c := old.Character
	qa := r.make("Dev Claude", "QA", "3-login")
	if _, err := r.conns.Connect(r.ctx, connections.ConnectRequest{Caller: tm, A: "Dev-1", B: "QA"}); err != nil {
		t.Fatal(err)
	}
	r.ready(c)
	_ = r.svc.TaskAssigned(r.ctx, c.ID)
	r.busy(c)
	res, err := r.svc.Replace(r.ctx, characters.ReplaceRequest{Workspace: workspace, Table: "3-login", Name: "Dev-1", Sheet: "Dev Persistente", By: byChar(tm)})
	if err != nil || res.Pending == "" {
		t.Fatalf("replace of a busy character: %v %+v, want pending", err, res)
	}
	_ = r.svc.Done(r.ctx, c.ID, "feito")
	r.hook(c, providers.EventTurnEnd)
	n, err := r.reg.ByName(r.ctx, workspace, "3-login", "Dev-1")
	if err != nil || n.ID == c.ID {
		t.Fatalf("the new one = %+v, %v", n, err)
	}
	if got := r.reasonsBetween(n, qa); got != "manual" {
		t.Errorf("the new Dev-1 and QA: %q, want the manual connection of the old one", got)
	}
	if got := r.reasonsBetween(n, tm); got != "criador,mesa" {
		t.Errorf("the new Dev-1 and the table master: %q, want criador,mesa", got)
	}
	if cs, _ := r.conns.Store().Of(r.ctx, connections.Char(c.ID)); len(cs) != 0 {
		t.Errorf("the old one kept connections: %+v", cs)
	}
}
