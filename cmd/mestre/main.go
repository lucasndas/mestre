// Command mestre is the CLI of the Mestre (docs/cli/convencoes.md). It only
// assembles the process around internal/cli: the environment, the folder and
// the signals. Every command talks to the engine of the machine through the
// local channel (docs/arquitetura/canal-local.md).
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"github.com/lucasndas/mestre/internal/cli"
)

// version is the version of the binary; the release sets it with
// -ldflags "-X main.version=...".
var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
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
