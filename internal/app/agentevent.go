package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/lucasndas/mestre/internal/hook"
	"github.com/lucasndas/mestre/internal/logging"
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
	if p := e.period.Load(); p != nil && p.usage != nil && ev.AgentID != "" {
		p.usage.onEvent(ev) // the Citadel takes the use of the characters of this machine
	}
	e.mu.Lock()
	run := e.tow
	e.mu.Unlock()
	if run == nil || run.client == nil || ev.AgentID == "" {
		return
	}
	if run.usage != nil {
		run.usage.Note(ev) // where the transcript of the character is, for its periodic reading
	}
	payload, err := agentEventPayload(ev, ans, e.deps.Masker)
	if err != nil {
		e.log.Warn("app: an event of a character was not sent to the Citadel", "err", err)
		return
	}
	// agent.event asks for an ack: it waits in the outbox while the Citadel is
	// away and goes again at the next connection, where the Citadel applies it
	// once. The hook does not wait for the outbox: the send goes on its own.
	go func() {
		if _, err := run.client.Link().Send(context.Background(), protocol.Outgoing{Type: protocol.TypeAgentEvent, Payload: payload}); err != nil {
			e.log.Warn("app: an event of a character was not queued for the Citadel", "err", err)
		}
	}()
}

// agentEventPayload is the payload of agent.event: the normalized event, without
// the payload of the provider (Raw only serves the recordings of the conformance
// test), and the answer the hook gave.
func agentEventPayload(ev providers.Event, ans hook.Answer, mask *logging.Masker) (protocol.Object, error) {
	ev.Raw = nil
	if mask != nil { // what the event says leaves the machine and waits in its outbox: no secret goes in clear
		ev.Prompt, ev.LastMessage, ev.Error = mask.Mask(ev.Prompt), mask.Mask(ev.LastMessage), mask.Mask(ev.Error)
		if ev.Tool != nil {
			t := *ev.Tool
			t.Command = mask.Mask(t.Command)
			ev.Tool = &t
		}
		if ev.ToolResult != nil {
			r := *ev.ToolResult
			r.Error, r.Output = mask.Mask(r.Error), mask.Mask(r.Output)
			ev.ToolResult = &r
		}
	}
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
func (e *Engine) handleAgentEvent(ctx context.Context, m *protocol.Message) (*protocol.Reply, error) {
	obj, ok := m.Payload.(*protocol.Object)
	if !ok {
		return nil, nil
	}
	period := e.period.Load()
	if e.deps.OnAgentEvent == nil && (period == nil || period.usage == nil) {
		return nil, nil
	}
	var ev providers.Event
	var ans hook.Answer
	if err := json.Unmarshal((*obj)["event"], &ev); err != nil {
		return nil, protocol.NewError(protocol.CodeInvalidPayload, "agent.event: the event is not readable: %v", err)
	}
	_ = json.Unmarshal((*obj)["answer"], &ans)
	// The identity of the sender is the verified one of the connection; the
	// character the event names must run there, or any Tower could forge the
	// events of a character of another (and move its state).
	if ev.AgentID == "" {
		return nil, protocol.NewError(protocol.CodeForbidden, "agent.event: the event names no character")
	}
	if err := e.runsOn(ctx, ev.AgentID, m.Peer.ID); err != nil {
		return nil, protocol.NewError(protocol.CodeForbidden, "agent.event: %v", err)
	}
	if e.deps.OnAgentEvent != nil {
		e.deps.OnAgentEvent(m.Peer.ID, ev, ans)
	}
	if period != nil && period.usage != nil {
		period.usage.onEvent(ev)
	}
	return nil, nil
}

// runsOn says whether the character runs on the machine, by the registry of the
// Citadel (Deps.AgentMachine). Without the registry nothing runs anywhere: the
// answer is no.
func (e *Engine) runsOn(ctx context.Context, agentID, machine string) error {
	where := e.deps.AgentMachine
	if p := e.period.Load(); where == nil && p != nil {
		where = p.agentMachine
	}
	if where == nil {
		return errors.New("this Citadel does not know where the characters run")
	}
	if at, ok := where(ctx, agentID); !ok || at != machine {
		return fmt.Errorf("the character %q does not run on %s", agentID, machine)
	}
	return nil
}
