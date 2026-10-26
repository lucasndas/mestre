package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/mestre-ia/mestre/internal/hook"
	"github.com/mestre-ia/mestre/internal/providers"
)

// hookWaitDelay is how long the runner waits, after the time limit ended the
// tree of the hook, for the pipes to close: a process that escaped the tree
// and still holds stderr does not keep the call of the provider waiting.
const hookWaitDelay = 500 * time.Millisecond

// classHookRunner runs a synchronous hook of the class the way the
// providers run theirs (sh -c on Linux, cmd /s /c on Windows): in its folder,
// with the environment of the character plus MESTRE_HOOK_EVENT and the event
// on stdin (docs/classes/classes.md#hooks-extras-da-classe). Its stdout is
// not the dispatcher's, which belongs to the provider.
//
// The hook runs in a tree of its own (a process group on Unix, a Job Object
// on Windows): the time limit kills the whole tree, not only the shell, and
// whatever the hook left running when it ended goes with it, so no
// descendant holds stderr past the limit. On Linux that includes a
// descendant that left the group with setsid: the dispatcher adopts the
// orphans (subreaper) and sweeps them (orphans_linux.go). A hook that ends
// with 0 while a descendant still holds its stderr is a failure, not a
// success (docs/provedores/despachante-de-hooks.md#uma-chamada).
type classHookRunner struct{}

func (classHookRunner) Run(ctx context.Context, h hook.ClassHook, event providers.EventKind, stdin []byte) (int, string, error) {
	c := shellCommand(ctx, h.Run)
	c.Dir = h.Dir
	c.Env = append(os.Environ(), "MESTRE_HOOK_EVENT="+string(event))
	var stderr bytes.Buffer
	c.Stdin, c.Stderr = bytes.NewReader(stdin), &stderr
	c.WaitDelay = hookWaitDelay
	tree, err := newHookTree(c)
	if err != nil {
		return 0, "", err
	}
	defer tree.close()
	if err := c.Start(); err != nil {
		return 0, "", err
	}
	if err := tree.started(c); err != nil {
		_ = c.Process.Kill()
		_ = c.Wait()
		return 0, stderr.String(), err
	}
	err = c.Wait()
	tree.kill() // what the hook left behind
	var ee *exec.ExitError
	if errors.As(err, &ee) && ctx.Err() == nil {
		return ee.ExitCode(), stderr.String(), nil
	}
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	if errors.Is(err, exec.ErrWaitDelay) {
		// The hook exited with 0 but a descendant kept its stderr open, so
		// what the hook said is cut and its outcome is not known: a failure,
		// never a clean exit 0 (a blocking hook then denies).
		err = fmt.Errorf("the hook ended but left a process holding its output: %w", err)
	}
	return 0, stderr.String(), err
}
