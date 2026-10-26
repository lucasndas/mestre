package decrees_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/decrees"
	"github.com/mestre-ia/mestre/internal/localchan"
	"github.com/mestre-ia/mestre/internal/orchestration/characters"
)

type roster map[string]characters.Character

func (r roster) Get(_ context.Context, id string) (characters.Character, error) {
	if c, ok := r[id]; ok {
		return c, nil
	}
	return characters.Character{}, characters.ErrNoCharacter
}

func (r roster) TableMaster(_ context.Context, _, table string) (characters.Character, bool, error) {
	for _, c := range r {
		if c.Role == characters.RoleTableMaster && c.Table == table {
			return c, true, nil
		}
	}
	return characters.Character{}, false, nil
}

type sentinel struct {
	OK   bool
	Code string
	Exit int
	Data map[string]any
}

func delegate(w *world, by characters.Character, flags map[string]any) sentinel {
	fwd := localchan.NewLocalForwarder()
	(&decrees.SubHandler{Service: w.svc, Characters: roster{gm.ID: gm, tm42.ID: tm42, tm43.ID: tm43}}).Register(fwd)
	resp := fwd.Forward(w.ctx, &localchan.Call{Request: localchan.Request{V: 1, ID: "x", AgentID: by.ID, Cmd: "approval delegate", Flags: flags},
		Caller: localchan.Caller{Identified: true, AgentID: by.ID}})
	out := sentinel{OK: resp.OK, Exit: resp.ExitCode()}
	if resp.Error != nil {
		out.Code = resp.Error.Code
	}
	_ = json.Unmarshal(resp.Data, &out.Data)
	return out
}

// Part of CA-DELEG-04 (the command; the rest is in delegation_test.go) and CA-CLI-GM-12: the Grand Master passes on what it
// decides to the Mestre of a table; wider than the origin, another table, too long
// is 3 delegation_too_wide; the Mestre da mesa is level_denied.
func TestApprovalDelegateByTheCommandSubdelegatesNarrowerOnly(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	origin := w.workflowsDelegation("42-login", 0)
	at := func(d time.Duration) string { return w.clk.Now().Add(d).UTC().Format(time.RFC3339) }
	flags := func(table string, d time.Duration) map[string]any {
		return map[string]any{"table": table, "type": "envio_workflows", "until": at(d), "reason": "workflows do login"}
	}
	got := delegate(w, gm, flags("42-login", 2*day))
	if !got.OK || int64(got.Data["origin"].(float64)) != origin.Number || got.Data["table"] != "42-login" {
		t.Fatalf("delegate = %+v, want a subdelegation from the decree %d", got, origin.Number)
	}
	for name, c := range map[string]struct {
		by    characters.Character
		flags map[string]any
		code  string
	}{
		"a table without the origin": {gm, flags("43-busca", 2*day), "delegation_too_wide"},
		"a fixed type":               {gm, map[string]any{"table": "42-login", "type": "ativacao_operador", "until": at(2 * day), "reason": "x"}, "delegation_too_wide"},
		"forty days":                 {gm, flags("42-login", 40*day), "delegation_too_wide"},
		"a table master":             {tm42, flags("42-login", 2*day), "level_denied"},
	} {
		if got := delegate(w, c.by, c.flags); got.OK || got.Code != c.code || got.Exit != 3 {
			t.Errorf("%s: %+v, want exit 3 %s", name, got, c.code)
		}
	}
	for name, until := range map[string]string{"in the past": at(-time.Hour), "with no zone": "2026-11-20T18:00:00"} {
		f := flags("42-login", 2*day)
		f["until"] = until
		if got := delegate(w, gm, f); got.OK {
			t.Errorf("an until %s was accepted: %+v", name, got)
		}
	}
	typo := flags("42-login", 2*day)
	typo["type"] = "envio_workflow"
	if got := delegate(w, gm, typo); got.OK || got.Code != "usage" {
		t.Errorf("a type that does not exist: %+v, want usage", got)
	}
	bad := flags("42-login", 2*day)
	bad["until"] = "amanhã"
	if got := delegate(w, gm, bad); got.OK || got.Code != "usage" {
		t.Errorf("an until that is not a time: %+v, want usage", got)
	}
	withPattern := flags("42-login", 2*day)
	withPattern["pattern"] = "npm *"
	if got := delegate(w, gm, withPattern); got.OK || got.Code != "usage" {
		t.Errorf("--pattern: %+v, want usage (not available)", got)
	}
	if got := delegate(w, gm, flags("99-nada", 2*day)); got.OK || got.Code != "not_found" || got.Exit != 4 {
		t.Errorf("a table with no Mestre: %+v, want exit 4 not_found", got)
	}
	if got := delegate(w, gm, map[string]any{"table": "42-login"}); got.OK || got.Code != "usage" {
		t.Errorf("fields missing: %+v, want usage", got)
	}
}
