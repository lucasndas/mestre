package hook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"runtime"
	"time"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/localchan"
	"github.com/mestre-ia/mestre/internal/logging"
	"github.com/mestre-ia/mestre/internal/providers"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// Env is what one run of the dispatcher gets from its process.
type Env struct {
	Getenv func(string) string
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	Clock  clock.Clock
	// Getwd is the folder of the process, sent with the request; nil sends
	// none.
	Getwd func() (string, error)
	// Dial opens a connection to the socket of the endpoint; nil is the
	// Unix socket.
	Dial func(socket string) localchan.Dialer
	// Files is the disk (snapshots and queue); nil is OSFiles.
	Files Files
	// Resolve resolves the links of a path for the adapter
	// (providers.Options.Resolve); nil is filepath.EvalSymlinks.
	Resolve func(string) (string, error)
	// Machine are the folders of the instance; nil finds them from
	// MESTRE_ENDPOINT (InstanceOf).
	Machine *Machine
	// ClassHooks runs the synchronous hooks of the class; nil runs none,
	// and a blocking one then denies.
	ClassHooks Runner
	// GOOS is the system ("" is this one).
	GOOS string
	// DecideDegraded is the decision with the engine down; nil is
	// classes.DecideDegraded. The dispatcher runs it within the deadline of
	// the call, whatever it takes (the tests give a slow one).
	DecideDegraded func(ctx context.Context, env classes.Env, snapshot *classes.Rules, ev providers.Event) classes.Decision
}

// Runner runs one synchronous hook of the class with the event on stdin,
// MESTRE_HOOK_EVENT in its environment and its folder, within its time
// limit (docs/classes/classes.md#hooks-extras-da-classe). err is a hook
// that could not run or ran out of time.
type Runner interface {
	Run(ctx context.Context, h ClassHook, event providers.EventKind, stdin []byte) (exit int, stderr string, err error)
}

// EnvRecord, set to 1 in the environment of a character, makes the
// dispatcher send the engine the payload of each hook as it got it
// (providers.Event.Raw): the conformance test records them
// (docs/provedores/conformidade.md#resultado).
const EnvRecord = "MESTRE_HOOK_RECORD"

// Run is `mestre hook --provider <id> --event <native event>`: it answers
// one hook call of a provider and returns the exit code, only 0 or 2. args
// are the arguments after "hook".
func Run(ctx context.Context, env Env, args []string) int {
	provider, native, perr := parseArgs(args)
	var adapter providers.Adapter
	if perr == nil {
		adapter, perr = providers.NewRegistry(providers.Options{
			Resolve: env.Resolve,
			// CODEX_HOME and the transcript are read only at a Codex Stop
			// of a character, never before.
			CodexTurnFailure: func(stop providers.Event) (providers.StopReason, string, bool) {
				return providers.CodexTurnFailure(env.Getenv("CODEX_HOME"))(stop)
			},
		}).For(provider)
	}
	if perr == nil && provider == providers.Null {
		perr = errors.New("--provider is empty")
	}
	agent := env.Getenv(localchan.EnvAgentID)
	if agent == "" {
		// Not a character (a person's own session in the same folder):
		// neutral at once, reading nothing and connecting nowhere.
		if perr != nil {
			return 0
		}
		return write(env, adapter.FormatResponse(native, providers.Reply{Kind: providers.ReplyNeutral}))
	}
	if perr != nil {
		// A hook file the Mestre did not generate: block where blocking is
		// possible.
		fmt.Fprintln(env.Stderr, mask("mestre hook: "+perr.Error()))
		return 2
	}
	d := newDispatcher(env, adapter, provider, native, agent)
	out, finished := d.within(ctx)
	if finished && out.Exit == 0 {
		for _, n := range d.notes {
			out.Stderr = append(out.Stderr, n...)
		}
	}
	return write(env, out)
}

func parseArgs(args []string) (providers.ID, string, error) {
	var provider, event string
	var seen [2]bool
	for i := 0; i < len(args); i++ {
		a := args[i]
		if i+1 >= len(args) {
			return "", "", fmt.Errorf("usage: mestre hook --provider <id> --event <native event> (%q has no value)", a)
		}
		switch a {
		case "--provider":
			provider, seen[0] = args[i+1], true
		case "--event":
			event, seen[1] = args[i+1], true
		default:
			return "", "", fmt.Errorf("usage: mestre hook --provider <id> --event <native event> (unknown %q)", a)
		}
		i++
	}
	if !seen[0] || !seen[1] || event == "" {
		return "", "", errors.New("usage: mestre hook --provider <id> --event <native event>")
	}
	return providers.ID(provider), event, nil
}

func write(env Env, out providers.HookOutput) int {
	if len(out.Stdout) > 0 {
		_, _ = env.Stdout.Write(out.Stdout)
	}
	if len(out.Stderr) > 0 {
		_, _ = env.Stderr.Write(out.Stderr)
	}
	if out.Exit != 0 && out.Exit != 2 {
		return 2 // never 1: Claude Code does not block on it
	}
	return out.Exit
}

// mask hides the secrets of a line the dispatcher writes to stderr.
func mask(s string) string { return logging.NewMasker().Mask(s) }

type dispatcher struct {
	env      Env
	clk      clock.Clock
	files    Files
	adapter  providers.Adapter
	opts     providers.Options
	provider providers.ID
	native   string
	agent    string
	goos     string
	machine  *Machine
	// start is when the call began: the budget of the hooks of the class
	// counts from it. deadline is when the whole call must have answered
	// (callLimit after start).
	start, deadline time.Time
	// notes are the failures of the hooks of the class that did not block:
	// "registra o erro e segue" (docs/classes/classes.md#hooks-extras-da-classe),
	// on the stderr of the call.
	notes []string
}

// note registers the failure of a hook of the class that goes on.
func (d *dispatcher) note(h ClassHook, exit int, stderr string, err error) {
	why := stderr
	switch {
	case err != nil:
		why = err.Error()
	case why == "":
		why = fmt.Sprintf("saiu com %d", exit)
	}
	d.notes = append(d.notes, mask(fmt.Sprintf("mestre hook: o hook da classe %s falhou e a execução seguiu: %s\n", h.Name, why)))
}

func newDispatcher(env Env, a providers.Adapter, p providers.ID, native, agent string) *dispatcher {
	d := &dispatcher{
		env: env, clk: env.Clock, files: env.Files, adapter: a, opts: providers.Options{Resolve: env.Resolve},
		provider: p, native: native, agent: agent, goos: env.GOOS, machine: env.Machine,
	}
	if d.clk == nil {
		d.clk = clock.Real{}
	}
	if d.files == nil {
		d.files = OSFiles{}
	}
	if d.goos == "" {
		d.goos = runtime.GOOS
	}
	return d
}

// guardGrace is how long past the deadline of the call the dispatcher
// still waits for its own answer before it gives the answer of a call out
// of time: within the margin below the provider's limit.
const guardGrace = 500 * time.Millisecond

// callLimit is the time the whole call has, from the start of `mestre hook`:
// the provider's limit for the native event less the margin
// (classes.ProviderMargin), never less than a second. The wait for the
// engine, the degraded decision and the hooks of the class all fit in it.
func (d *dispatcher) callLimit() time.Duration {
	limit := time.Duration(providers.HookSeconds(d.provider, d.native)-classes.ProviderMargin) * time.Second
	return max(limit, time.Second)
}

// within runs the call under its deadline. Past the deadline, and the grace
// after it, the answer is the one of an internal error: it blocks in
// pre_tool and permission_request (exit 2 in Claude Code and Codex, deny in
// Antigravity), because past its limit Codex goes on as if allowed.
// finished is false then: the call goes on in the background until the
// process exits, and nothing of it (the notes) may be read.
func (d *dispatcher) within(ctx context.Context) (providers.HookOutput, bool) {
	d.start = d.clk.Now()
	limit := d.callLimit()
	d.deadline = d.start.Add(limit)
	rctx, cancel := clock.WithTimeout(ctx, d.clk, limit)
	defer cancel()
	done := make(chan providers.HookOutput, 1)
	go func() { done <- d.run(rctx) }()
	guard := d.clk.NewTimer(limit + guardGrace)
	defer guard.Stop()
	select {
	case out := <-done:
		return out, true
	case <-guard.C():
		return d.fail(errOutOfTime), false
	}
}

// errOutOfTime is a call that did not answer within its deadline.
var errOutOfTime = errors.New("a chamada passou do prazo do provedor")

// run answers the call; a panic is an internal error.
func (d *dispatcher) run(ctx context.Context) (out providers.HookOutput) {
	defer func() {
		if r := recover(); r != nil {
			out = d.fail(fmt.Errorf("panic: %v", r))
		}
	}()
	payload, err := readStdin(d.env.Stdin)
	if err != nil {
		return d.fail(err)
	}
	in := providers.HookInput{
		NativeEvent: d.native, Stdin: payload, AgentID: d.agent, TableID: d.env.Getenv("MESTRE_MESA"), ReceivedAt: d.clk.Now(),
	}
	ev, err := d.adapter.NormalizeHook(in)
	if err == nil && d.env.Getenv(EnvRecord) == "1" && json.Valid(payload) {
		ev.Raw = payload
	}
	switch {
	case errors.Is(err, providers.ErrUnknownEvent):
		// Logged by the engine (or left in the queue) and answered neutral.
		ev = providers.Event{
			Version: providers.EventVersion, Provider: d.provider, NativeEvent: d.native, AgentID: d.agent,
			TableID: in.TableID, Error: err.Error(), ReceivedAt: in.ReceivedAt,
		}
		d.inform(ctx, ev)
		return d.format(providers.Reply{Kind: providers.ReplyNeutral})
	case err != nil:
		return d.fail(err)
	case ev.Event == providers.EventPreTool || ev.Event == providers.EventPermissionRequest:
		return d.format(d.decide(ctx, ev))
	}
	return d.format(d.inform(ctx, ev))
}

func (d *dispatcher) format(r providers.Reply) providers.HookOutput {
	return d.adapter.FormatResponse(d.native, r)
}

// fail is the internal error of the dispatcher itself: it blocks in
// pre_tool and permission_request (exit 2 with the reason, or deny) and is
// neutral in the other events.
func (d *dispatcher) fail(err error) providers.HookOutput {
	return d.format(providers.Reply{Kind: providers.ReplyError,
		Reason: mask("mestre hook: erro interno, a ação foi bloqueada: " + err.Error())})
}

func readStdin(r io.Reader) ([]byte, error) {
	if r == nil {
		return nil, errors.New("no stdin")
	}
	b, err := io.ReadAll(io.LimitReader(r, MaxStdin+1))
	if err != nil {
		return nil, fmt.Errorf("read the payload: %w", err)
	}
	if len(b) > MaxStdin {
		return nil, fmt.Errorf("payload over %d bytes: truncated, it cannot be decided", MaxStdin)
	}
	return b, nil
}

func deny(reason string) providers.Reply {
	return providers.Reply{Kind: providers.ReplyDeny, Reason: reason}
}

// decide is pre_tool and permission_request: the engine's decision, or the
// degraded one when the engine does not answer in time. A command longer
// than MaxCommand is denied before any analysis, by the engine or here.
func (d *dispatcher) decide(ctx context.Context, ev providers.Event) providers.Reply {
	if ev.Tool != nil && len(ev.Tool.Command) > MaxCommand {
		dec := classes.Decision{Verdict: classes.Deny, Layer: classes.LayerFixed, Reason: fmt.Sprintf(
			"regra fixa de segurança: o comando tem mais de %d KiB, grande demais para analisar; divida-o ou grave o conteúdo num arquivo", MaxCommand>>10)}
		_ = enqueue(d.files, d.queueDir(), d.clk, ev, &dec)
		return deny(dec.Reason)
	}
	ans, reached, err := d.ask(ctx, ev, DecisionDeadline)
	if !reached {
		dec, ok := d.degradedWithin(ctx, ev)
		if !ok {
			dec = classes.Decision{Verdict: classes.Deny, Layer: classes.LayerDegraded,
				Reason: classes.DegradedReason + " (a análise local passou do prazo do provedor)"}
			_ = enqueue(d.files, d.queueDir(), d.clk, ev, &dec)
			return providers.Reply{Kind: providers.ReplyError, Reason: "mestre hook: " + dec.Reason + "; a ação foi bloqueada"}
		}
		_ = enqueue(d.files, d.queueDir(), d.clk, ev, &dec)
		if dec.Verdict == classes.Allow {
			return providers.Reply{Kind: providers.ReplyAllow, Tool: ev.Tool}
		}
		return deny(dec.Reason)
	}
	if err != nil {
		return deny("o motor não decidiu (" + err.Error() + "); a ação foi bloqueada")
	}
	switch ans.Verdict {
	case VerdictAllow:
		if why, denied := d.classHooks(ctx, ev, ans.ClassHooks, true); denied {
			return deny(why)
		}
		return providers.Reply{Kind: providers.ReplyAllow, Tool: ev.Tool}
	case VerdictDeny:
		if ans.Reason == "" {
			ans.Reason = "negado pelo motor"
		}
		return deny(ans.Reason)
	}
	return deny(fmt.Sprintf("o motor respondeu %q, que não é uma decisão; a ação foi bloqueada", ans.Verdict))
}

// inform is every other event: the engine acknowledges it (and gives the
// context of session_start and turn_start); without the engine, the event
// goes to the queue and the turn goes on without new context.
func (d *dispatcher) inform(ctx context.Context, ev providers.Event) providers.Reply {
	ans, reached, err := d.ask(ctx, ev, InfoDeadline)
	if !reached {
		_ = enqueue(d.files, d.queueDir(), d.clk, ev, nil)
		return providers.Reply{Kind: providers.ReplyNeutral}
	}
	if err != nil {
		return providers.Reply{Kind: providers.ReplyNeutral}
	}
	d.classHooks(ctx, ev, ans.ClassHooks, false)
	if ans.Context != "" {
		return providers.Reply{Kind: providers.ReplyContext, Context: ans.Context}
	}
	return providers.Reply{Kind: providers.ReplyNeutral}
}

// errEngine is a response of the engine that is an error.
type errEngine struct{ code string }

func (e errEngine) Error() string { return e.code }

// ask sends the event to the engine and waits up to deadline. reached is
// false when the engine is down: no connection, or no answer in time. err
// is an engine that answered with an error, or an answer that does not
// decode.
func (d *dispatcher) ask(ctx context.Context, ev providers.Event, deadline time.Duration) (Answer, bool, error) {
	socket, err := localchan.EndpointFromEnv(d.env.Getenv)
	if err != nil {
		return Answer{}, false, nil
	}
	arg, err := json.Marshal(ev)
	if err == nil && len(arg) > localchan.MaxLine-4096 && ev.Raw != nil {
		// The recorded payload goes first: nothing decides on it.
		ev.Raw = nil
		arg, err = json.Marshal(ev)
	}
	if err == nil && len(arg) > localchan.MaxLine-4096 && ev.Tool != nil {
		// The raw input is not decided on: what does not fit goes without it.
		t := *ev.Tool
		t.RawInput = nil
		ev.Tool = &t
		arg, err = json.Marshal(ev)
	}
	if err != nil {
		return Answer{}, true, err
	}
	if len(arg) > localchan.MaxLine-4096 {
		return Answer{}, true, errors.New("o evento é grande demais para o canal local")
	}
	dial := localchan.UnixDialer
	if d.env.Dial != nil {
		dial = d.env.Dial
	}
	cwd := ""
	if d.env.Getwd != nil {
		cwd, _ = d.env.Getwd()
	}
	client := localchan.NewClient(localchan.ClientConfig{Clock: d.clk, Dial: dial(socket), ConnectWindow: -1})
	cctx, cancel := clock.WithTimeout(ctx, d.clk, deadline)
	defer cancel()
	resp, err := client.Call(cctx, localchan.Request{
		AgentID: d.agent, Cmd: localchan.CmdHook, Args: []json.RawMessage{arg}, Cwd: cwd,
	}, localchan.CallOptions{Timeout: deadline})
	if err != nil {
		return Answer{}, false, nil // the deadline (or the caller) ended the wait
	}
	if !resp.OK {
		if resp.Error != nil {
			switch resp.Error.Code {
			case localchan.CodeEngineUnreachable, localchan.CodeTimeout:
				return Answer{}, false, nil
			}
			return Answer{}, true, errEngine{resp.Error.Code}
		}
		return Answer{}, true, errEngine{"internal"}
	}
	var ans Answer
	if err := resp.DecodeData(&ans); err != nil {
		return Answer{}, true, fmt.Errorf("resposta do motor ilegível: %w", err)
	}
	return ans, true, nil
}

// classHooks runs the synchronous hooks of the class the engine sent. In
// pre_tool (decision) an exit 2 denies with its stderr, and a failure or a
// timeout denies when the hook is blocking; elsewhere the outcome only
// matters to the hook's own output.
func (d *dispatcher) classHooks(ctx context.Context, ev providers.Event, hooks []ClassHook, decision bool) (string, bool) {
	if len(hooks) == 0 {
		return "", false
	}
	stdin, err := json.Marshal(ev)
	if err != nil {
		return "hook da classe: " + err.Error(), decision
	}
	// The budget of the class hooks and the deadline of the call: the
	// earlier of the two.
	deadline := d.start.Add(budgetOf(hooks))
	if !d.deadline.IsZero() && d.deadline.Before(deadline) {
		deadline = d.deadline
	}
	for _, h := range hooks {
		if h.Refused != "" {
			if decision && h.Blocking {
				return fmt.Sprintf("hook da classe %s recusado e é bloqueante: %s", h.Name, mask(h.Refused)), true
			}
			continue
		}
		if d.env.ClassHooks == nil {
			if decision && h.Blocking {
				return fmt.Sprintf("hook da classe %s não pôde rodar", h.Name), true
			}
			continue
		}
		// The hooks of the call share one budget: what is left of it, not
		// the hook's own limit, when that is less.
		left := deadline.Sub(d.clk.Now())
		if left <= 0 {
			if decision && h.Blocking {
				return fmt.Sprintf("hook da classe %s é bloqueante e não rodou: o tempo dos hooks da classe nesta chamada acabou", h.Name), true
			}
			d.note(h, 0, "", errors.New("não rodou: o tempo dos hooks da classe nesta chamada acabou"))
			continue
		}
		hctx, cancel := clock.WithTimeout(ctx, d.clk, min(h.Timeout(), left))
		exit, stderr, err := d.env.ClassHooks.Run(hctx, h, ev.Event, stdin)
		cancel()
		failed := err != nil || exit != 0
		switch {
		case decision && err == nil && exit == 2:
			reason := stderr
			if reason == "" {
				reason = "sem motivo"
			}
			return fmt.Sprintf("hook da classe %s negou: %s", h.Name, mask(reason)), true
		case decision && failed && h.Blocking:
			return fmt.Sprintf("hook da classe %s falhou e é bloqueante", h.Name), true
		case failed:
			d.note(h, exit, stderr, err)
		}
	}
	return "", false
}

// budgetOf is the time the hooks of one call share, from the start of the
// call: the smallest BudgetMS the engine sent, or, from an engine that sent
// none, the largest time limit among them (each is already cut below the
// provider's).
func budgetOf(hooks []ClassHook) time.Duration {
	var budget, longest int64
	for _, h := range hooks {
		if h.BudgetMS > 0 && (budget == 0 || h.BudgetMS < budget) {
			budget = h.BudgetMS
		}
		longest = max(longest, h.TimeoutMS)
	}
	if budget == 0 {
		budget = longest
	}
	return time.Duration(budget) * time.Millisecond
}

// machineOf finds the folders of the instance from the endpoint.
func (d *dispatcher) machineOf() *Machine {
	if d.machine != nil {
		return d.machine
	}
	socket, err := localchan.EndpointFromEnv(d.env.Getenv)
	if err != nil {
		return &Machine{}
	}
	inst, ok := InstanceOf(d.goos, socket)
	if !ok {
		return &Machine{}
	}
	// On Windows the root is the one of the socket InstanceOf validated,
	// <root>\Mestre\<inst>\run\cli.sock, never a %ProgramData% of the
	// caller (empty, or another one).
	programData := ""
	if d.goos == "windows" {
		programData = filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(socket))))
	}
	m := MachineOf(d.goos, programData, inst)
	if !absolute(d.goos, m.Agents) || !absolute(d.goos, m.Queue) {
		m = Machine{} // no snapshot and no queue: never folders relative to the worktree
	}
	d.machine = &m
	return d.machine
}

func (d *dispatcher) queueDir() string { return d.machineOf().Queue }

// degradedWithin runs the degraded decision until the context of the call
// ends: ok is false when it did not decide in time (or panicked), and the
// call is then denied, not left to the provider's limit.
func (d *dispatcher) degradedWithin(ctx context.Context, ev providers.Event) (classes.Decision, bool) {
	type result struct {
		dec classes.Decision
		ok  bool
	}
	ch := make(chan result, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				ch <- result{}
			}
		}()
		ch <- result{d.degraded(ctx, ev), true}
	}()
	select {
	case r := <-ch:
		return r.dec, r.ok
	case <-ctx.Done():
		return classes.Decision{}, false
	}
}

// degraded is the decision with the engine down: the fixed rules and the
// snapshot of the character, with its environment from env.json.
func (d *dispatcher) degraded(ctx context.Context, ev providers.Event) classes.Decision {
	m := d.machineOf()
	env := classes.Env{AgentID: d.agent, TableID: ev.TableID, Layout: m.Layout, OS: d.goos, Normalize: d.opts.NormalizePath}
	var snap *classes.Rules
	// Without env.json the engine never said the class has no blocking
	// hooks: as with them, only reading.
	blocking := true
	if m.Agents != "" && classes.ValidAgentID(d.agent) {
		dir := filepath.Join(m.Agents, d.agent)
		if b, err := d.files.ReadFile(filepath.Join(dir, classes.SnapshotName)); err == nil {
			snap, _ = classes.ParseRules(b) // unreadable: nil, only the fixed rules and reading
		}
		if b, err := d.files.ReadFile(filepath.Join(dir, EnvName)); err == nil {
			if ce, err := parseEnv(b, d.agent); err == nil {
				env.TableID, env.Worktree, env.Tmp, env.Home = ce.TableID, ce.Worktree, ce.Tmp, ce.Home
				env.OperatorBase, env.ComputerControl = ce.OperatorBase, ce.ComputerControl
				blocking = ce.BlockingHooks
			}
		}
	}
	decide := d.env.DecideDegraded
	if decide == nil {
		decide = classes.DecideDegraded
	}
	dec := decide(ctx, env, snap, ev)
	if dec.Verdict == classes.Allow && snap != nil && blocking {
		// The blocking hooks of the class do not run without the engine,
		// and they exist to deny: what the snapshot allows beyond reading
		// waits for the engine.
		if ro := decide(ctx, env, nil, ev); ro.Verdict != classes.Allow {
			return classes.Decision{Verdict: classes.Deny, Layer: classes.LayerDegraded, Action: dec.Action,
				Reason: classes.DegradedReason + " (a classe tem hooks bloqueantes, que só rodam com o motor: só leitura)"}
		}
	}
	return dec
}
