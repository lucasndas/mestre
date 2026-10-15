package app_test

import (
	"context"
	"testing"

	"github.com/lucasndas/mestre/internal/app"
	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/hook"
	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/providers"
)

// runtimeFake is the characters of a machine for the wiring test.
type runtimeFake struct{ events []providers.EventKind }

func (runtimeFake) Env(context.Context, string) (classes.Env, error)     { return classes.Env{}, nil }
func (runtimeFake) WaitingApproval(context.Context, string, int64) error { return nil }
func (runtimeFake) Context(context.Context, providers.Event) (string, error) {
	return "layer", nil
}
func (r *runtimeFake) OnHook(ev providers.Event, _ hook.Answer) {
	r.events = append(r.events, ev.Event)
}

type noIdentifier struct{ localchan.Identifier }

// The hook dispatcher of the engine asks the characters of the machine for
// the environment, the layer and the approval wait, and tells them every event
// it answered, without taking the sink somebody else had.
func TestUseCharactersWiresTheRuntimeIntoTheHookDispatcher(t *testing.T) {
	t.Parallel()
	rt := &runtimeFake{}
	var other []providers.EventKind
	deps := app.Deps{OnHookEvent: func(ev providers.Event, _ hook.Answer) { other = append(other, ev.Event) }}
	id := noIdentifier{}
	deps.UseCharacters(rt, id)
	if deps.Characters == nil || deps.States == nil || deps.Contexts == nil || deps.Identifier != localchan.Identifier(id) {
		t.Fatalf("deps = %+v", deps)
	}
	if text, err := deps.Contexts.Context(context.Background(), providers.Event{}); err != nil || text != "layer" {
		t.Errorf("context = %q, %v", text, err)
	}
	deps.OnHookEvent(providers.Event{Event: providers.EventTurnStart}, hook.Answer{})
	if len(rt.events) != 1 || rt.events[0] != providers.EventTurnStart || len(other) != 1 {
		t.Errorf("events: runtime %v, previous sink %v", rt.events, other)
	}
	// Without an identifier the one of the Deps stays.
	deps.UseCharacters(rt, nil)
	if deps.Identifier != localchan.Identifier(id) {
		t.Error("the identifier was replaced by nothing")
	}
}
