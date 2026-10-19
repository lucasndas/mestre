package app

import (
	"context"
	"encoding/json"

	"github.com/lucasndas/mestre/internal/hook"
	"github.com/lucasndas/mestre/internal/protocol"
	"github.com/lucasndas/mestre/internal/providers"
)

// onHookEvent is told every event of the hooks of this machine after it was
// answered: the consumers of the machine (the asynchronous hooks of the
// classes) first, and, on a Tower, the Citadel through agent.event.
func (e *Engine) onHookEvent(ev providers.Event, ans hook.Answer) {
	if e.deps.OnHookEvent != nil {
		e.deps.OnHookEvent(ev, ans)
	}
	e.mu.Lock()
	run := e.tow
	e.mu.Unlock()
	if run == nil || run.client == nil || ev.AgentID == "" {
		return
	}
	payload, err := agentEventPayload(ev, ans)
	if err != nil {
		e.log.Warn("app: an event of a character was not sent to the Citadel", "err", err)
		return
	}
	// agent.event asks for an ack: it waits in the outbox while the Citadel is
	// away and goes again at the next connection, where the Citadel applies it once.
	if _, err := run.client.Link().Send(context.Background(), protocol.Outgoing{Type: protocol.TypeAgentEvent, Payload: payload}); err != nil {
		e.log.Warn("app: an event of a character was not queued for the Citadel", "err", err)
	}
}

// agentEventPayload is the payload of agent.event: the normalized event, without
// the payload of the provider (Raw only serves the recordings of the conformance
// test), and the answer the hook gave.
func agentEventPayload(ev providers.Event, ans hook.Answer) (protocol.Object, error) {
	ev.Raw = nil
	evb, err := json.Marshal(ev)
	if err != nil {
		return nil, err
	}
	ab, err := json.Marshal(ans)
	if err != nil {
		return nil, err
	}
	return protocol.Object{"event": evb, "answer": ab}, nil
}

// handleAgentEvent is the handler of agent.event on the Citadel: the event of a
// character of a Tower, from the machine that sent it.
func (e *Engine) handleAgentEvent(_ context.Context, m *protocol.Message) (*protocol.Reply, error) {
	obj, ok := m.Payload.(*protocol.Object)
	if !ok || e.deps.OnAgentEvent == nil {
		return nil, nil
	}
	var ev providers.Event
	var ans hook.Answer
	if err := json.Unmarshal((*obj)["event"], &ev); err != nil {
		return nil, protocol.NewError(protocol.CodeInvalidPayload, "agent.event: the event is not readable: %v", err)
	}
	_ = json.Unmarshal((*obj)["answer"], &ans)
	e.deps.OnAgentEvent(m.Peer.ID, ev, ans)
	return nil, nil
}
