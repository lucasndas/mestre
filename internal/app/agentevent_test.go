package app

import (
	"context"
	"errors"
	"testing"

	"github.com/lucasndas/mestre/internal/hook"
	"github.com/lucasndas/mestre/internal/protocol"
	"github.com/lucasndas/mestre/internal/providers"
)

// CA-CIDADELA-14
func TestAnAgentEventIsAcceptedOnlyFromTheMachineWhereTheCharacterRuns(t *testing.T) {
	t.Parallel()
	where := map[string]string{"dev-1": "torre-1", "dev-2": "torre-2"}
	send := func(deps Deps, from, agent string) error {
		e := &Engine{deps: deps}
		payload, err := agentEventPayload(providers.Event{AgentID: agent, Event: providers.EventTurnEnd}, hook.Answer{}, nil)
		if err != nil {
			t.Fatal(err)
		}
		_, err = e.handleAgentEvent(context.Background(), &protocol.Message{Payload: &payload, Peer: protocol.Peer{ID: from}})
		return err
	}
	var got []string
	deps := Deps{
		OnAgentEvent: func(machine string, ev providers.Event, _ hook.Answer) { got = append(got, machine+"/"+ev.AgentID) },
		AgentMachine: func(_ context.Context, id string) (string, bool) { m, ok := where[id]; return m, ok },
	}
	if err := send(deps, "torre-1", "dev-1"); err != nil {
		t.Fatalf("the event of the machine where the character runs: %v", err)
	}
	for _, c := range []struct{ from, agent string }{{"torre-1", "dev-2"}, {"torre-2", "dev-1"}, {"torre-1", "unknown"}, {"torre-1", ""}} {
		err := send(deps, c.from, c.agent)
		var perr *protocol.Error
		if !errors.As(err, &perr) || perr.Code != protocol.CodeForbidden {
			t.Errorf("%s says an event of %q: %v; want forbidden", c.from, c.agent, err)
		}
	}
	if len(got) != 1 || got[0] != "torre-1/dev-1" {
		t.Fatalf("events delivered: %v; want only the first", got)
	}
	// a Citadel that does not know where the characters run refuses everything
	deps.AgentMachine = nil
	var perr *protocol.Error
	if err := send(deps, "torre-1", "dev-1"); !errors.As(err, &perr) || perr.Code != protocol.CodeForbidden {
		t.Fatalf("without a registry: %v; want forbidden", err)
	}
}
