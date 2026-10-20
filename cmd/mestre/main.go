// Command mestre is the CLI of the Mestre (docs/cli/convencoes.md), the
// engine (`mestre run`, docs/arquitetura/componentes.md) and the hook
// dispatcher (`mestre hook`, docs/provedores/despachante-de-hooks.md). It
// only assembles the process around internal/cli, internal/app and
// internal/hook: the environment, the folder and the signals. Every command
// of the CLI, and the dispatcher, talks to the engine of the machine through
// the local channel (docs/arquitetura/canal-local.md).
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/lucasndas/mestre/internal/app"
	"github.com/lucasndas/mestre/internal/cli"
	"github.com/lucasndas/mestre/internal/hook"
	"github.com/lucasndas/mestre/pkg/clock"
)

// version is the version of the binary; the release sets it with
// -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "hook" {
		// Before anything else: without MESTRE_AGENT_ID the dispatcher
		// answers at once, reading nothing.
		os.Exit(hook.Run(context.Background(), hook.Env{
			Getenv: os.Getenv, Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr, Clock: clock.Real{},
			Getwd: os.Getwd, ClassHooks: classHookRunner{},
		}, os.Args[2:]))
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	if len(os.Args) > 1 && os.Args[1] == "run" {
		code := app.Main(ctx, os.Args[2:], os.Stderr, os.LookupEnv, version)
		stop()
		os.Exit(code)
	}
	allowEngine() // Windows: the engine must be able to open this process to identify it
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error: cannot read the current folder:", err)
		stop()
		os.Exit(1)
	}
	code := cli.Run(ctx, cli.Env{Stdout: os.Stdout, Stderr: os.Stderr, Cwd: cwd, Version: version}, os.Args[1:])
	stop()
	os.Exit(code)
}
