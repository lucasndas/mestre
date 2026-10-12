// Command fakeagent is the fake agent binary of the full suite
// (docs/qualidade/dubles.md#agente-falso-fakeagent). It imitates Claude
// Code, Codex or Antigravity, chosen by --provider or by the name it was
// called with (claude, codex, agy, with .exe on Windows). It is not part of
// the release packages.
package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"runtime"

	"github.com/lucasndas/mestre/internal/testkit/fakeagent"
	"github.com/lucasndas/mestre/pkg/clock"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx, os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, argv []string) error {
	args := argv[1:]
	p, ok := fakeagent.ProviderFromName(argv[0])
	for i := 0; !ok && i+1 < len(args); i++ {
		if args[i] == "--provider" {
			if p, ok = fakeagent.ParseProvider(args[i+1]); ok {
				args = append(args[:i:i], args[i+2:]...)
			}
		}
	}
	if !ok {
		return errors.New("fakeagent: call it claude, codex or agy, or pass --provider claude|codex|antigravity")
	}
	dir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("fakeagent: %w", err)
	}
	return fakeagent.Run(ctx, fakeagent.Config{
		Provider: p, Args: args, Env: os.Environ(), Dir: dir,
		In: os.Stdin, Out: os.Stdout, Clock: clock.Real{},
		Hooks: shell{}, Commands: shell{},
	})
}

// shell runs hooks and commands the way the providers do: sh -c on Linux,
// cmd /c on Windows.
type shell struct{}

func command(ctx context.Context, line, dir string, env []string) *exec.Cmd {
	var c *exec.Cmd
	if runtime.GOOS == "windows" {
		c = exec.CommandContext(ctx, "cmd", "/c", line)
	} else {
		c = exec.CommandContext(ctx, "sh", "-c", line)
	}
	c.Dir, c.Env = dir, env
	return c
}

func (shell) RunHook(ctx context.Context, call fakeagent.HookCall) (fakeagent.HookResult, error) {
	c := command(ctx, call.Command, call.Dir, call.Env)
	var out, errb bytes.Buffer
	c.Stdin, c.Stdout, c.Stderr = bytes.NewReader(call.Stdin), &out, &errb
	err := c.Run()
	res := fakeagent.HookResult{Stdout: out.Bytes(), Stderr: errb.Bytes()}
	var ee *exec.ExitError
	if errors.As(err, &ee) && ctx.Err() == nil {
		res.ExitCode = ee.ExitCode()
		return res, nil
	}
	if err != nil {
		return res, fmt.Errorf("hook %q: %w", call.Command, err)
	}
	return res, nil
}

func (shell) RunCommand(ctx context.Context, line, dir string, env []string) (string, int, error) {
	out, err := command(ctx, line, dir, env).CombinedOutput()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return string(out), ee.ExitCode(), nil
	}
	if err != nil {
		return string(out), -1, fmt.Errorf("run %q: %w", line, err)
	}
	return string(out), 0, nil
}
