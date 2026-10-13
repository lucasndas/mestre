package fakeagent

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/lucasndas/mestre/internal/terminal"
	"github.com/lucasndas/mestre/internal/testkit"
	"github.com/lucasndas/mestre/internal/testkit/fakepty"
	"github.com/lucasndas/mestre/pkg/clock"
)

// MemLauncher is the in-memory launcher of the fast suite: "start a
// process" becomes a goroutine running the fake agent on a fake PTY
// (docs/qualidade/dubles.md#agente-falso-fakeagent). Programs it does not
// know fail the test as not implemented.
type MemLauncher struct {
	Clock    clock.Clock
	Hooks    HookRunner
	Commands CommandRunner
	Reporter testkit.Reporter
	// RecordingVersion pins the recordings of every agent it starts; ""
	// means the newest.
	RecordingVersion string
}

var _ terminal.Launcher = (*MemLauncher)(nil)

// Start runs spec.Path as the fake agent: claude, codex or agy by name, or
// fakeagent with --provider.
func (l *MemLauncher) Start(ctx context.Context, spec terminal.Spec) (terminal.Process, error) {
	p, ok := ProviderFromName(spec.Path)
	args := spec.Args
	if !ok && strings.TrimSuffix(filepath.Base(spec.Path), ".exe") == "fakeagent" {
		p, args, ok = providerFlag(args)
	}
	if !ok {
		msg := testkit.NotImplementedPrefix + "launch " + spec.Path
		l.Reporter.Errorf("%s", msg)
		return nil, fmt.Errorf("%s", msg)
	}
	cols, rows := spec.Cols, spec.Rows
	if cols == 0 || rows == 0 {
		cols, rows = 120, 40
	}
	pair := fakepty.New(l.Clock, cols, rows)
	ctx, cancel := context.WithCancel(ctx)
	mp := &MemProcess{pair: pair, cancel: cancel, done: make(chan struct{})}
	go func() {
		mp.err = Run(ctx, Config{
			Provider: p, Args: args, Env: spec.Env, Dir: spec.Dir,
			In: pair.Process(), Out: pair.Process(),
			Clock: l.Clock, Hooks: l.Hooks, Commands: l.Commands,
			RecordingVersion: l.RecordingVersion,
		})
		var ni *NotImplementedError
		if errors.As(mp.err, &ni) { // a double asked for what it does not model fails the test
			l.Reporter.Errorf("%s", ni)
		}
		_ = pair.Process().Close()
		close(mp.done)
	}()
	return mp, nil
}

// providerFlag extracts --provider <id> from args.
func providerFlag(args []string) (Provider, []string, bool) {
	for i, a := range args {
		v, found := strings.CutPrefix(a, "--provider=")
		if !found && a == "--provider" && i+1 < len(args) {
			v, found = args[i+1], true
			args = append(append([]string(nil), args[:i]...), args[i+2:]...)
		} else if found {
			args = append(append([]string(nil), args[:i]...), args[i+1:]...)
		}
		if found {
			p, ok := ParseProvider(v)
			return p, args, ok
		}
	}
	return "", args, false
}

// MemProcess is a fake agent started by MemLauncher.
type MemProcess struct {
	pair   *fakepty.Pair
	cancel context.CancelFunc
	once   sync.Once
	done   chan struct{}
	err    error
}

// PTY returns the terminal side.
func (p *MemProcess) PTY() terminal.PTY { return p.pair.Terminal() }

// Output returns everything the agent printed.
func (p *MemProcess) Output() string { return p.pair.Output() }

// Wait waits for the agent to end.
func (p *MemProcess) Wait() error {
	<-p.done
	return p.err
}

// Kill stops the agent and hangs up the terminal.
func (p *MemProcess) Kill() error {
	p.once.Do(func() {
		p.cancel()
		_ = p.pair.Terminal().Close()
	})
	return nil
}
