package ptyd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/aymanbagabas/go-pty"

	"github.com/lucasndas/mestre/internal/terminal/ptywire"
)

// PTYRunner starts the agent on a real PTY (ConPTY on Windows) through
// github.com/aymanbagabas/go-pty, the same interface on both systems. What
// differs by system (how the tree is grouped, signalled and killed) is in
// runner_linux.go and runner_windows.go.
type PTYRunner struct {
	// Suite is the value of MESTRE_TEST_SUITE ("" in production); the fast
	// suite never starts a process.
	Suite string
	// SweepLogonSession (Windows) makes the forced stop also end what is left
	// of the logon session of this process, which the launcher made for this
	// terminal alone (--own-logon). It is still refused unless the session
	// is a batch logon (internal/winsvc/logonsweep).
	SweepLogonSession bool
}

var _ Runner = (*PTYRunner)(nil)

// Start starts spec on a new PTY of the fixed size, as the leader of a new
// session (Linux) or in a Job Object (Windows), with TERM=xterm-256color.
func (r *PTYRunner) Start(ctx context.Context, spec StartSpec) (Agent, error) {
	if err := ptywire.GuardSuite(r.Suite); err != nil {
		return nil, fmt.Errorf("start %s: %w", spec.Path, err)
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("start %s: %w", spec.Path, err)
	}
	p, err := pty.New()
	if err != nil {
		return nil, fmt.Errorf("open pty: %w", err)
	}
	if err := p.Resize(int(spec.Cols), int(spec.Rows)); err != nil {
		_ = p.Close()
		return nil, fmt.Errorf("size pty: %w", err)
	}
	// Not CommandContext: ctx only governs the start, the agent outlives it.
	cmd := p.Command(spec.Path, spec.Args...)
	cmd.Env = withTerm(spec.Env)
	cmd.Dir = spec.Dir
	sys, err := prepare(cmd, r.SweepLogonSession)
	if err != nil {
		_ = p.Close()
		return nil, fmt.Errorf("start %s: %w", spec.Path, err)
	}
	if err := cmd.Start(); err != nil {
		sys.abort()
		_ = p.Close()
		return nil, fmt.Errorf("start %s: %w", spec.Path, err)
	}
	if err := sys.started(cmd, p); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		sys.abort()
		_ = p.Close()
		return nil, fmt.Errorf("start %s: %w", spec.Path, err)
	}
	return &ptyAgent{p: p, cmd: cmd, sys: sys}, nil
}

// withTerm returns env with TERM=xterm-256color; an empty env means the
// environment of this process.
func withTerm(env []string) []string {
	if env == nil {
		env = os.Environ()
	}
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if k, _, _ := strings.Cut(kv, "="); !strings.EqualFold(k, "TERM") {
			out = append(out, kv)
		}
	}
	return append(out, "TERM=xterm-256color")
}

type ptyAgent struct {
	p   pty.Pty
	cmd *pty.Cmd
	sys *sysAgent

	closeOnce sync.Once
	closeErr  error
}

func (a *ptyAgent) PID() int { return a.cmd.Process.Pid }

func (a *ptyAgent) Read(b []byte) (int, error) {
	n, err := a.p.Read(b)
	return n, endOfOutput(err)
}

func (a *ptyAgent) Write(b []byte) (int, error) { return a.p.Write(b) }

func (a *ptyAgent) Wait() ExitInfo {
	_ = a.cmd.Wait() // the status is in ProcessState; an exit code is not an error here
	if a.cmd.ProcessState == nil {
		return ExitInfo{Code: -1}
	}
	return exitInfo(a.cmd.ProcessState)
}

// Signal asks the tree to end (docs/arquitetura/terminal.md#encerramento,
// step 2).
func (a *ptyAgent) Signal() error { return a.sys.signal(a) }

// Kill ends the whole tree (step 3).
func (a *ptyAgent) Kill() error { return a.sys.kill(a) }

// closePTY closes the PTY once; on Windows that is the ClosePseudoConsole
// of the signal step.
func (a *ptyAgent) closePTY() error {
	a.closeOnce.Do(func() { a.closeErr = a.p.Close() })
	return a.closeErr
}

// InTree is TreeMember: the Job Object lives in the Windows part.
func (a *ptyAgent) InTree(pid int, start string) (bool, error) { return a.sys.inTree(pid, start) }

func (a *ptyAgent) Close() error {
	_ = a.closePTY()
	a.sys.release()
	return nil
}
