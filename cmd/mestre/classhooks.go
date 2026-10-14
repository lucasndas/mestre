package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"time"

	"github.com/lucasndas/mestre/internal/hook"
	"github.com/lucasndas/mestre/internal/providers"
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
// descendant holds stderr past the limit (docs/provedores/despachante-de-hooks.md#uma-chamada).
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
	return 0, stderr.String(), err
}
