// Command testrun runs the test suites and measures them
// (docs/qualidade/testes.md#executor-testrun):
//
//	go run ./tools/testrun fast|full|heavy|affected|quarantine [-count n] [-shuffle]
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"

	"github.com/lucasndas/mestre/pkg/clock"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout))
}

func run(args []string, out io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(out, "usage: go run ./tools/testrun fast|full|heavy|affected|quarantine [-count n] [-shuffle]")
		return 2
	}
	mode := args[0]
	fl := flag.NewFlagSet("testrun "+mode, flag.ContinueOnError)
	fl.SetOutput(out)
	count := fl.Int("count", 1, "go test -count (the nightly flaky hunt uses 5)")
	shuffle := fl.Bool("shuffle", false, "go test -shuffle=on")
	if err := fl.Parse(args[1:]); err != nil {
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(out, err)
		return 2
	}
	r := &Runner{
		Exec: osExec{root: root}, Clock: clock.Real{}, FS: os.DirFS(root), Out: out,
		Count: *count, Shuffle: *shuffle, Getenv: os.Getenv, AppendFile: appendFile,
		WriteFile: func(p string, data []byte) error {
			full := filepath.Join(root, filepath.FromSlash(p))
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				return err
			}
			return os.WriteFile(full, data, 0o644)
		},
	}
	switch mode {
	case "fast", "full", "heavy":
		return r.RunSuite(ctx, Suites[mode])
	case "affected":
		return r.Affected(ctx)
	case "quarantine":
		return r.Quarantine(ctx)
	}
	fmt.Fprintf(out, "testrun: unknown mode %q\n", mode)
	return 2
}

// osExec runs real commands from the repository root.
type osExec struct{ root string }

func (e osExec) Run(ctx context.Context, c Cmd, stdout io.Writer) (int, error) {
	cmd := exec.CommandContext(ctx, c.Name, c.Args...)
	cmd.Dir = filepath.Join(e.root, c.Dir)
	cmd.Env = append(os.Environ(), c.Env...)
	cmd.Stdout = stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode(), nil
	}
	if err != nil {
		return -1, fmt.Errorf("run %s: %w", c, err)
	}
	return 0, nil
}

// appendFile appends data to the file at path, creating it if needed (the
// GitHub Actions job summary).
func appendFile(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
