package cli

import (
	"context"
	"fmt"

	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/logging"
)

// Profiles gives the engine's record of a character: the level and the class
// (docs/cli/convencoes.md#identificação-do-chamador). A character the engine
// does not know gets the profile of an ordinary character with a class that
// allows nothing.
type Profiles interface {
	Profile(ctx context.Context, agentID string) Profile
}

// ProfilesFunc is Profiles as a function.
type ProfilesFunc func(ctx context.Context, agentID string) Profile

// Profile implements Profiles.
func (f ProfilesFunc) Profile(ctx context.Context, agentID string) Profile { return f(ctx, agentID) }

// QueueingForwarder is a Forwarder that can keep a call for later: with the
// Citadel away, the call goes to the outbox of the machine and the answer is
// Queued. The Gate uses it for the commands defined as Queueable.
type QueueingForwarder interface {
	localchan.Forwarder
	ForwardOrQueue(ctx context.Context, c *localchan.Call) localchan.Response
}

// GateConfig configures a Gate.
type GateConfig struct {
	// Commands is the definition of the commands (nil: Default).
	Commands *Registry
	// Profiles gives the level and the class of the callers (nil: every
	// caller is an ordinary character whose class allows no command).
	Profiles Profiles
	// Next serves what the Gate lets through: the Forwarder that sends the
	// call to the Citadel.
	Next localchan.Forwarder
	// Masker is the detector of secrets, the one of the log masking (nil:
	// the formats alone, without the values of the vault).
	Masker *logging.Masker
}

// Gate is the engine side of the conventions of the CLI
// (docs/cli/convencoes.md): before a call goes to the Citadel, the engine
// checks it against the level of the caller and the class, refuses the text
// that looks like a secret where the command says so, and lets a command that
// needs no answer now wait in the outbox. The engine checks every call; the
// skill only teaches.
type Gate struct {
	cfg GateConfig
}

var _ localchan.Forwarder = (*Gate)(nil)

// NewGate returns a Gate.
func NewGate(cfg GateConfig) *Gate {
	if cfg.Commands == nil {
		cfg.Commands = Default()
	}
	if cfg.Masker == nil {
		cfg.Masker = logging.NewMasker()
	}
	if cfg.Next == nil {
		cfg.Next = localchan.UnavailableForwarder{}
	}
	return &Gate{cfg: cfg}
}

func (g *Gate) profile(ctx context.Context, c *localchan.Call) Profile {
	if g.cfg.Profiles == nil || !c.Caller.Identified {
		return Profile{}
	}
	return g.cfg.Profiles.Profile(ctx, c.Caller.AgentID)
}

// Forward implements localchan.Forwarder.
func (g *Gate) Forward(ctx context.Context, c *localchan.Call) localchan.Response {
	spec, ok := g.cfg.Commands.Get(c.Request.Cmd)
	switch {
	case !ok:
		return localchan.Fail(localchan.CodeUsage, fmt.Sprintf("unknown command %q", c.Request.Cmd), "mestre help lists the commands")
	case !c.Caller.Identified:
		return notIdentified("the engine does not know this process as a process of a character")
	}
	if refusal, ok := spec.Check(g.profile(ctx, c)); !ok {
		return refusal
	}
	if refusal, ok := spec.checkRequest(c.Request); !ok {
		return refusal
	}
	if spec.Secret {
		if refusal, found := g.secret(c.Request); found {
			return refusal
		}
	}
	if q, ok := g.cfg.Next.(QueueingForwarder); ok && spec.Queueable {
		return q.ForwardOrQueue(ctx, c)
	}
	return g.cfg.Next.Forward(ctx, c)
}

// checkRequest checks the shape of the request against the definition, as
// the CLI already did on its side (a process of the character can write the
// request itself): every flag is one of the command, with a value of its
// kind (text for a string or a duration, true or false for a bool), and the
// arguments of a Secret command are text, so the detector of secrets sees
// every byte the command gets.
func (s Spec) checkRequest(r localchan.Request) (localchan.Response, bool) {
	usage := func(msg string) (localchan.Response, bool) {
		return localchan.Fail(localchan.CodeUsage, msg, "usage: "+s.Usage), false
	}
	for name, v := range r.Flags {
		f, known := s.flag(name)
		if !known {
			return usage(fmt.Sprintf("unknown flag --%s", name))
		}
		var ok bool
		switch f.Kind {
		case FlagBool:
			_, ok = v.(bool)
		default:
			_, ok = v.(string)
		}
		if !ok {
			return usage(fmt.Sprintf("--%s has a value of the wrong kind", name))
		}
	}
	if s.Secret {
		if _, ok := r.Strings(); !ok {
			return usage(fmt.Sprintf("the arguments of %s are text", s.Name))
		}
	}
	return localchan.Response{}, true
}

// secret looks for text that looks like a secret in what the command
// received (rule 6 of docs/cli/convencoes.md#regras-de-segurança-da-cli): the
// same detector as the log masking. The message says where, never what.
// checkRequest already made every argument and every value text (or a
// bool).
func (g *Gate) secret(r localchan.Request) (localchan.Response, bool) {
	looks := func(s string) bool { return g.cfg.Masker.Mask(s) != s }
	args, ok := r.Strings()
	if !ok {
		return secretRefusal("the arguments"), true // never here: checkRequest refuses them first
	}
	for i, a := range args {
		if looks(a) {
			return secretRefusal(fmt.Sprintf("argument %d", i+1)), true
		}
	}
	for name, v := range r.Flags {
		if s, ok := v.(string); ok && looks(s) {
			return secretRefusal("--" + name), true
		}
	}
	return localchan.Response{}, false
}

func secretRefusal(where string) localchan.Response {
	return localchan.Fail("secret_detected", where+" looks like a secret, so nothing was sent",
		"remove the secret from the text; no command passes secrets on")
}

// Help is the engine's `help`: the commands the caller may use. Register it
// as the local handler of localchan.CmdHelp.
func (g *Gate) Help() localchan.Handler {
	return localchan.HandlerFunc(func(ctx context.Context, c *localchan.Call) localchan.Response {
		var names []string
		for _, s := range g.cfg.Commands.Visible(g.profile(ctx, c)) {
			names = append(names, s.Name)
		}
		return localchan.OK(map[string]any{"commands": names, "filtered": true})
	})
}

// States of a terminal that matter to a message typed into it
// (docs/conectividade/mensagens.md, terminal.status).
const (
	StateIdle    = "idle"
	StateBusy    = "busy"
	StateBlocked = "blocked"
	StateStuck   = "stuck"

	ReasonPermission = "permission"
	ReasonInput      = "input"
)

// CheckRaw decides whether `ask --raw` may type into a target in the given
// state (rule 3 of docs/cli/convencoes.md#regras-de-segurança-da-cli): nobody
// interrupts who works. Only a free target takes keys, and a target blocked
// on a screen the engine does not recognize (reason input), which is how the
// Master of the table answers it. A busy target is `busy` (exit 7); one
// waiting for a permission is `permission_prompt` (exit 3), because
// permissions are decided by `mestre hook` and the approval flow, never by
// keys. A stuck target takes keys too (docs/orquestracao/esperas-e-caixa-de-entrada.md);
// any other state (starting, waiting, stopped…) is refused as `busy`.
func CheckRaw(state, reason string) (localchan.Response, bool) {
	switch {
	case state == StateIdle, state == StateStuck, state == StateBlocked && reason == ReasonInput:
		return localchan.Response{}, true
	case state == StateBlocked && reason == ReasonPermission:
		return localchan.Fail("permission_prompt", "the target is waiting for a permission, so nothing was typed",
			"a permission is decided by the approval flow, never by keys"), false
	}
	return localchan.Fail("busy", fmt.Sprintf("the target is %s, so nothing was typed", state),
		"send a normal message, which waits in its inbox"), false
}
