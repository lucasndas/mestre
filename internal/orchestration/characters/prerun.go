package characters

import (
	"context"
	"fmt"
	"time"
)

// PreRunOutcome is what a command run as a character came to.
type PreRunOutcome struct {
	Output   string
	ExitCode int
	TimedOut bool
	// Stderr is the output of a command that failed.
	Stderr string
}

// PreRunCheck is the command a pre-run asks to run.
type PreRunCheck struct {
	Agent, Table, Dir, Command string
}

// PreRun runs a command as the character, in its worktree, without interaction
// (the pre-run of a ritual): the terminal asks its mestre-pty, which is the
// account of the agent, so the command is never run as another account. The
// decision of the permissions on the command is the Citadel's, before; what is
// kept of the output is cut at maxOutput bytes (0: all).
func (rt *Runtime) PreRun(ctx context.Context, agentID, command string, timeout time.Duration, maxOutput int) (PreRunOutcome, error) {
	s := rt.session(agentID)
	if s == nil {
		return PreRunOutcome{}, noProcess(agentID)
	}
	s.mu.Lock()
	proc := s.proc
	s.mu.Unlock()
	runner, ok := proc.(HookRunner)
	if !ok {
		return PreRunOutcome{}, fmt.Errorf("the terminal of %s cannot run a command as its agent", agentID)
	}
	if rt.o.PreRunGate == nil {
		return PreRunOutcome{}, fmt.Errorf("this machine decides no pre-run: nothing ran")
	}
	if err := rt.o.PreRunGate(ctx, PreRunCheck{Agent: agentID, Table: s.launch.Table, Dir: s.launch.Dir, Command: command}); err != nil {
		return PreRunOutcome{}, err
	}
	res, err := runner.RunHook(ctx, command, s.launch.Dir, "pre_run", nil, timeout)
	if err != nil {
		return PreRunOutcome{}, err
	}
	out := res.Output
	if maxOutput > 0 && len(out) > maxOutput {
		out = out[:maxOutput]
	}
	o := PreRunOutcome{Output: out, ExitCode: res.Exit, TimedOut: res.TimedOut}
	if res.Exit != 0 || res.TimedOut {
		o.Stderr = out // the terminal gives both streams in one: the tail of it is what the failure shows
	}
	return o, nil
}
