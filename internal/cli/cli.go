// Package cli is the `mestre` command line: the single definition of the
// commands (commands.go), parsing, calling the engine through the local
// channel (internal/localchan) and printing the result with the conventions
// of docs/cli/convencoes.md: text or --json, the exit codes, the identity
// of the caller, the help and `mestre debug`. The engine side of the
// conventions (the level of the caller, the class, the secrets, the request
// that waits in the outbox) is the Gate in gate.go.
//
// The commands of the documents of the CLI that belong to later milestones
// are not here: they are added to Default with their own Spec and use this
// package's shared code. Until then, `list`, `ask` and `connect` are the
// "casca" that the channel and the connections need to be exercised ("casca" rule of
// docs/desenvolvimento/ordem-de-implementacao.md): the syntax and the happy
// path through the channel.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/logging"
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
	// Commands is the definition of the commands (nil: Default).
	Commands *Registry
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
	if e.Commands == nil {
		e.Commands = Default()
	}
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
// may come anywhere; a flag of a command is `--name value` or `--name=value`;
// everything after a lone `--` is positional.
func parse(reg *Registry, argv []string) (*parsed, error) {
	p := &parsed{flags: map[string]any{}}
	var toks []string
	literal := false
	for _, a := range argv {
		switch {
		case literal:
			toks = append(toks, a)
		case a == "--":
			literal = true
			toks = append(toks, a)
		case a == "--json":
			p.json = true
		case a == "--help" || a == "-h":
			p.help = true
		case a == "--debug":
			p.debugFlag = true
		default:
			toks = append(toks, a)
		}
	}
	if len(toks) == 0 {
		if p.help {
			p.spec, _ = reg.Get("help")
			return p, nil
		}
		return nil, &usageError{"no command given", "mestre help lists the commands"}
	}
	spec, n, ok := reg.Resolve(toks)
	if !ok {
		return nil, &usageError{fmt.Sprintf("unknown command %q", toks[0]), "mestre help lists the commands"}
	}
	p.spec = spec
	toks = toks[n:]
	literal = false
	for i := 0; i < len(toks); i++ {
		a := toks[i]
		if a == "--" && !literal {
			literal = true
			continue
		}
		if literal || len(a) < 3 || a[:2] != "--" {
			p.args = append(p.args, a)
			continue
		}
		name, val, hasVal := cutFlag(a)
		f, known := spec.flag(name)
		if !known {
			return nil, &usageError{fmt.Sprintf("unknown flag --%s", name), "mestre " + spec.Name + " --help"}
		}
		if !hasVal {
			if f.Kind == FlagBool {
				p.flags[name] = true
				continue
			}
			if i+1 >= len(toks) {
				return nil, &usageError{fmt.Sprintf("flag --%s needs a value", name), "usage: " + spec.Usage}
			}
			i++
			val = toks[i]
		}
		if err := p.setFlag(f, val); err != nil {
			return nil, err
		}
	}
	if n := len(p.args); !p.help && (n < spec.MinArgs || (spec.MaxArgs >= 0 && n > spec.MaxArgs)) {
		return nil, &usageError{fmt.Sprintf("wrong number of arguments for %s", spec.Name), "usage: " + spec.Usage}
	}
	return p, nil
}

func cutFlag(a string) (name, val string, hasVal bool) {
	for i := 2; i < len(a); i++ {
		if a[i] == '=' {
			return a[2:i], a[i+1:], true
		}
	}
	return a[2:], "", false
}

func (p *parsed) setFlag(f Flag, val string) error {
	switch f.Kind {
	case FlagDuration:
		d, err := time.ParseDuration(val)
		if err != nil || d <= 0 {
			return &usageError{fmt.Sprintf("--%s %q is not a duration", f.Name, val), "use 90s, 10m or 2h"}
		}
		p.flags[f.Name] = val
	case FlagBool:
		if val != "true" && val != "false" {
			return &usageError{fmt.Sprintf("--%s %q is not true or false", f.Name, val), "use --" + f.Name}
		}
		p.flags[f.Name] = val == "true"
	default:
		p.flags[f.Name] = val
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
	p, err := parse(env.Commands, argv)
	if err != nil {
		var ue *usageError
		errors.As(err, &ue)
		o := out{env: env, json: slicesContains(argv, "--json")}
		return o.fail(localchan.Fail(localchan.CodeUsage, ue.msg, ue.hint))
	}
	o := out{env: env, json: p.json}
	if p.help && p.spec.Name != "help" {
		return o.commandHelp(p.spec)
	}
	switch p.spec.Name {
	case "debug":
		return runDebug(ctx, env, p)
	case "help":
		return runHelp(ctx, env, p)
	}
	if env.Getenv(localchan.EnvAgentID) == "" {
		// A person in a shell, or a terminal made by `spawn`: only help,
		// debug and hook work. The engine refuses the same way, but there
		// is no reason to wait for an engine that may be restarting.
		return o.fail(notIdentified(localchan.EnvAgentID + " is not set: only help and debug work outside a character"))
	}
	return runRemote(ctx, env, p, o)
}

func notIdentified(msg string) localchan.Response {
	return localchan.Fail(localchan.CodeNotIdentified, msg, "run mestre debug")
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
		masker := logging.NewMasker()
		opt.OnPartial = func(d json.RawMessage) { fmt.Fprintf(env.Stderr, "partial: %s\n", masker.Mask(string(d))) }
	}
	resp, err := c.Call(ctx, request(env, p), opt)
	if err != nil {
		return o.fail(localchan.Fail(localchan.CodeInternal, "interrupted: "+err.Error(), "repeat the command; mestre debug checks the engine"))
	}
	return o.response(p.spec.Name, resp)
}
