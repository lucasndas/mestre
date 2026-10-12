// Package terminal is the single layer that starts processes
// (docs/arquitetura/terminal.md). M0 delivers only the launcher contract and
// the fast-suite guard; the PTY/ConPTY implementation and the emulator arrive
// in M1, behind the same interfaces.
package terminal

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
)

// EnvTestSuite names the suite being run; set by tools/testrun.
const EnvTestSuite = "MESTRE_TEST_SUITE"

// ErrExternalProcessInFastSuite is returned when the fast suite asks for a
// real process (docs/qualidade/testes.md#regras-e-erros).
var ErrExternalProcessInFastSuite = errors.New("external process in fast suite: use the fake PTY")

// PTY is the terminal side of a pseudo-terminal: it reads what the process
// prints and writes what the user types.
type PTY interface {
	io.ReadWriteCloser
	Resize(cols, rows uint16) error
}

// Spec describes a process to start.
type Spec struct {
	Path string
	Args []string
	Env  []string // KEY=VALUE, complete environment
	Dir  string
	Cols uint16
	Rows uint16
}

// Process is a started process attached to a PTY.
type Process interface {
	PTY() PTY
	// Wait blocks until the process ends and returns its error.
	Wait() error
	Kill() error
}

// Launcher starts processes. The in-memory launcher of internal/testkit
// replaces it in the fast suite (docs/qualidade/dubles.md).
type Launcher interface {
	Start(ctx context.Context, spec Spec) (Process, error)
}

// GuardSuite returns ErrExternalProcessInFastSuite when suite is "fast".
func GuardSuite(suite string) error {
	if suite == "fast" {
		return ErrExternalProcessInFastSuite
	}
	return nil
}

// ExecLauncher starts real processes. Until M1 it connects them by pipes
// instead of a PTY; Resize is a no-op.
type ExecLauncher struct {
	// Suite is the value of MESTRE_TEST_SUITE ("" in production).
	Suite string
}

// NewExecLauncher reads the suite from getenv (os.Getenv in main).
func NewExecLauncher(getenv func(string) string) *ExecLauncher {
	return &ExecLauncher{Suite: getenv(EnvTestSuite)}
}

var _ Launcher = (*ExecLauncher)(nil)

// Start starts spec, or refuses in the fast suite.
func (l *ExecLauncher) Start(ctx context.Context, spec Spec) (Process, error) {
	if err := GuardSuite(l.Suite); err != nil {
		return nil, fmt.Errorf("start %s: %w", spec.Path, err)
	}
	cmd := exec.CommandContext(ctx, spec.Path, spec.Args...)
	cmd.Env = spec.Env
	cmd.Dir = spec.Dir
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	cmd.Stdin = inR
	cmd.Stdout = outW
	cmd.Stderr = outW
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", spec.Path, err)
	}
	p := &execProcess{cmd: cmd, pty: &pipePTY{r: outR, w: inW}, done: make(chan struct{})}
	go func() {
		p.err = cmd.Wait()
		_ = outW.Close()
		_ = inR.Close()
		close(p.done)
	}()
	return p, nil
}

type execProcess struct {
	cmd  *exec.Cmd
	pty  *pipePTY
	done chan struct{}
	err  error
}

func (p *execProcess) PTY() PTY { return p.pty }

func (p *execProcess) Wait() error {
	<-p.done
	return p.err
}

func (p *execProcess) Kill() error {
	if err := p.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("kill %s: %w", p.cmd.Path, err)
	}
	return nil
}

type pipePTY struct {
	r *io.PipeReader
	w *io.PipeWriter
}

func (p *pipePTY) Read(b []byte) (int, error)  { return p.r.Read(b) }
func (p *pipePTY) Write(b []byte) (int, error) { return p.w.Write(b) }
func (p *pipePTY) Resize(_, _ uint16) error    { return nil }

// Close ends the input (the process sees EOF) and drains the remaining
// output in the background, so the process's writes never hit a closed pipe
// and Wait reports the real exit status. The output pipe is closed by the
// wait goroutine when the process exits, which also ends the drain.
func (p *pipePTY) Close() error {
	err := p.w.Close()
	go func() { _, _ = io.Copy(io.Discard, p.r) }()
	return err
}
