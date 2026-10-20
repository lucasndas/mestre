package characters

import (
	"context"
	"encoding/base64"
	"fmt"
	"time"
	"unicode/utf16"
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
	line, err := shellLine(rt.o.OS, command)
	if err != nil {
		return PreRunOutcome{}, err
	}
	res, err := runner.RunHook(ctx, line, s.launch.Dir, "pre_run", nil, timeout)
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

// maxCmdLine is what cmd takes in a command line (8191), with a margin.
const maxCmdLine = 8000

// shellLine is the line the terminal runs for a command of a pre-run. On Windows
// the runner of the terminal is cmd, and a command of an agent there is
// PowerShell's (the permissions read it as POSIX and as PowerShell, never as
// cmd): it runs in the PowerShell of the system, by its full path (cmd looks in
// the current folder first, which is the worktree), with the command as the text
// it was decided on (-EncodedCommand carries it with no quoting at all). cmd runs
// with /d, so the AutoRun of the account of the agent does not run first.
func shellLine(goos, command string) (string, error) {
	if goos != "windows" {
		return command, nil
	}
	u := utf16.Encode([]rune(command))
	b := make([]byte, 0, 2*len(u))
	for _, c := range u {
		b = append(b, byte(c), byte(c>>8))
	}
	line := `"` + systemPowerShell() + `" -NoProfile -NonInteractive -EncodedCommand ` + base64.StdEncoding.EncodeToString(b)
	if len(line) > maxCmdLine {
		return "", fmt.Errorf("the command is too long for a pre-run on Windows (the line of cmd is at most %d characters; the command is carried as base64 of UTF-16, about 2.7 times its size): nothing ran", maxCmdLine)
	}
	return line, nil
}
