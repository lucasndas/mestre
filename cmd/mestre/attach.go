package main

import (
	"context"
	"io"

	"github.com/mestre-ia/mestre/internal/app"
	"github.com/mestre-ia/mestre/internal/attach"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// attachMain is `mestre attach`: the command of people, a client of the panel
// (docs/cli/attach.md). It returns the exit code.
func attachMain(ctx context.Context, args []string, stdout, stderr io.Writer, lookupEnv func(string) (string, bool)) int {
	return attach.Run(ctx, attach.Env{
		Stdout: stdout, Stderr: stderr, LookupEnv: lookupEnv, Console: attach.OSConsole(), Clock: clock.Real{},
		Folders: func(instance, dir string) (string, int, error) {
			paths := app.DirPaths(dir)
			if dir == "" {
				if instance == "" {
					instance = app.DefaultInstance
				}
				var err error
				if paths, err = app.DefaultPaths(instance); err != nil {
					return "", 0, err
				}
			}
			port := 0
			if cfg, err := app.LoadConfig(paths); err == nil {
				port = cfg.EnginePort
			}
			return paths.Config, port, nil
		},
	}, args)
}
