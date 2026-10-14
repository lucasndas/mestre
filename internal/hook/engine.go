package hook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"time"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/providers"
	"github.com/lucasndas/mestre/pkg/clock"
)

// Characters is the terminal registry of the machine (M4): the
// environment of a character that runs here. An error is a character this
// machine does not know.
type Characters interface {
	Env(ctx context.Context, agentID string) (classes.Env, error)
}

// Approvals opens the approval requests (docs/aprovacoes/fluxo.md, M5).
// Open returns the number of the request open for exactly this action of
// the character: the one it opens, or the one already open, since repeating
// the action with the request open does not open another
// (docs/classes/permissoes.md#quando-a-decisão-é-pedir-aprovação).
type Approvals interface {
	Open(ctx context.Context, req ApprovalRequest) (int64, error)
}

// ApprovalRequest is what an approval request is opened for.
type ApprovalRequest struct {
	AgentID, TableID string
	// Action is the exact action the approval becomes an exception for.
	Action classes.Action
	// About is the action and the rule that asked.
	About string
	Event providers.Event
}

// States records the state of a character
// (docs/orquestracao/esperas-e-caixa-de-entrada.md, M4).
type States interface {
	// WaitingPermission puts the character in "aguardando permissão" for
	// the request.
	WaitingPermission(ctx context.Context, agentID string, request int64) error
}

// Contexts gives the context of a session or of a turn (the Mestre layer
// and the decrees, M4 and M5).
type Contexts interface {
	Context(ctx context.Context, ev providers.Event) (string, error)
}

// ClassHooks gives the synchronous hooks of the class of the character for
// an event, already for this system and with their time limits cut to 2 s
// below the provider's (docs/classes/classes.md#hooks-extras-da-classe).
type ClassHooks interface {
	Sync(ctx context.Context, ev providers.Event) ([]ClassHook, error)
}

// Engine is the engine side of the dispatcher: the handler of `cmd: "hook"`
// on the local channel. It decides for the character the server verified:
// a call whose event names another character, or that comes from no
// terminal of it, is denied.
type Engine struct {
	Clock clock.Clock
	// Rules are the effective rules of the characters (classes.Store) and
	// Ledger the record of the exceptions used (the same Store).
	Rules  classes.RulesSource
	Ledger classes.ExceptionLedger
	// Characters, Approvals, States, Contexts and ClassHooks are the parts
	// of later milestones; nil: no character is known (every tool is
	// denied), every ask is denied, no state is kept, no context, no class
	// hook.
	Characters Characters
	Approvals  Approvals
	States     States
	Contexts   Contexts
	ClassHooks ClassHooks
	// Agents is agentes/: the engine writes env.json of each character it
	// decides for there, for the degraded mode. Empty writes none.
	Agents string
	// OnEvent is told every event after it is answered, with the answer;
	// it must not block (the engine processes the informative events
	// afterwards).
	OnEvent func(providers.Event, Answer)
	Logger  *slog.Logger

	mu   sync.Mutex
	envs map[string]CharacterEnv
}

var _ localchan.Handler = (*Engine)(nil)

// Handle serves one hook call.
func (e *Engine) Handle(ctx context.Context, c *localchan.Call) localchan.Response {
	if len(c.Request.Args) != 1 {
		return localchan.Fail(localchan.CodeBadRequest, "hook takes one argument, the normalized event", "")
	}
	var ev providers.Event
	if err := json.Unmarshal(c.Request.Args[0], &ev); err != nil {
		return localchan.Fail(localchan.CodeBadRequest, "the event does not decode: "+err.Error(), "")
	}
	if ev.Version != providers.EventVersion {
		return localchan.Fail(localchan.CodeBadRequest, fmt.Sprintf("event version %d", ev.Version), "update mestre")
	}
	var ans Answer
	switch {
	case !c.Caller.Identified || c.Caller.AgentID == "" || c.Caller.AgentID != ev.AgentID:
		ans = Answer{Verdict: VerdictDeny, Reason: "o chamador não foi identificado como processo de um terminal deste personagem"}
		e.log().Warn("hook: a call not identified as the character's", "agent", ev.AgentID, "pid", c.Caller.PID)
	case ev.Event == providers.EventPreTool || ev.Event == providers.EventPermissionRequest:
		ans = e.decide(ctx, ev)
	case ev.Event == providers.EventNone && ev.Error != "":
		e.log().Warn("hook: unknown native event, answered neutral", "agent", ev.AgentID, "provider", ev.Provider, "native_event", ev.NativeEvent)
		ans = Answer{Verdict: VerdictNeutral}
	default:
		ans = e.inform(ctx, ev)
	}
	if e.OnEvent != nil {
		e.OnEvent(ev, ans)
	}
	return localchan.OK(ans)
}

func (e *Engine) log() *slog.Logger {
	if e.Logger == nil {
		return slog.New(slog.DiscardHandler)
	}
	return e.Logger
}

// decide runs the permission pipeline for the character
// (docs/classes/permissoes.md#pipeline-de-decisão).
func (e *Engine) decide(ctx context.Context, ev providers.Event) Answer {
	p := classes.Policy{Source: e.rules(), Env: e.env}
	d := p.Decide(ctx, ev)
	var ans Answer
	switch d.Verdict {
	case classes.Allow:
		ans = Answer{Verdict: VerdictAllow}
		if d.ClassHooks {
			hooks, err := e.classHooks(ctx, ev)
			if err != nil {
				// A blocking hook may be among them: blocked, as on any doubt.
				ans = Answer{Verdict: VerdictDeny, Reason: "os hooks da classe não puderam ser lidos; tente de novo"}
				break
			}
			ans.ClassHooks = hooks
		}
	case classes.Ask:
		ans = e.ask(ctx, ev, d)
	default:
		ans = Answer{Verdict: VerdictDeny, Reason: d.Reason}
	}
	l := e.log().With("agent", ev.AgentID, "event", ev.Event, "verdict", ans.Verdict, "layer", d.Layer)
	if ev.Tool != nil {
		l = l.With("tool", ev.Tool.NativeName, "kind", ev.Tool.Kind)
	}
	if ans.Verdict == VerdictAllow {
		l.Debug("hook: decided")
	} else {
		l.Info("hook: denied", "reason", ans.Reason, "request", ans.Request)
	}
	return ans
}

// ask opens the approval request and answers deny with its number; the
// character is waiting for permission.
func (e *Engine) ask(ctx context.Context, ev providers.Event, d classes.Decision) Answer {
	if e.Approvals == nil {
		return Answer{Verdict: VerdictDeny, Reason: fmt.Sprintf("%s pede aprovação, e esta máquina ainda não abre pedidos de aprovação", d.Action)}
	}
	n, err := e.Approvals.Open(ctx, ApprovalRequest{AgentID: ev.AgentID, TableID: ev.TableID, Action: d.Action, About: d.Reason, Event: ev})
	if err != nil || n <= 0 {
		e.log().Warn("hook: the approval request was not opened", "agent", ev.AgentID, "err", err)
		return Answer{Verdict: VerdictDeny, Reason: "não foi possível abrir o pedido de aprovação; tente de novo"}
	}
	if e.States != nil {
		if err := e.States.WaitingPermission(ctx, ev.AgentID, n); err != nil {
			e.log().Warn("hook: the state of the character was not kept", "agent", ev.AgentID, "request", n, "err", err)
		}
	}
	return Answer{Verdict: VerdictDeny, Reason: classes.AskReason(n, d.Action), Request: n}
}

// inform answers an informative event at once: the context of session_start
// and turn_start, and the synchronous hooks of the class. The rest of its
// processing is OnEvent's, after the answer.
func (e *Engine) inform(ctx context.Context, ev providers.Event) Answer {
	ans := Answer{Verdict: VerdictNeutral}
	if (ev.Event == providers.EventSessionStart || ev.Event == providers.EventTurnStart) && e.Contexts != nil {
		text, err := e.Contexts.Context(ctx, ev)
		if err != nil {
			e.log().Warn("hook: no context for the turn", "agent", ev.AgentID, "err", err)
		}
		ans.Context = text
	}
	hooks, err := e.classHooks(ctx, ev)
	if err != nil {
		e.log().Warn("hook: the hooks of the class could not be read", "agent", ev.AgentID, "err", err)
	}
	ans.ClassHooks = hooks
	return ans
}

func (e *Engine) classHooks(ctx context.Context, ev providers.Event) ([]ClassHook, error) {
	if e.ClassHooks == nil {
		return nil, nil
	}
	return e.ClassHooks.Sync(ctx, ev)
}

var errNoRegistry = errors.New("no terminal registry on this machine")

func (e *Engine) rules() classes.RulesSource {
	if e.Rules == nil {
		return noRules{}
	}
	return e.Rules
}

type noRules struct{}

func (noRules) Rules(context.Context, string) (*classes.Rules, error) {
	return nil, errors.New("no effective rules on this machine")
}

// env is the environment of the character, with the record of the
// exceptions; it also keeps env.json up to date for the degraded mode.
func (e *Engine) env(ctx context.Context, agentID string) (classes.Env, error) {
	if e.Characters == nil {
		return classes.Env{}, errNoRegistry
	}
	env, err := e.Characters.Env(ctx, agentID)
	if err != nil {
		return env, err
	}
	env.Exceptions = e.Ledger
	if err := e.saveEnv(agentID, env); err != nil {
		e.log().Warn("hook: env.json of the character not written", "agent", agentID, "err", err)
	}
	return env, nil
}

// saveEnv writes env.json when the environment changed since the last
// time: a whole file (a temporary renamed over it), 0640 like policy.json.
func (e *Engine) saveEnv(agentID string, env classes.Env) error {
	if e.Agents == "" || !classes.ValidAgentID(agentID) {
		return nil
	}
	ce := envOf(agentID, env)
	e.mu.Lock()
	defer e.mu.Unlock()
	if old, ok := e.envs[agentID]; ok && reflect.DeepEqual(old, ce) {
		return nil
	}
	data, err := json.Marshal(ce)
	if err != nil {
		return err
	}
	dir := filepath.Join(e.Agents, agentID)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".env-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, werr := tmp.Write(data)
	if err := errors.Join(werr, tmp.Chmod(0o640), tmp.Close()); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), filepath.Join(dir, EnvName)); err != nil {
		return err
	}
	if e.envs == nil {
		e.envs = map[string]CharacterEnv{}
	}
	e.envs[agentID] = ce
	return nil
}

// DrainEvery is how often the engine reads the hook queue while it runs:
// the dispatcher also leaves events there when the engine is slow.
const DrainEvery = time.Minute

// DrainLoop reads the hook queue now and every DrainEvery until ctx ends,
// giving each record to handle (nil: they are only logged). A queued record
// names a character it claims, not one the engine verified.
func (e *Engine) DrainLoop(ctx context.Context, dir string, handle func(QueueRecord)) {
	clk := e.Clock
	if clk == nil {
		clk = clock.Real{}
	}
	tick := clk.NewTicker(DrainEvery)
	defer tick.Stop()
	for {
		n, err := Drain(dir, MaxQueued, clk, e.log(), func(r QueueRecord) {
			if handle != nil {
				handle(r)
			}
		})
		if err != nil {
			e.log().Warn("hook: the hook queue was not read", "err", err)
		} else if n > 0 {
			e.log().Info("hook: events read from the hook queue", "count", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C():
		}
	}
}
