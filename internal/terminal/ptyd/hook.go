package ptyd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/mestre-ia/mestre/internal/terminal/ptywire"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// The asynchronous hooks of the classes run here, as the agent
// (docs/arquitetura/terminal.md#protocolo-motor--mestre-pty, message hook): the
// mestre-pty is the account of the agent, the engine is not.

// Errors of the hooks.
var (
	// ErrHookBusy: a hook is already running in this terminal.
	ErrHookBusy = errors.New("a hook is already running in this terminal")
	// ErrHookDir: the folder of the hook is not the one of the agent or inside it.
	ErrHookDir = errors.New("the folder of the hook is outside the folder of the agent")
	// ErrHookSpec: the request has no command line, or a time limit out of range.
	ErrHookSpec = errors.New("the hook request is not valid")
)

// HookSpec is a hook to run.
type HookSpec struct {
	// Run is the command line, Dir the folder, Env the environment (the one of
	// the agent) without MESTRE_HOOK_EVENT, which Event becomes.
	Run, Dir string
	Env      []string
	Event    string
	Stdin    []byte
	Timeout  time.Duration
}

// HookResult is how a hook ended: its exit code, what it wrote (stdout and
// stderr together, at most ptywire.MaxHookOutput bytes), and whether it ran
// out of time or its output was cut.
type HookResult struct {
	Exit      int
	Output    string
	TimedOut  bool
	Truncated bool
}

// HookRunner runs a hook; ShellHooks is the real one.
type HookRunner interface {
	Run(ctx context.Context, spec HookSpec) (HookResult, error)
}

// ShellHooks runs the line with the shell of the system, in a tree of its own
// that the time limit kills whole.
type ShellHooks struct {
	// Suite is the value of MESTRE_TEST_SUITE ("" in production); the fast
	// suite never starts a process.
	Suite string
	Clock clock.Clock
}

// hookWaitDelay is how long the runner waits, after the tree of the hook was
// killed or the shell ended, for the pipes to close.
const hookWaitDelay = 500 * time.Millisecond

var _ HookRunner = ShellHooks{}

// Run implements HookRunner.
func (h ShellHooks) Run(ctx context.Context, spec HookSpec) (HookResult, error) {
	if err := ptywire.GuardSuite(h.Suite); err != nil {
		return HookResult{}, fmt.Errorf("hook: %w", err)
	}
	clk := h.Clock
	if clk == nil {
		clk = clock.Real{}
	}
	rctx, cancel := clock.WithTimeout(ctx, clk, spec.Timeout)
	defer cancel()
	c := shellCommand(rctx, spec.Run)
	c.Dir = spec.Dir
	env := agentEnv(runtime.GOOS, spec.Env, spec.Dir)
	if env == nil {
		env = os.Environ()
	}
	c.Env = append(append([]string(nil), env...), "MESTRE_HOOK_EVENT="+spec.Event)
	out := &cappedBuffer{max: ptywire.MaxHookOutput}
	c.Stdin, c.Stdout, c.Stderr = bytes.NewReader(spec.Stdin), out, out
	c.WaitDelay = hookWaitDelay
	tree, err := newHookTree(c)
	if err != nil {
		return HookResult{}, err
	}
	defer tree.close()
	if err := c.Start(); err != nil {
		return HookResult{}, err
	}
	if err := tree.started(c); err != nil {
		_ = c.Process.Kill()
		_ = c.Wait()
		return HookResult{}, err
	}
	err = c.Wait()
	tree.kill() // what the hook left behind
	res := HookResult{Output: out.String(), Truncated: out.cut}
	var ee interface{ ExitCode() int }
	switch {
	case rctx.Err() != nil && ctx.Err() == nil:
		res.TimedOut = true
		return res, nil
	case ctx.Err() != nil:
		return res, ctx.Err()
	case errors.As(err, &ee):
		res.Exit = ee.ExitCode()
		return res, nil
	case errors.Is(err, errWaitDelay):
		// The hook ended but a descendant kept its output open: what it said is
		// cut and its outcome is not known — a failure, never a clean exit 0.
		return res, fmt.Errorf("the hook ended but left a process holding its output: %w", err)
	case err != nil:
		return res, err
	}
	return res, nil
}

// cappedBuffer keeps the first max bytes written and drops the rest.
type cappedBuffer struct {
	mu  sync.Mutex
	b   bytes.Buffer
	max int
	cut bool
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := len(p)
	if room := c.max - c.b.Len(); room < n {
		c.cut = true
		p = p[:max(room, 0)]
	}
	c.b.Write(p)
	return n, nil
}

// String is what was kept. The Write above reports the whole input as written
// (not the part kept), so a hook that writes a lot is never cut short by a
// failed write.
func (c *cappedBuffer) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.b.String()
}

// RunHook runs a hook as the agent: only once the agent started, one at a
// time, in the folder of the agent or inside it, with the environment the
// agent was started with.
func (s *Session) RunHook(ctx context.Context, spec HookSpec) (HookResult, error) {
	s.mu.Lock()
	state, base, env := s.state, s.startDir, s.startEnv
	s.mu.Unlock()
	if state == ptywire.StateIdle {
		return HookResult{}, ErrNotRunning
	}
	if spec.Run == "" || strings.ContainsRune(spec.Run, 0) {
		return HookResult{}, fmt.Errorf("%w: no command line", ErrHookSpec)
	}
	if spec.Timeout < ptywire.MinHookTimeout || spec.Timeout > ptywire.MaxHookTimeout {
		return HookResult{}, fmt.Errorf("%w: the time limit goes from %s to %s", ErrHookSpec, ptywire.MinHookTimeout, ptywire.MaxHookTimeout)
	}
	dir, err := hookDir(base, spec.Dir)
	if err != nil {
		return HookResult{}, err
	}
	if !s.hookMu.TryLock() {
		return HookResult{}, ErrHookBusy
	}
	defer s.hookMu.Unlock()
	// the end of the terminal (Kill, Close) ends the hook with its tree
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	s.mu.Lock()
	s.hookCancel = cancel
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.hookCancel = nil
		s.mu.Unlock()
	}()
	spec.Dir, spec.Env = dir, env
	hooks := s.cfg.Hooks
	if hooks == nil {
		hooks = ShellHooks{}
	}
	return hooks.Run(ctx, spec)
}

// hookDir is the folder of the hook: the one of the agent when none is asked,
// otherwise a folder inside it.
func hookDir(base, dir string) (string, error) {
	if dir == "" {
		return base, nil
	}
	if base == "" || !filepath.IsAbs(dir) {
		return "", ErrHookDir
	}
	rel, err := filepath.Rel(base, filepath.Clean(dir))
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", ErrHookDir
	}
	return filepath.Join(base, rel), nil
}

// cancelHook ends the hook that runs, if any.
func (s *Session) cancelHook() {
	s.mu.Lock()
	cancel := s.hookCancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}
