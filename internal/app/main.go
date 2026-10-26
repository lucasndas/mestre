package app

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"

	"github.com/mestre-ia/mestre/internal/logging"
)

// Main is `mestre run [--instance <inst>] [--dir <pasta>]`: the engine of the
// instance in the foreground, until ctx ends (docs/arquitetura/componentes.md).
// The service that runs it is milestone 7. --dir puts every folder of the
// instance under one folder, as the full suite and development use it.
// lookupEnv is os.LookupEnv. It returns the exit code.
func Main(ctx context.Context, args []string, stderr io.Writer, lookupEnv func(string) (string, bool), version string) int {
	// present is enough, even empty: an agent never starts the engine
	// (docs/arquitetura/componentes.md#mestre)
	if _, agent := lookupEnv("MESTRE_AGENT_ID"); agent {
		fmt.Fprintln(stderr, "error: an agent never starts the engine (MESTRE_AGENT_ID is in the environment)")
		return 3
	}
	getenv := func(k string) string { v, _ := lookupEnv(k); return v }
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	instance := fs.String("instance", DefaultInstance, "the instance of the Mestre")
	dir := fs.String("dir", "", "every folder of the instance under this folder")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintln(stderr, "usage: mestre run [--instance <inst>] [--dir <folder>]")
		return 2
	}
	paths := DirPaths(*dir)
	if *dir == "" {
		var err error
		if paths, err = DefaultPaths(*instance); err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 2
		}
	}
	cfg, err := LoadConfig(paths)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	deps := Deps{Version: version, HostOrchestration: true, Integration: StandardIntegration{}}
	after, cleanup, err := testHooks(ctx, &deps, cfg, getenv)
	defer cleanup()
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	// one masker for the log and the vault: a secret the vault opens is
	// hidden in every line after it
	deps.Masker = logging.NewMasker()
	deps.Logger = logging.New(stderr, deps.Masker, logging.Options{Level: slog.LevelInfo, Clock: deps.Clock})
	e, err := New(cfg, paths, deps)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	if err := after(e); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	if err := e.Run(ctx); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	return 0
}
