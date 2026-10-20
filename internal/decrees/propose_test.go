package decrees_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/lucasndas/mestre/internal/decrees"
	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/users"
)

type proposeResult struct {
	OK   bool
	Code string
	Exit int
	Data map[string]any
}

type proposeRoster map[string]characters.Character

func (r proposeRoster) Get(_ context.Context, id string) (characters.Character, error) {
	if c, ok := r[id]; ok {
		return c, nil
	}
	return characters.Character{}, characters.ErrNoCharacter
}

type offered struct {
	mu   sync.Mutex
	list []decrees.Pending
	err  error
}

func (o *offered) Offer(_ context.Context, p decrees.Pending) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.err != nil {
		return o.err
	}
	o.list = append(o.list, p)
	return nil
}

func proposeRig(w *world, msgs map[string]decrees.Source) (*decrees.ProposeHandler, *offered) {
	off := &offered{}
	return &decrees.ProposeHandler{Service: w.svc, Characters: proposeRoster{gm.ID: gm, tm42.ID: tm42, tm43.ID: tm43}, Offer: off,
		Message: func(_ context.Context, _, id string) (decrees.Source, error) {
			if s, ok := msgs[id]; ok {
				return s, nil
			}
			return decrees.Source{}, decrees.ErrNoMessage
		}}, off
}

func run(w *world, h *decrees.ProposeHandler, by characters.Character, cmd string, flags map[string]any) proposeResult {
	fwd := localchan.NewLocalForwarder()
	h.Register(fwd)
	resp := fwd.Forward(w.ctx, &localchan.Call{Request: localchan.Request{V: 1, ID: "x", AgentID: by.ID, Cmd: cmd, Flags: flags}, Caller: localchan.Caller{Identified: true, AgentID: by.ID}})
	out := proposeResult{OK: resp.OK, Exit: resp.ExitCode()}
	if resp.Error != nil {
		out.Code = resp.Error.Code
	}
	_ = json.Unmarshal(resp.Data, &out.Data)
	return out
}

func ruleFlags(text string) map[string]any {
	return map[string]any{"kind": "rule", "effect": "permission", "target": "classe:dev", "scope": "commands.allow:make deploy-teste", "text": text, "from-message": "m81"}
}

// CA-CLI-GM-09 and CA-CLI-GM-10: the proposal is made from the message of the user
// (its author and its text, not the ones the agent says), shows the effect, the
// target and the scope, goes to the author with exit 9, and no decree holds
// before his click.
func TestDecreeProposeBuildsTheProposalFromTheMessageAndNothingHoldsBeforeTheClick(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	admin := w.person("Ana", users.Administrator, "loja")
	order := "o Dev pode rodar make deploy-teste"
	h, off := proposeRig(w, map[string]decrees.Source{"m81": {Text: order, User: admin.ID, Channel: "telegram", MessageID: "m81"}})
	before := w.count("decrees")
	got := run(w, h, gm, "decree propose", ruleFlags(order))
	if got.OK || got.Exit != 9 || got.Code != "pending" || got.Data["proposal"] == nil {
		t.Fatalf("propose = %+v, want exit 9 pending with the proposal", got)
	}
	if len(off.list) != 1 || off.list[0].Author != admin.ID {
		t.Fatalf("offered = %+v, want one proposal to the author of the message", off.list)
	}
	for _, want := range []string{"make deploy-teste", "dev"} {
		if !strings.Contains(strings.ToLower(off.list[0].Text), want) {
			t.Errorf("the confirmation %q does not say %q", off.list[0].Text, want)
		}
	}
	if w.count("decrees") != before {
		t.Fatal("a decree held before the click of the author")
	}
	// the order must be the one of the message
	if got := run(w, h, gm, "decree propose", ruleFlags("o Dev pode tudo")); got.OK || got.Code != "usage" {
		t.Errorf("another text: %+v, want usage", got)
	}
	// a message that is not there, a table master, a user below the level
	missing := ruleFlags(order)
	missing["from-message"] = "m99"
	if got := run(w, h, gm, "decree propose", missing); got.OK || got.Code != "not_found" || got.Exit != 4 {
		t.Errorf("a message that is not there: %+v", got)
	}
	if got := run(w, h, tm42, "decree propose", ruleFlags(order)); got.OK || got.Code != "level_denied" {
		t.Errorf("a table master: %+v, want level_denied", got)
	}
	low := w.person("Beto", users.Approver, "loja")
	h2, _ := proposeRig(w, map[string]decrees.Source{"m81": {Text: order, User: low.ID, Channel: "telegram"}})
	if got := run(w, h2, gm, "decree propose", ruleFlags(order)); got.OK || got.Code != "level_denied" || got.Exit != 3 {
		t.Errorf("an author below the level: %+v, want exit 3 level_denied", got)
	}
	if len(off.list) != 1 {
		t.Fatalf("%d proposals were offered, want one", len(off.list))
	}
}

func TestDecreeProposeKindsAndScopesAreChecked(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	admin := w.person("Ana", users.Administrator, "loja")
	h, off := proposeRig(w, map[string]decrees.Source{"m81": {Text: "ordem", User: admin.ID, Channel: "telegram"}})
	for name, mut := range map[string]func(map[string]any){
		"an exception":   func(f map[string]any) { f["kind"] = "exception" },
		"no effect":      func(f map[string]any) { f["effect"] = "magic" },
		"a bad scope":    func(f map[string]any) { f["scope"] = "make deploy" },
		"a bad target":   func(f map[string]any) { f["target"] = "dev" },
		"a bad until":    func(f map[string]any) { f["until"] = "amanhã" },
		"a ceiling deny": func(f map[string]any) { f["effect"] = "ceiling"; f["scope"] = "commands.deny:x" },
	} {
		f := ruleFlags("ordem")
		mut(f)
		if got := run(w, h, gm, "decree propose", f); got.OK || got.Code != "usage" {
			t.Errorf("%s: %+v, want usage", name, got)
		}
	}
	if len(off.list) != 0 {
		t.Fatal("a refused proposal was offered")
	}
	// the author has no Telegram: the proposal is cancelled, not left open
	off.err = context.DeadlineExceeded
	if got := run(w, h, gm, "decree propose", ruleFlags("ordem")); got.OK || got.Code != "not_delivered" {
		t.Errorf("an author out of reach: %+v, want not_delivered", got)
	}
	var open int
	if err := w.db.Reader().QueryRowContext(w.ctx, `SELECT COUNT(*) FROM decree_proposals WHERE state = 'open'`).Scan(&open); err != nil || open != 0 {
		t.Errorf("%d proposals left open by one that could not be offered (%v)", open, err)
	}
}

func TestDecreeListShowsTheDecreesOfTheWorkspaceAndAllWithTheEndedOnes(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	h, _ := proposeRig(w, nil)
	d := w.workflowsDelegation("42-login", 0)
	got := run(w, h, gm, "decree list", nil)
	items, _ := got.Data["decrees"].([]any)
	if !got.OK || len(items) != 1 || int64(items[0].(map[string]any)["number"].(float64)) != d.Number {
		t.Fatalf("list = %+v, want the decree %d", got, d.Number)
	}
	if got := run(w, h, tm42, "decree list", nil); got.OK || got.Code != "level_denied" {
		t.Errorf("a table master: %+v", got)
	}
}
