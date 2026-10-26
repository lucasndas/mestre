package app

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/mestre-ia/mestre/internal/cli"
	"github.com/mestre-ia/mestre/internal/localchan"
	"github.com/mestre-ia/mestre/internal/protocol"
)

// forwarder is the Forwarder of the local channel: on the Citadel the call is
// served here, by the same code that serves the cli.call of the Towers; on a
// Tower it goes to the Citadel as cli.call (docs/arquitetura/canal-local.md#encaminhamento-à-cidadela).
// With the Citadel away or suspended the answer is citadel_unavailable, and
// citadel_in_failover while this machine takes the command.
type forwarder struct{ e *Engine }

var _ localchan.Forwarder = forwarder{}

// Errors of the forwarding, as the CLI shows them.
var (
	errUnavailable = localchan.Fail(localchan.CodeCitadelUnavailable, "the Citadel is not available, the request was not made",
		"the Owner is already warned; repeat later")
	errFailover = localchan.Fail(localchan.CodeCitadelInFailover, "another machine is taking the place of the Citadel, the request was not made",
		"repeat in a few minutes")
)

var _ cli.QueueingForwarder = forwarder{}

// Forward implements localchan.Forwarder.
func (f forwarder) Forward(ctx context.Context, c *localchan.Call) localchan.Response {
	return f.forward(ctx, c, false)
}

// ForwardOrQueue implements cli.QueueingForwarder, for the commands that need
// no answer now (`done`): with the Citadel away, suspended or handing over,
// the call goes to the outbox of this Tower and the answer is Queued, with
// the id of the request as the op_id. The Citadel applies it once when it
// acts (the id is the idempotency key of the message) and hands the result
// to OnQueuedResult. The call of a Citadel that is itself suspended is not
// queued: it has no outbox for its own calls, and answers citadel_unavailable.
func (f forwarder) ForwardOrQueue(ctx context.Context, c *localchan.Call) localchan.Response {
	return f.forward(ctx, c, true)
}

func (f forwarder) forward(ctx context.Context, c *localchan.Call, queue bool) localchan.Response {
	e := f.e
	e.mu.Lock()
	failover, cit, tow := e.failover, e.cit, e.tow
	e.mu.Unlock()
	switch {
	case failover:
		return errFailover
	case cit != nil:
		if refusal, ok := cit.cliRefusal(); !ok {
			return refusal
		}
		if !cit.gate.enter() {
			return errFailover
		}
		defer cit.gate.leave()
		if refusal, ok := e.lockRefusal(ctx, c.Request.Cmd); !ok {
			return refusal
		}
		return e.local.Forward(ctx, c)
	case tow != nil:
		link := tow.client.Link()
		if !queue {
			resp, _ := e.callCitadel(ctx, link, c, "", false)
			return resp
		}
		// The id of the request is the id of the message, so a call that
		// went and lost its answer, and the same call waiting in the outbox,
		// are one message for the Citadel, which runs it once.
		id := c.Request.ID
		if _, err := protocol.ParseULID(id); err != nil {
			id = ""
		}
		resp, answered := e.callCitadel(ctx, link, c, id, true)
		if resp.OK || resp.Error == nil || !awayCode(resp.Error.Code) {
			return resp
		}
		if answered {
			// The Citadel refused it, so it did not run it, but it kept the id
			// of the message: the one that waits needs another.
			id = ""
		}
		return e.queueCall(ctx, link, c, id, resp)
	}
	return errUnavailable
}

// awayCode says whether a refusal means the Citadel did not run the call and
// will take it later.
func awayCode(code string) bool {
	return code == localchan.CodeCitadelUnavailable || code == localchan.CodeCitadelInFailover
}

// queueCall puts the call in the outbox of the Tower and answers Queued. If
// the outbox cannot take it, the answer stays the refusal of the Citadel.
func (e *Engine) queueCall(ctx context.Context, link *protocol.Link, c *localchan.Call, id string, refusal localchan.Response) localchan.Response {
	obj, err := e.cliCallObject(c, true, false)
	if err != nil {
		return refusal
	}
	id, err = link.Send(ctx, protocol.Outgoing{Type: protocol.TypeCLICall, Payload: obj, ID: id})
	if err != nil {
		e.log.Warn("app: queue a call of the CLI", "cmd", c.Request.Cmd, "err", err)
		return refusal
	}
	return cli.Queued(id)
}

// cliCallObject is the payload of a cli.call for the call c.
func (e *Engine) cliCallObject(c *localchan.Call, queued, mayQueue bool) (protocol.CLICall, error) {
	raw, err := json.Marshal(cliCall{
		Request: c.Request, Queued: queued, MayQueue: mayQueue,
		Caller: cliCaller{AgentID: c.Caller.AgentID, TerminalID: c.Caller.TerminalID, MachineID: e.id.MachineID},
	})
	if err != nil {
		return nil, err
	}
	var obj protocol.CLICall
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, err
	}
	return obj, nil
}

// cliCall is the payload of cli.call: the request, plus the verified caller.
type cliCall struct {
	localchan.Request
	Caller cliCaller `json:"caller"`
	// Queued is set on a call that waited in the outbox of the Tower: the
	// CLI was told "queued", so the result goes to the inbox of the caller.
	Queued bool `json:"queued,omitempty"`
	// MayQueue is set on the first, direct send of a call that goes to the
	// outbox if its answer does not come back: the Citadel keeps the result
	// of the run for the duplicate that may follow (ranCalls).
	MayQueue bool `json:"may_queue,omitempty"`
}

type cliCaller struct {
	AgentID    string `json:"agent_id"`
	TerminalID string `json:"terminal_id"`
	MachineID  string `json:"machine_id"`
}

// callCitadel sends the call to the Citadel and waits for its cli.result. It
// goes only on the connection open now: a request that needs an answer now
// must never run later, after the CLI was told the Citadel is away. id is the
// id of the message (empty: a new one). answered says that the response is the
// Citadel's cli.result, not a failure to reach it.
func (e *Engine) callCitadel(ctx context.Context, link *protocol.Link, c *localchan.Call, id string, mayQueue bool) (resp localchan.Response, answered bool) {
	sess := link.Session()
	if sess == nil {
		return errUnavailable, false
	}
	obj, err := e.cliCallObject(c, false, mayQueue)
	if err != nil {
		return localchan.Fail(localchan.CodeInternal, err.Error(), ""), false
	}
	if id == "" {
		id = protocol.NewIDGen(e.clk).NewString()
	}
	var sink func(json.RawMessage)
	if !mayQueue { // a call that may be queued has its answer from the queue, not from here
		sink = func(data json.RawMessage) { c.Partial(data) }
	}
	ch, release := e.results.wait(id, sink)
	defer release()
	if _, err := link.Send(ctx, protocol.Outgoing{Type: protocol.TypeCLICall, Payload: obj, ID: id, Transient: true}); err != nil {
		return errUnavailable, false
	}
	select {
	case r := <-ch:
		return r, true
	case <-sess.Done():
		return errUnavailable, false
	case <-ctx.Done():
		return localchan.Fail(localchan.CodeTimeout, "the Citadel did not answer in time", ""), false
	}
}

// cliRefusal says whether the Citadel of this period serves a call of the
// CLI now, and the answer when it does not: citadel_in_failover while it
// hands the command over, citadel_unavailable while it does not act
// (suspended at the start, or stepping down).
func (r *citadelRun) cliRefusal() (localchan.Response, bool) {
	switch {
	case r.gate.isDraining():
		return errFailover, false
	case !r.fence.Acting():
		return errUnavailable, false
	}
	return localchan.Response{}, true
}

// handleCLICall is cli.call on the Citadel: the request runs with the
// commands of this Citadel and the answer goes back as cli.result. The
// handler only starts it: a command may take long. It passes the same fence
// as a call made on this machine: a Citadel that does not act answers
// citadel_unavailable (citadel_in_failover while it hands over), and a
// command runs inside the gate of the handover until it ends.
func (e *Engine) handleCLICall(run *citadelRun) protocol.HandlerFunc {
	return func(ctx context.Context, m *protocol.Message) (*protocol.Reply, error) {
		raw, err := json.Marshal(m.Payload)
		if err != nil {
			return nil, protocol.NewError(protocol.CodeInvalidPayload, "%v", err)
		}
		var call cliCall
		if err := json.Unmarshal(raw, &call); err != nil {
			return nil, protocol.NewError(protocol.CodeInvalidPayload, "%v", err)
		}
		if call.Caller.MachineID != m.Peer.ID {
			return nil, protocol.NewError(protocol.CodeForbidden, "the caller is of %s, the message of %s", call.Caller.MachineID, m.Peer.ID)
		}
		link, id := m.Link, m.Env.ID
		bg := context.WithoutCancel(ctx)
		answer := func(resp localchan.Response) {
			resp.V, resp.ID = call.V, call.ID
			b, err := json.Marshal(resp)
			if err != nil {
				return
			}
			var obj protocol.CLIResult
			if err := json.Unmarshal(b, &obj); err != nil {
				return
			}
			if _, err := link.Send(bg, protocol.Outgoing{Type: protocol.TypeCLIResult, Payload: obj, ReplyTo: id, Transient: true}); err != nil && !errors.Is(err, protocol.ErrNotConnected) {
				e.log.Warn("app: cli.result", "err", err)
			}
		}
		// The caller named by a Tower is believed only for a character that runs
		// on that Tower: the call gets the level of the character, so a Tower
		// must not speak for the characters of another machine. The answer is a
		// result, so the CLI that waits for it hears it at once.
		if call.Caller.AgentID != "" {
			if err := e.runsOn(ctx, call.Caller.AgentID, m.Peer.ID); err != nil {
				answer(localchan.Fail(localchan.CodeNotIdentified, "the caller is not a character of this machine: "+err.Error(), ""))
				return nil, nil
			}
		}
		if refusal, ok := run.cliRefusal(); !ok {
			if call.Queued {
				// retryable: the call waited in the outbox of its Tower for
				// this moment, and stays there for the Citadel that acts; when
				// this one acts it closes the Tower, which sends it again
				if !run.fence.Acting() {
					run.waiting(m.Peer.ID)
				}
				return nil, protocol.NewError(protocol.CodeUnavailable, "the Citadel does not act now: %s", refusal.Error.Code)
			}
			answer(refusal)
			return nil, nil
		}
		// the gate of the message is left when the handler returns: the
		// command holds one of its own until it ends
		if !run.gate.enter() {
			if call.Queued {
				return nil, errHandingOver()
			}
			answer(errFailover)
			return nil, nil
		}
		peer := m.Peer.ID
		if call.MayQueue && !call.Queued {
			run.ran.started(peer, id)
		}
		go func() {
			defer run.gate.leave()
			lc := &localchan.Call{Request: call.Request, Caller: localchan.Caller{
				Identified: true, AgentID: call.Caller.AgentID, TerminalID: call.Caller.TerminalID,
			}}
			if !call.Queued && !call.MayQueue {
				// the signs of life of a long command (`ask`: queued, delivered,
				// answering…) go back as they happen; a queued call has nobody
				// waiting at the other end
				lc.SetPartialSink(func(data json.RawMessage) {
					answer(localchan.Response{Partial: true, Data: data})
				})
			}
			resp, ok := e.lockRefusal(bg, call.Cmd)
			if ok {
				resp = e.local.Forward(bg, lc)
			}
			qresp := resp
			qresp.V, qresp.ID = call.V, call.ID
			qr := QueuedResult{OpID: id, AgentID: call.Caller.AgentID, Cmd: call.Cmd, Response: qresp}
			switch {
			case call.Queued:
				e.queuedResult(qr)
			case call.MayQueue:
				// the answer below may be lost: the same message from the
				// outbox then takes this result
				run.ran.finished(peer, id, qr)
			}
			answer(resp)
		}()
		return nil, nil
	}
}

// queuedResult hands the result of a call the CLI was told "queued" to the
// inbox of the caller (OnQueuedResult), or to the log.
func (e *Engine) queuedResult(qr QueuedResult) {
	if e.deps.OnQueuedResult != nil {
		e.deps.OnQueuedResult(qr)
		return
	}
	e.log.Info("app: a queued call of the CLI ran", "op_id", qr.OpID, "cmd", qr.Cmd, "agent_id", qr.AgentID, "exit", qr.Response.ExitCode())
}

// results are the cli.call waiting for their cli.result on a Tower.
type results struct {
	mu       sync.Mutex
	waiting  map[string]chan localchan.Response
	partials map[string]func(json.RawMessage)
}

func newResults() *results {
	return &results{waiting: map[string]chan localchan.Response{}, partials: map[string]func(json.RawMessage){}}
}

// wait registers the call; partial (nil: none) receives the partial responses of
// the command as they come.
func (r *results) wait(id string, partial func(json.RawMessage)) (<-chan localchan.Response, func()) {
	ch := make(chan localchan.Response, 1)
	r.mu.Lock()
	r.waiting[id] = ch
	if partial != nil {
		r.partials[id] = partial
	}
	r.mu.Unlock()
	return ch, func() {
		r.mu.Lock()
		delete(r.waiting, id)
		delete(r.partials, id)
		r.mu.Unlock()
	}
}

// handle is cli.result on a Tower.
func (r *results) handle(_ context.Context, m *protocol.Message) (*protocol.Reply, error) {
	raw, err := json.Marshal(m.Payload)
	if err != nil {
		return nil, protocol.NewError(protocol.CodeInvalidPayload, "%v", err)
	}
	var resp localchan.Response
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, protocol.NewError(protocol.CodeInvalidPayload, "%v", err)
	}
	if resp.Partial {
		// a sign of life of a long command: on to the CLI that waits for it
		r.mu.Lock()
		sink := r.partials[m.Env.ReplyTo]
		r.mu.Unlock()
		if sink != nil {
			sink(resp.Data)
		}
		return nil, nil
	}
	r.mu.Lock()
	ch := r.waiting[m.Env.ReplyTo]
	r.mu.Unlock()
	if ch != nil {
		select {
		case ch <- resp:
		default:
		}
	}
	return nil, nil
}
