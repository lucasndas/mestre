package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"

	"github.com/lucasndas/mestre/internal/hook"
	"github.com/lucasndas/mestre/internal/providers"
)

// classHookRunner runs a synchronous hook of the class the way the
// providers run theirs (sh -c on Linux, cmd /s /c on Windows): in its folder,
// with the environment of the character plus MESTRE_HOOK_EVENT and the event
// on stdin (docs/classes/classes.md#hooks-extras-da-classe). Its stdout is
// not the dispatcher's, which belongs to the provider.
type classHookRunner struct{}

func (classHookRunner) Run(ctx context.Context, h hook.ClassHook, event providers.EventKind, stdin []byte) (int, string, error) {
	c := shellCommand(ctx, h.Run)
	c.Dir = h.Dir
	c.Env = append(os.Environ(), "MESTRE_HOOK_EVENT="+string(event))
	var stderr bytes.Buffer
	c.Stdin, c.Stderr = bytes.NewReader(stdin), &stderr
	err := c.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) && ctx.Err() == nil {
		return ee.ExitCode(), stderr.String(), nil
	}
	if err == nil && ctx.Err() != nil {
		err = ctx.Err()
	}
	return 0, stderr.String(), err
}
