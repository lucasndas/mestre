package app

import (
	"encoding/json"
	"testing"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/orchestration/chain"
)

// The live target of a request is read from the object itself; what the engine
// cannot read, or does not know, is no target (the flow then approves nothing).
func TestTheLiveTargetOfADestructiveRequestIsRefusedForWhatTheEngineCannotRead(t *testing.T) {
	t.Parallel()
	b := destructiveBroker{}
	req := func(typ classes.RequestType, cmd, object string) chain.Request {
		return chain.Request{Type: typ, Target: chain.DestructiveTarget{Command: cmd, Object: object, Version: "1"}.JSON()}
	}
	for name, r := range map[string]chain.Request{
		"another type":      req(classes.RequestPR, "note delete", "a/b/p1"),
		"unknown command":   req(classes.RequestDestructive, "rm -rf", "x"),
		"no scrolls":        req(classes.RequestDestructive, "note delete", "a/b/p1"),
		"no rituals":        req(classes.RequestDestructive, "ritual delete", "r1"),
		"table not a table": req(classes.RequestDestructive, "table close --cancel", "nothing"),
		"class not a class": req(classes.RequestDestructive, "class delete", "a/b"),
		"no target":         {Type: classes.RequestDestructive, Target: json.RawMessage(`{`)},
	} {
		if got, err := b.Live(t.Context(), r); err == nil {
			t.Errorf("%s: a live target was given: %s", name, got)
		}
	}
}

func TestTheLiveTargetNeedsTheOrchestrationOfThePeriod(t *testing.T) {
	t.Parallel()
	if _, err := (lateTargets{&stdRun{}}).Current(t.Context(), chain.Request{Type: classes.RequestDestructive}); err == nil {
		t.Error("a target was read with no orchestration")
	}
	var orc *Orchestration
	if _, err := orc.DestructiveTarget(t.Context(), chain.Request{}); err == nil {
		t.Error("a target was read from no orchestration")
	}
}

func TestAClassIsAskedAboutByItsWorkspaceScopeTableAndName(t *testing.T) {
	t.Parallel()
	k := classes.ClassKey{Scope: classes.ClassTable, Table: "42-login", Name: "dev"}
	if got := classObject("loja", k); got != "loja/"+string(k.Scope)+"/42-login/dev" {
		t.Errorf("classObject = %q", got)
	}
}
