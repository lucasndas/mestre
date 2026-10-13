// Package cli is the `mestre` command line: parsing, calling the engine
// through the local channel (internal/localchan), and printing the result
// with the exit codes of docs/cli/convencoes.md.
//
// Until the CLI documents arrive (M4, docs/cli/comunicacao.md), the
// commands here are the "casca" that the channel needs to be exercised
// ("casca" rule of docs/desenvolvimento/ordem-de-implementacao.md): the
// syntax of `list` and `ask` and the happy path through the channel, plus
// `help` and `debug`, which this stage owns. Their errors, `--json` shapes
// and full definition close in the stage of the CLI of communication.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/pkg/clock"
)

// Env is everything the CLI takes from the process around it, so tests run
// it in memory.
type Env struct {
	Getenv func(string) string
	Stdout io.Writer
	Stderr io.Writer
	Clock  clock.Clock
	// Dial opens a connection to the socket the endpoint names. Nil means
	// the Unix socket at that path.
	Dial func(socket string) localchan.Dialer
	// Cwd is the current folder of the process (sent in the request).
	Cwd string
	// Version is the version of this binary.
	Version string
	// FileExists tells whether a path exists (nil: os.Stat).
	FileExists func(path string) bool
}

func (e *Env) defaults() {
	if e.Getenv == nil {
		e.Getenv = os.Getenv
	}
	if e.Clock == nil {
		e.Clock = clock.Real{}
	}
	if e.Dial == nil {
		e.Dial = localchan.UnixDialer
	}
	if e.FileExists == nil {
		e.FileExists = func(p string) bool { _, err := os.Stat(p); return err == nil }
	}
	if e.Version == "" {
		e.Version = "dev"
	}
}

// FlagKind is how a flag's value is read.
type FlagKind int

// Kinds of flags.
const (
	FlagString FlagKind = iota
	FlagDuration
	FlagBool
)

// Spec is the definition of one command.
type Spec struct {
	Name string
	// MinArgs and MaxArgs bound the positional arguments (MaxArgs < 0: any).
	MinArgs, MaxArgs int
	Flags            map[string]FlagKind
	// DefaultTimeout is the time limit of the command when it has no
	// --timeout; zero means the channel's default of 30 s.
	DefaultTimeout time.Duration
	// Local commands are answered by the CLI itself, calling the engine
	// only for what they need.
	Local bool
	Usage string
}

// AskDefaultTimeout is the default time limit of `mestre ask` until the
// waits document fixes it (docs/orquestracao/esperas-e-caixa-de-entrada.md).
const AskDefaultTimeout = 10 * time.Minute

// commands is the table of the casca.
var commands = []Spec{
	{Name: "help", MaxArgs: 1, Local: true, Usage: "mestre help [command]"},
	{Name: "debug", MaxArgs: 0, Local: true, Usage: "mestre debug"},
	{Name: "list", MaxArgs: 0, Usage: "mestre list"},
	{Name: "ask", MinArgs: 2, MaxArgs: 2, Flags: map[string]FlagKind{"timeout": FlagDuration},
		DefaultTimeout: AskDefaultTimeout, Usage: `mestre ask "<name>" "<prompt>" [--timeout <dur>]`},
}

// resolve finds the command at the start of toks and returns how many
// words its name has.
func resolve(toks []string) (Spec, int, bool) {
	if len(toks) >= 2 {
		for _, s := range commands {
			if s.Name == toks[0]+" "+toks[1] {
				return s, 2, true
			}
		}
	}
	for _, s := range commands {
		if s.Name == toks[0] {
			return s, 1, true
		}
	}
	return Spec{}, 0, false
}

// parsed is a command line after the flags were read.
type parsed struct {
	spec      Spec
	args      []string
	flags     map[string]any
	json      bool
	help      bool
	debugFlag bool
}

// usageError is a bad command line: exit 2.
type usageError struct{ msg, hint string }

func (e *usageError) Error() string { return e.msg }

// parse reads the command line. The global flags (--json, --help, --debug)
// may come anywhere; a flag of a command is `--name value` or `--name=value`.
func parse(argv []string) (*parsed, error) {
	p := &parsed{flags: map[string]any{}}
	var toks []string
	for _, a := range argv {
		switch a {
		case "--json":
			p.json = true
		case "--help", "-h":
			p.help = true
		case "--debug":
			p.debugFlag = true
		default:
			toks = append(toks, a)
		}
	}
	if len(toks) == 0 {
		if p.help {
			p.spec = Spec{Name: "help", Local: true, MaxArgs: 1, Usage: "mestre help [command]"}
			return p, nil
		}
		return nil, &usageError{"no command given", "mestre help lists the commands"}
	}
	spec, n, ok := resolve(toks)
	if !ok {
		return nil, &usageError{fmt.Sprintf("unknown command %q", toks[0]), "mestre help lists the commands"}
	}
	p.spec = spec
	toks = toks[n:]
	for i := 0; i < len(toks); i++ {
		a := toks[i]
		if !strings.HasPrefix(a, "--") || a == "--" {
			p.args = append(p.args, a)
			continue
		}
		name, val, hasVal := strings.Cut(strings.TrimPrefix(a, "--"), "=")
		kind, known := spec.Flags[name]
		if !known {
			return nil, &usageError{fmt.Sprintf("unknown flag --%s", name), "usage: " + spec.Usage}
		}
		if !hasVal {
			if kind == FlagBool {
				p.flags[name] = true
				continue
			}
			if i+1 >= len(toks) {
				return nil, &usageError{fmt.Sprintf("flag --%s needs a value", name), "usage: " + spec.Usage}
			}
			i++
			val = toks[i]
		}
		if err := p.setFlag(name, val); err != nil {
			return nil, err
		}
	}
	if n := len(p.args); !p.help && (n < spec.MinArgs || (spec.MaxArgs >= 0 && n > spec.MaxArgs)) {
		return nil, &usageError{fmt.Sprintf("wrong number of arguments for %s", spec.Name), "usage: " + spec.Usage}
	}
	return p, nil
}

func (p *parsed) setFlag(name, val string) error {
	kind, known := p.spec.Flags[name]
	if !known {
		return &usageError{fmt.Sprintf("unknown flag --%s", name), "mestre " + p.spec.Name + " --help"}
	}
	switch kind {
	case FlagDuration:
		d, err := time.ParseDuration(val)
		if err != nil || d <= 0 {
			return &usageError{fmt.Sprintf("--%s %q is not a duration", name, val), "use 90s, 10m or 2h"}
		}
		p.flags[name] = val
	case FlagBool:
		p.flags[name] = val == "true"
	default:
		p.flags[name] = val
	}
	return nil
}

// timeout is the time limit of the command.
func (p *parsed) timeout() time.Duration {
	if v, ok := p.flags["timeout"].(string); ok {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return p.spec.DefaultTimeout
}

// Run runs the command line argv (without the program name) and returns the
// exit code.
func Run(ctx context.Context, env Env, argv []string) int {
	env.defaults()
	p, err := parse(argv)
	if err != nil {
		var ue *usageError
		errors.As(err, &ue)
		o := out{env: env, json: slicesContains(argv, "--json")}
		return o.fail(localchan.Fail(localchan.CodeUsage, ue.msg, ue.hint))
	}
	o := out{env: env, json: p.json}
	if p.help && p.spec.Name != "help" {
		fmt.Fprintf(env.Stdout, "usage: %s\n", p.spec.Usage)
		return localchan.ExitOK
	}
	switch p.spec.Name {
	case "debug":
		return runDebug(ctx, env, p)
	case "help":
		return runHelp(ctx, env, p)
	}
	return runRemote(ctx, env, p, o)
}

func slicesContains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// client builds the client of the endpoint of this process.
func client(env Env, window time.Duration) (*localchan.Client, error) {
	sock, err := localchan.EndpointFromEnv(env.Getenv)
	if err != nil {
		return nil, err
	}
	return localchan.NewClient(localchan.ClientConfig{Clock: env.Clock, Dial: env.Dial(sock), ConnectWindow: window}), nil
}

func request(env Env, p *parsed) localchan.Request {
	return localchan.Request{
		AgentID: env.Getenv(localchan.EnvAgentID),
		Cmd:     p.spec.Name,
		Args:    localchan.TextArgs(p.args...),
		Flags:   p.flags,
		Cwd:     env.Cwd,
	}
}

// runRemote sends a command to the engine and prints what comes back.
func runRemote(ctx context.Context, env Env, p *parsed, o out) int {
	c, err := client(env, 0)
	if err != nil {
		return o.fail(localchan.Fail(localchan.CodeEngineUnreachable, err.Error(), "run mestre debug"))
	}
	opt := localchan.CallOptions{Timeout: p.timeout()}
	if p.debugFlag {
		opt.OnPartial = func(d json.RawMessage) { fmt.Fprintf(env.Stderr, "partial: %s\n", d) }
	}
	resp, err := c.Call(ctx, request(env, p), opt)
	if err != nil {
		return o.fail(localchan.Fail(localchan.CodeInternal, "interrupted: "+err.Error(), ""))
	}
	return o.response(p.spec.Name, resp)
}

// out prints results and errors in text or --json.
type out struct {
	env  Env
	json bool
}

// response prints a final response and returns its exit code.
func (o out) response(cmd string, r localchan.Response) int {
	code := r.ExitCode()
	switch {
	case o.json:
		o.printJSON(r)
	case r.OK:
		fmt.Fprintln(o.env.Stdout, renderText(cmd, r.Data))
	default:
		o.textError(r.Error)
	}
	return code
}

// fail prints an error response of the CLI itself.
func (o out) fail(r localchan.Response) int {
	code := localchan.ExitFor(r.Error.Code)
	if o.json {
		o.printJSON(r)
	} else {
		o.textError(r.Error)
	}
	return code
}

func (o out) textError(e *localchan.Error) {
	if e == nil {
		fmt.Fprintln(o.env.Stderr, "error: the engine answered with no result")
		return
	}
	fmt.Fprintf(o.env.Stderr, "error: %s\n", e.Message)
	if e.Hint != "" {
		fmt.Fprintf(o.env.Stderr, "hint: %s\n", e.Hint)
	}
}

// printJSON writes the single object of --json: {"ok", "data"} or
// {"ok": false, "error"}, and nothing on stderr.
func (o out) printJSON(r localchan.Response) {
	v := map[string]any{"ok": r.OK}
	if len(r.Data) > 0 || r.OK {
		var data any
		if len(r.Data) > 0 {
			_ = json.Unmarshal(r.Data, &data)
		}
		v["data"] = data
	}
	if !r.OK {
		v["error"] = r.Error
	}
	b, _ := json.Marshal(v)
	fmt.Fprintln(o.env.Stdout, string(b))
}

// renderText is the text mode of the casca: the answer of `ask`, the text
// of a result that has one, otherwise the data as JSON. The tables of each
// command arrive with the documents of the CLI.
func renderText(cmd string, data json.RawMessage) string {
	if len(data) == 0 {
		return ""
	}
	var obj map[string]any
	if json.Unmarshal(data, &obj) == nil {
		key := "text"
		if cmd == "ask" {
			key = "response"
		}
		if s, ok := obj[key].(string); ok {
			return s
		}
	}
	var s string
	if json.Unmarshal(data, &s) == nil {
		return s
	}
	return string(data)
}
