package providers

import (
	"context"
	"errors"
	"strings"
)

// LayerChannel is the channel that carries the Mestre layer of a
// character (docs/sessao-zero/personagem.md#canal-de-entrega-por-provedor).
type LayerChannel string

// The channels: the main one of the provider, the hook of the start of the
// turn, and the first message, the universal reserve.
const (
	LayerMain         LayerChannel = ""
	LayerHook         LayerChannel = "hook"
	LayerFirstMessage LayerChannel = "first-message"
)

// LayerFallbacks are the "Se falhar" paths of the items of "Verificar na
// implementação" about the layer, switched on by the conformance test
// (docs/provedores/conformidade.md) when the default fails. The zero value
// is the default of each item.
type LayerFallbacks struct {
	// Channel replaces the main channel.
	Channel LayerChannel
	// ReadRootAgents makes the layer tell the agent to read the AGENTS.md
	// of the workspace root before anything else, for a provider that
	// loads it only when a file is touched.
	ReadRootAgents bool
}

// Command is a command to run to completion.
type Command struct {
	Path string
	Args []string
	Env  []string
	Dir  string
}

// ExecFunc runs a Command and returns what it printed on stdout. Processes
// start only in the designated layers (the launcher runs it as the account of
// the agents), so the engine gives the adapters the function; tests give a
// fake.
type ExecFunc func(ctx context.Context, c Command) ([]byte, error)

// ErrNoExec is a ReadUsage that needs to run a command without Options.Exec.
var ErrNoExec = errors.New("no command runner (Options.Exec)")

// envValue is the last value of key in KEY=VALUE pairs (the one a process
// sees), and whether it is set.
func envValue(env []string, key string) (string, bool) {
	v, ok := "", false
	for _, kv := range env {
		if k, val, found := strings.Cut(kv, "="); found && k == key {
			v, ok = val, true
		}
	}
	return v, ok
}
