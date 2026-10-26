package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/protocol"
	"github.com/lucasndas/mestre/internal/sessionzero"
	"github.com/lucasndas/mestre/pkg/clock"
)

// The characters of a Tower (docs/conectividade/mensagens.md): the Citadel
// renders the request of the session zero and sends it, the Runtime of the
// Tower prepares the files, starts the terminal and reports every transition
// back. Everything the Tower accepts comes from the Citadel of the epoch and
// concerns this machine.

// fromCitadel says whether the sender is the Citadel this machine follows.
func (e *Engine) fromCitadel(p protocol.Peer) bool {
	return p.Role == protocol.RoleMachine && p.ID == e.epochs.Citadel()
}

// towerCharacterError turns the failure of the Runtime into the refusal the
// Citadel reads: a prompt the agent has open and a busy terminal keep their
// own codes, the rest is a prerequisite that was not met (not retried by the
// outbox: the Citadel decides).
func towerCharacterError(op string, err error) error {
	switch {
	case errors.Is(err, characters.ErrPermissionPrompt):
		return protocol.NewError(protocol.CodeBusyTerminal, "%s: %s: %v", op, permissionPromptMark, err)
	case errors.Is(err, characters.ErrBusy):
		return protocol.NewError(protocol.CodeBusyTerminal, "%s: %v", op, err)
	}
	return protocol.NewError(protocol.CodeMissingPrerequisite, "%s: %v", op, err)
}

// permissionPromptMark tells, in a busy-terminal refusal, that the terminal
// waits for a decision the approval flow takes, never keys.
const permissionPromptMark = "permission_prompt"

// registerTowerCharacters puts in the router of a Tower the handlers of the
// commands of the Citadel for its characters.
func (e *Engine) registerTowerCharacters(router *protocol.Router) {
	guard := func(typ string, f func(ctx context.Context, rt *characters.Runtime, m *protocol.Message) (*protocol.Reply, error)) {
		router.HandleFunc(typ, func(ctx context.Context, m *protocol.Message) (*protocol.Reply, error) {
			if !e.fromCitadel(m.Peer) {
				return nil, protocol.NewError(protocol.CodeForbidden, "%s: only the Citadel gives this command", typ)
			}
			if e.rt == nil {
				return nil, protocol.NewError(protocol.CodeUnavailable, "%s: %v", typ, errTerminalsNotWired)
			}
			return f(ctx, e.rt.rt, m)
		})
	}
	guard(protocol.TypeCharacterStart, func(ctx context.Context, rt *characters.Runtime, m *protocol.Message) (*protocol.Reply, error) {
		p, ok := m.Payload.(*protocol.CharacterStart)
		if !ok {
			return nil, protocol.NewError(protocol.CodeInvalidPayload, "character.start: %T", m.Payload)
		}
		var req sessionzero.CharacterRequest
		raw, err := json.Marshal(p.Request)
		if err == nil {
			err = json.Unmarshal(raw, &req)
		}
		if err != nil {
			return nil, protocol.NewError(protocol.CodeInvalidPayload, "character.start: the request is not readable: %v", err)
		}
		// A request for another machine, or for a character other than the
		// one it names, is not run: the files it writes are in this worktree.
		if req.Machine != e.id.MachineID || req.ID != p.Character {
			return nil, protocol.NewError(protocol.CodeForbidden, "character.start: the request is for %q as %q, this machine is %q", req.Machine, req.ID, e.id.MachineID)
		}
		if err := e.towerWorktree(req.Dir); err != nil {
			return nil, protocol.NewError(protocol.CodeForbidden, "character.start: %v", err)
		}
		hooksHash, err := e.towerClassHooksReady(ctx, req)
		if err != nil {
			return nil, protocol.NewError(protocol.CodeMissingPrerequisite, "character.start: %v", err)
		}
		// the Citadel gives up at towerStartWait: the Tower gives up first, so
		// what the Citadel took for a failure is not left running here
		sctx, cancel := clock.WithTimeout(ctx, e.clk, towerStartWait-10*time.Second)
		defer cancel()
		if req.ProviderKey != nil && e.deps.Masker != nil {
			e.deps.Masker.Register(req.ProviderKey.Value) // what the agent echoes of it is hidden in the logs here too
		}
		_, err = rt.Start(sctx, characters.StartCommand{Character: p.Character, Terminal: p.TerminalID, Kind: characters.Kind(p.Kind), Request: req,
			Resumed: p.Resumed, ResumeMessage: p.ResumeMessage})
		if err != nil {
			return nil, towerCharacterError("character.start", err)
		}
		// the Tower says which hooks it applies: the Citadel starts nothing it cannot confirm
		return &protocol.Reply{Type: protocol.TypeCharacterStart, InAck: true, Payload: protocol.CharacterStartResult{ClassHooks: hooksHash}}, nil
	})
	guard(protocol.TypeCharacterStop, func(ctx context.Context, rt *characters.Runtime, m *protocol.Message) (*protocol.Reply, error) {
		p, ok := m.Payload.(*protocol.CharacterStop)
		if !ok {
			return nil, protocol.NewError(protocol.CodeInvalidPayload, "character.stop: %T", m.Payload)
		}
		if err := rt.Stop(ctx, characters.StopCommand{Character: p.Character, Final: p.Final, Reason: p.Reason, Abandon: p.Abandon, Terminal: p.TerminalID}); err != nil {
			return nil, towerCharacterError("character.stop", err)
		}
		return nil, nil
	})
	guard(protocol.TypeInboxDeliver, func(ctx context.Context, rt *characters.Runtime, m *protocol.Message) (*protocol.Reply, error) {
		p, ok := m.Payload.(*protocol.InboxDeliver)
		if !ok {
			return nil, protocol.NewError(protocol.CodeInvalidPayload, "inbox.deliver: %T", m.Payload)
		}
		res, err := rt.DeliverInbox(ctx, *p)
		if err != nil {
			return nil, towerCharacterError("inbox.deliver", err)
		}
		return &protocol.Reply{Type: protocol.TypeInboxResult, Payload: res, InAck: true}, nil
	})
	guard(protocol.TypeTerminalInput, func(ctx context.Context, rt *characters.Runtime, m *protocol.Message) (*protocol.Reply, error) {
		p, ok := m.Payload.(*protocol.TerminalInput)
		if !ok || p.Mode != protocol.InputRaw {
			return nil, protocol.NewError(protocol.CodeInvalidPayload, "terminal.input: only the raw input of a terminal is typed here")
		}
		id, ok := rt.AgentOf(p.TerminalID)
		if !ok {
			return nil, protocol.NewError(protocol.CodeNotFound, "terminal.input: no terminal %s on this machine", p.TerminalID)
		}
		if err := rt.Input(ctx, id, []byte(p.Data)); err != nil {
			return nil, towerCharacterError("terminal.input", err)
		}
		return nil, nil
	})
	guard(protocol.TypeRitualPreRun, func(ctx context.Context, rt *characters.Runtime, m *protocol.Message) (*protocol.Reply, error) {
		p, ok := m.Payload.(*protocol.RitualPreRun)
		if !ok {
			return nil, protocol.NewError(protocol.CodeInvalidPayload, "ritual.prerun: %T", m.Payload)
		}
		timeout := preRunTimeout(time.Duration(p.TimeoutSeconds) * time.Second)
		ctx, cancel := clock.WithTimeout(ctx, e.clk, timeout+10*time.Second)
		defer cancel()
		out, err := rt.PreRun(ctx, p.Character, p.Command, timeout, p.MaxOutput)
		if err != nil {
			return nil, towerCharacterError("ritual.prerun", err)
		}
		return &protocol.Reply{Type: protocol.TypeRitualPreRun, InAck: true, Payload: protocol.RitualPreRunResult{
			Output: out.Output, ExitCode: out.ExitCode, TimedOut: out.TimedOut, Stderr: out.Stderr}}, nil
	})
	guard(protocol.TypeTerminalScreen, func(ctx context.Context, rt *characters.Runtime, m *protocol.Message) (*protocol.Reply, error) {
		p, ok := m.Payload.(*protocol.TerminalScreen)
		if !ok {
			return nil, protocol.NewError(protocol.CodeInvalidPayload, "terminal.screen: %T", m.Payload)
		}
		id, ok := rt.AgentOf(p.TerminalID)
		if !ok {
			return nil, protocol.NewError(protocol.CodeNotFound, "terminal.screen: no terminal %s on this machine", p.TerminalID)
		}
		sc, err := rt.ScreenLines(ctx, id, p.Lines)
		if err != nil {
			return nil, towerCharacterError("terminal.screen", err)
		}
		cursor, err := json.Marshal(sc.Row)
		if err != nil {
			return nil, protocol.NewError(protocol.CodeInternal, "terminal.screen: %v", err)
		}
		return &protocol.Reply{Type: protocol.TypeTerminalScreenResult, InAck: true, Payload: protocol.TerminalScreenResult{
			Text: sc.Text, Cursor: protocol.Object{"row": cursor}, CapturedAt: protocol.Timestamp{Time: e.clk.Now().UTC()}, History: sc.History,
		}}, nil
	})
}

// towerLink is the connection of this machine to the Citadel, when it is a
// Tower now.
func (e *Engine) towerLink() *protocol.Link {
	e.mu.Lock()
	run := e.tow
	e.mu.Unlock()
	if run == nil || run.client == nil {
		return nil
	}
	return run.client.Link()
}

// towerSend sends to the Citadel what a character of this machine tells it. A
// message that asks for an ack waits in the outbox while the Citadel is away
// and goes at the next connection, where the Citadel applies it once.
func (e *Engine) towerSend(ctx context.Context, link *protocol.Link, typ string, payload any) error {
	_, err := link.Send(ctx, protocol.Outgoing{Type: typ, Payload: payload})
	return err
}

// reportPayload is a report of a character as terminal.status.
func reportPayload(r characters.Report) protocol.TerminalStatus {
	at := protocol.Timestamp{Time: r.At.UTC()}
	return protocol.TerminalStatus{
		TerminalID: r.Terminal, State: string(r.State), Reason: r.Reason, SessionID: r.Session,
		AgentID: r.Character, Seq: r.Seq, At: &at, PTYPid: r.PTY.PID, PTYStarted: r.PTY.Started, PTYVersion: r.PTY.Version,
		Reduced: r.Reduced, Resync: r.Resync,
	}
}

// resyncCharacters tells the Citadel the state of every terminal of this
// machine, at each connection: what changed while no Citadel was there is
// taken as the machine, which is the truth about the process, has it.
func (e *Engine) resyncCharacters(ctx context.Context, link *protocol.Link) {
	if e.rt == nil {
		return
	}
	// one message, so the Citadel sees which characters the Tower does not have; only
	// on this connection: a picture that waits in an outbox is no longer the truth
	at := protocol.Timestamp{Time: e.clk.Now().UTC()} // before the picture: what changes meanwhile is not judged by it
	live := e.rt.rt.Live()
	if live == nil {
		live = []protocol.TerminalState{}
	}
	cctx, cancel := clock.WithTimeout(ctx, e.clk, towerCallWait)
	defer cancel()
	res, err := link.Call(cctx, protocol.Outgoing{Type: protocol.TypeCharacterResync, Payload: protocol.CharacterResync{Terminals: live, At: &at}, Transient: true})
	switch {
	case err != nil:
		e.log.Warn("app: the terminals of this machine were not sent to the Citadel", "err", err)
		return
	case res.Status == protocol.AckError:
		e.log.Warn("app: the Citadel refused the terminals of this machine", "err", res.Error)
		return
	}
	// what the Citadel does not have there is ended here: a terminal of a start it gave up on
	var out protocol.CharacterResyncResult
	if len(res.Result) == 0 || json.Unmarshal(res.Result, &out) != nil {
		return
	}
	for _, id := range out.End {
		if err := e.rt.rt.Stop(ctx, characters.StopCommand{Character: id, Abandon: true, Reason: "the Citadel does not have it"}); err != nil {
			e.log.Warn("app: a terminal the Citadel does not have was not ended", "id", id, "err", err)
		}
	}
}

// noticeKinds are the notices a Tower may send about a character of its own:
// the supervision's, to the master of the table.
var noticeKinds = map[characters.NoticeKind]bool{
	characters.NoticeStuck: true, characters.NoticeInterrupted: true, characters.NoticeBlockedInput: true,
	characters.NoticeUnconfirmed: true, characters.NoticeStartFailed: true,
}

var errTowerNotice = errors.New("a Tower tells only the supervision of its characters to the master of the table")

func towerNotice(n characters.Notice) (protocol.CharacterNotice, error) {
	if n.To.Kind != characters.ToTableMaster || !noticeKinds[n.Kind] || n.Character == "" {
		return protocol.CharacterNotice{}, fmt.Errorf("%w (%s to %s)", errTowerNotice, n.Kind, n.To.Kind)
	}
	return protocol.CharacterNotice{Kind: string(n.Kind), Character: n.Character, Text: n.Text, Screen: n.Screen}, nil
}

// towerWorktree refuses a worktree outside the folder of the workspaces of
// this machine: the files of the session zero are written there.
func (e *Engine) towerWorktree(dir string) error {
	root, err := filepath.Abs(e.paths.Workspaces)
	if e.paths.Workspaces == "" || err != nil {
		return errors.New("this machine keeps no folder of workspaces")
	}
	// links count: the worktree is judged by where its nearest existing folder really is
	real, err := resolveExisting(filepath.Clean(dir))
	if err != nil {
		return err
	}
	// the root gets the same resolution as the worktree: when it does not exist
	// yet, only its existing prefix is resolved (short names of Windows included)
	if r, err := resolveExisting(root); err == nil {
		root = r
	}
	rel, err := filepath.Rel(root, real)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("the worktree %q is not in the workspaces of this machine", dir)
	}
	return nil
}

// resolveExisting resolves the links of the longest existing prefix of a path
// and puts the rest back.
func resolveExisting(p string) (string, error) {
	rest := ""
	for cur := p; ; {
		if r, err := filepath.EvalSymlinks(cur); err == nil {
			return filepath.Join(r, rest), nil
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", fmt.Errorf("the worktree %q has no folder that exists", p)
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
}
