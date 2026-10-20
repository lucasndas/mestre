package terminal

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/lucasndas/mestre/internal/terminal/ptywire"
	"github.com/lucasndas/mestre/pkg/clock"
)

// Default waits of the termination steps
// (docs/arquitetura/terminal.md#encerramento).
const (
	DefaultGracefulWait  = 10 * time.Second
	DefaultTerminateWait = 10 * time.Second
	// forcedWait bounds each action of the forced step.
	forcedWait = 10 * time.Second
)

// StopStep is the step at which the agent ended.
type StopStep int

// The steps of the termination, in order.
const (
	StepGraceful StopStep = iota + 1 // the provider's own exit command
	StepSignal                       // SIGTERM to the session; ClosePseudoConsole
	StepForced                       // the whole tree is killed
)

func (s StopStep) String() string {
	switch s {
	case StepGraceful:
		return "graceful"
	case StepSignal:
		return "signal"
	case StepForced:
		return "forced"
	}
	return fmt.Sprintf("step(%d)", int(s))
}

// StopPlan is how to end a terminal.
type StopPlan struct {
	// Graceful asks the agent to exit normally (the adapter types the
	// provider's exit command). Nil skips step 1.
	Graceful func(ctx context.Context, t *Terminal) error
	// GracefulWait and TerminateWait default to 10 s.
	GracefulWait, TerminateWait time.Duration
}

// Stop ends the terminal in the three steps of the document: the graceful
// exit, the signal to the session (or ClosePseudoConsole), and the forced
// kill of the whole tree. It returns the step at which the agent ended. The
// tree is swept at the end whatever the step, because a descendant may have
// outlived the agent or ignored the signal: this layer guarantees nothing
// is left, not even the mestre-pty. Afterwards the socket is removed and
// the registry row says "stopped". The waits use the engine's clock.
func (t *Terminal) Stop(ctx context.Context, plan StopPlan) (StopStep, error) {
	t.stopping.Store(true)
	gw, tw := plan.GracefulWait, plan.TerminateWait
	if gw == 0 {
		gw = DefaultGracefulWait
	}
	if tw == 0 {
		tw = DefaultTerminateWait
	}
	var errs []error
	step := StepForced
	switch {
	case plan.Graceful != nil && t.act(ctx, gw, func() {
		if err := plan.Graceful(ctx, t); err != nil {
			errs = append(errs, fmt.Errorf("graceful exit: %w", err))
		}
	}):
		step = StepGraceful
	case t.act(ctx, tw, func() {
		if err := t.client.Stop(ctx, ptywire.StopSignal); err != nil && !t.ended() {
			errs = append(errs, fmt.Errorf("signal: %w", err))
		}
	}):
		step = StepSignal
	}
	if err := t.sweep(ctx, step == StepForced); err != nil {
		errs = append(errs, err)
	}
	t.finish(ctx)
	return step, errors.Join(errs...)
}

// act runs f and waits up to d for the agent to end (or the mestre-pty to
// disappear).
func (t *Terminal) act(ctx context.Context, d time.Duration, f func()) bool {
	timer := t.m.clk.NewTimer(d)
	defer timer.Stop()
	f()
	select {
	case <-t.client.Exited():
		return true
	case <-t.client.Done():
		return true
	case <-timer.C():
		return false
	case <-ctx.Done():
		return false
	}
}

func (t *Terminal) ended() bool {
	select {
	case <-t.client.Done():
		return true
	case <-t.client.Exited():
		return true
	default:
		return false
	}
}

// sweep leaves nothing of the terminal alive. The log is completed first,
// because the output of the dying tree is still the agent's last words; then
// the mestre-pty is asked to kill the tree (forced step), the cgroup of the
// terminal is killed (it reaches whoever left the session with setsid -f,
// and the mestre-pty itself) and the mestre-pty and the sessions of the
// agent are ended by PID, which covers Linux without a cgroup and a
// mestre-pty that does not answer (the engine has no right over the agents'
// processes there, so the launcher does it: TreeKiller). On Windows the Job Object holds the
// tree: ending the mestre-pty ends it.
func (t *Terminal) sweep(ctx context.Context, forced bool) error {
	var errs []error
	if !t.disconnected() {
		fctx, cancel := clock.WithTimeout(ctx, t.m.clk, forcedWait)
		_ = t.FlushLog(fctx)
		if forced {
			_ = t.client.Stop(fctx, ptywire.StopKill)
		}
		cancel()
	}
	if err := t.m.cfg.Spawner.KillCgroup(ctx, t.id); err != nil {
		errs = append(errs, err)
	}
	if err := t.m.killTree(ctx, t.id, t.proc, []int{t.agentPID}); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func (t *Terminal) disconnected() bool {
	select {
	case <-t.client.Done():
		return true
	default:
		return false
	}
}

// finish releases the terminal after its tree ended: the log closes, the
// socket goes away and the row says "stopped".
func (t *Terminal) finish(ctx context.Context) {
	t.Detach()
	t.m.revokeAccess(t.id, t.rec.LogonSID)
	_ = os.Remove(t.rec.SocketPath)
	if err := t.m.reg.setStatus(ctx, t.id, StatusStopped, t.m.clk.Now()); err != nil {
		t.m.log.Warn("terminal stopped but its row was not updated", "terminal", t.id, "error", err)
	}
}
