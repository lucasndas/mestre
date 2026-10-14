// Package providers holds the provider adapters
// (docs/provedores/adaptador.md): the only place where Claude Code, Codex
// and Antigravity differ. The engine, the classes and the session zero talk
// to the Adapter contract alone; terminals without an agent (bash,
// powershell bases) use the null adapter.
//
// Besides the adapters, the package has the engine-side pieces the contract
// describes: Turns completes the normalized events with what the payload
// does not carry, and Supervisor runs the timeouts of the adapter document
// (start screen, hooks after the first prompt, delivery confirmation). The
// files of one provider are claude*.go, codex.go and antigravity.go with their
// native_*.go (the second barrier of the permissions).
package providers

import (
	"errors"
	"fmt"
	"sort"
)

// ID names a provider, as in the sheet's `agent` field.
type ID string

// Providers. Null is the adapter of terminals without an agent.
const (
	ClaudeCode  ID = "claude-code"
	Codex       ID = "codex"
	Antigravity ID = "antigravity"
	Null        ID = ""
)

// Adapter is the contract of docs/provedores/adaptador.md#contrato.
//
// The operations of the first group are the ones the adapter document
// specifies in full: BuildLaunch, NormalizeHook, Screens and TypeMessage,
// and FormatResponse, whose formats are the hook dispatcher's
// (despachante-de-hooks.md). The second group joins this interface with the
// documents that specify it: HookFiles, PreTrust and DeliverMestreLayer with
// the provider documents (claude-code.md, codex.md, antigravity.md) and
// ReadUsage with the provider documents and the usage limits
// (limites-de-uso.md). The contract rules hold for all of them: nothing
// writes AGENTS.md, every generation is idempotent and merges only the
// managed part, and no secret goes in a command-line argument.
type Adapter interface {
	// Info describes the provider: id and what its hooks do not cover.
	Info() Info
	// BuildLaunch builds the command that starts, or resumes, a character.
	BuildLaunch(req LaunchRequest) (Launch, error)
	// NormalizeHook turns the stdin of one `mestre hook` call into the
	// internal format. A native event the map does not use (PostInvocation)
	// comes back with Event == EventNone; an unknown one is ErrUnknownEvent.
	NormalizeHook(in HookInput) (Event, error)
	// FormatResponse turns the reply to one call of a native event into
	// the stdout and the exit code the provider expects. It is pure: the
	// dispatcher calls it also without MESTRE_AGENT_ID, for the neutral
	// answer, and with a native event the adapter does not know.
	FormatResponse(nativeEvent string, r Reply) HookOutput
	// Screens reads the rendered screen: readiness and known screens.
	Screens(in ScreenInput) ScreenResult
	// TypeMessage builds the bytes that deliver text to the agent.
	TypeMessage(text string) ([]byte, error)

	// DeliverMestreLayer gives the arguments and files that deliver the
	// MESTRE.md of a character; the arguments go in
	// LaunchRequest.ProviderArgs.
	DeliverMestreLayer(req LayerRequest) (Layer, error)
	// HookFiles gives the hook files of a worktree or account profile and
	// the managed part of each.
	HookFiles(req HookFilesRequest) ([]ManagedFile, error)
	// PreTrust trusts a worktree in the account profile; idempotent.
	PreTrust(req PreTrustRequest) (Trust, error)
	// ReadUsage turns the last usage source of a character into the
	// normalized usage record.
	ReadUsage(req UsageRequest) (Usage, error)
}

// Info is what the engine needs to know about a provider beyond the
// operations.
type Info struct {
	ID ID
	// Hooks is false for the null adapter: no hook ever arrives.
	Hooks bool
	// Reduced, when set, is why every character of this provider has
	// reduced monitoring: events of the map the provider does not have
	// (docs/provedores/adaptador.md#monitoramento-reduzido).
	Reduced string
	// DerivedSessionStart is true when the provider has no session start
	// event and the first turn_start of a session stands for it (Turns).
	DerivedSessionStart bool
}

// ErrUnknownEvent is a native event the adapter does not know: the
// dispatcher logs it and answers neutrally (despachante-de-hooks.md).
var ErrUnknownEvent = errors.New("unknown native event")

// Registry holds the adapters by provider id.
type Registry struct{ m map[ID]Adapter }

// NewRegistry returns the adapters of the three providers and the null one.
func NewRegistry(opts Options) *Registry {
	r := &Registry{m: map[ID]Adapter{}}
	for _, a := range []Adapter{NewClaudeCode(opts), NewCodex(opts), NewAntigravity(opts), NewNull()} {
		r.m[a.Info().ID] = a
	}
	return r
}

// For returns the adapter of a sheet's agent; "" is the null adapter.
func (r *Registry) For(id ID) (Adapter, error) {
	a, ok := r.m[id]
	if !ok {
		return nil, fmt.Errorf("provider %q: no adapter (known: %v)", id, r.ids())
	}
	return a, nil
}

func (r *Registry) ids() []string {
	var out []string
	for id := range r.m {
		if id != Null {
			out = append(out, string(id))
		}
	}
	sort.Strings(out)
	return out
}
