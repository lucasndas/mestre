package fakepty

import (
	"context"
	"sync"

	"github.com/lucasndas/mestre/internal/terminal/ptyd"
	"github.com/lucasndas/mestre/pkg/clock"
)

// Runner is the ptyd.Runner of the fast suite: "start the agent" creates a
// fake PTY pair, and the test plays the agent through Agent.Process(). No
// process starts.
type Runner struct {
	Clock clock.Clock
	// StartErr, when set, is returned by Start.
	StartErr error

	mu     sync.Mutex
	agents []*Agent
}

var _ ptyd.Runner = (*Runner)(nil)

// Start creates the pair at the size of spec.
func (r *Runner) Start(_ context.Context, spec ptyd.StartSpec) (ptyd.Agent, error) {
	if r.StartErr != nil {
		return nil, r.StartErr
	}
	a := &Agent{Spec: spec, pair: New(r.Clock, spec.Cols, spec.Rows), done: make(chan struct{})}
	r.mu.Lock()
	r.agents = append(r.agents, a)
	r.mu.Unlock()
	return a, nil
}

// Last returns the agent started last, or nil.
func (r *Runner) Last() *Agent {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.agents) == 0 {
		return nil
	}
	return r.agents[len(r.agents)-1]
}

// Agent is a fake agent: the terminal side is read by the code under test,
// the process side (Process) is driven by the test.
type Agent struct {
	Spec ptyd.StartSpec

	pair *Pair
	once sync.Once
	done chan struct{}
	exit ptyd.ExitInfo

	mu       sync.Mutex
	signaled int
	killed   int
}

var _ ptyd.Agent = (*Agent)(nil)

// Process is the side the test writes the agent's output to and reads the
// keystrokes from.
func (a *Agent) Process() *ProcessSide { return a.pair.Process() }

// Pair returns the whole pair (transcripts, size).
func (a *Agent) Pair() *Pair { return a.pair }

// Exit makes the agent end with code.
func (a *Agent) Exit(code int) {
	a.once.Do(func() {
		a.exit = ptyd.ExitInfo{Code: code}
		close(a.done)
	})
}

// Signals returns how many times Signal and Kill were called.
func (a *Agent) Signals() (signaled, killed int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.signaled, a.killed
}

func (a *Agent) Read(b []byte) (int, error)  { return a.pair.Terminal().Read(b) }
func (a *Agent) Write(b []byte) (int, error) { return a.pair.Terminal().Write(b) }
func (a *Agent) PID() int                    { return 4242 }

// Wait blocks until Exit, Signal or Kill.
func (a *Agent) Wait() ptyd.ExitInfo {
	<-a.done
	return a.exit
}

// Signal ends the agent as SIGTERM would.
func (a *Agent) Signal() error {
	a.mu.Lock()
	a.signaled++
	a.mu.Unlock()
	a.Exit(143)
	return nil
}

// Kill ends the agent as SIGKILL would.
func (a *Agent) Kill() error {
	a.mu.Lock()
	a.killed++
	a.mu.Unlock()
	a.Exit(137)
	return nil
}

// Close hangs up the terminal.
func (a *Agent) Close() error { return a.pair.Terminal().Close() }
