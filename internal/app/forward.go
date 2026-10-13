package app

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/protocol"
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

// Forward implements localchan.Forwarder.
func (f forwarder) Forward(ctx context.Context, c *localchan.Call) localchan.Response {
	e := f.e
	e.mu.Lock()
	failover, cit, tow := e.failover, e.cit, e.tow
	e.mu.Unlock()
	switch {
	case failover:
		return errFailover
	case cit != nil:
		if !cit.fence.Acting() {
			return errUnavailable
		}
		return e.local.Forward(ctx, c)
	case tow != nil:
		return e.callCitadel(ctx, tow.client.Link(), c)
	}
	return errUnavailable
}

// cliCall is the payload of cli.call: the request, plus the verified caller.
type cliCall struct {
	localchan.Request
	Caller cliCaller `json:"caller"`
}

type cliCaller struct {
	AgentID    string `json:"agent_id"`
	TerminalID string `json:"terminal_id"`
	MachineID  string `json:"machine_id"`
}

// callCitadel sends the call to the Citadel and waits for its cli.result. It
// goes only on the connection open now: a request that needs an answer now
// must never run later, after the CLI was told the Citadel is away.
func (e *Engine) callCitadel(ctx context.Context, link *protocol.Link, c *localchan.Call) localchan.Response {
	sess := link.Session()
	if sess == nil {
		return errUnavailable
	}
	raw, err := json.Marshal(cliCall{Request: c.Request, Caller: cliCaller{AgentID: c.Caller.AgentID, TerminalID: c.Caller.TerminalID, MachineID: e.id.MachineID}})
	if err != nil {
		return localchan.Fail(localchan.CodeInternal, err.Error(), "")
	}
	var obj protocol.CLICall
	if err := json.Unmarshal(raw, &obj); err != nil {
		return localchan.Fail(localchan.CodeInternal, err.Error(), "")
	}
	id := protocol.NewIDGen(e.clk).NewString()
	ch, release := e.results.wait(id)
	defer release()
	if _, err := link.Send(ctx, protocol.Outgoing{Type: protocol.TypeCLICall, Payload: obj, ID: id, Transient: true}); err != nil {
		return errUnavailable
	}
	select {
	case r := <-ch:
		return r
	case <-sess.Done():
		return errUnavailable
	case <-ctx.Done():
		return localchan.Fail(localchan.CodeTimeout, "the Citadel did not answer in time", "")
	}
}

// handleCLICall is cli.call on the Citadel: the request runs with the
// commands of this Citadel and the answer goes back as cli.result. The
// handler only starts it: a command may take long.
func (e *Engine) handleCLICall(ctx context.Context, m *protocol.Message) (*protocol.Reply, error) {
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
	go func() {
		bg := context.WithoutCancel(ctx)
		resp := e.local.Forward(bg, &localchan.Call{Request: call.Request, Caller: localchan.Caller{
			Identified: true, AgentID: call.Caller.AgentID, TerminalID: call.Caller.TerminalID,
		}})
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
	}()
	return nil, nil
}

// results are the cli.call waiting for their cli.result on a Tower.
type results struct {
	mu      sync.Mutex
	waiting map[string]chan localchan.Response
}

func newResults() *results { return &results{waiting: map[string]chan localchan.Response{}} }

func (r *results) wait(id string) (<-chan localchan.Response, func()) {
	ch := make(chan localchan.Response, 1)
	r.mu.Lock()
	r.waiting[id] = ch
	r.mu.Unlock()
	return ch, func() {
		r.mu.Lock()
		delete(r.waiting, id)
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
		return nil, nil // the partials of a long command arrive with the commands of milestone 4
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
