package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/mestre-ia/mestre/internal/realaccount"
)

// built is what the tool builds for the tests.
type built struct {
	dir  string
	test string // the test binary of internal/realaccount
	pty  string // the mestre-pty
}

// build compiles the test binary and the mestre-pty into a new folder, with
// cgo off like the release binaries: the launcher empties the ambient
// capabilities with a raw system call on every thread, which a cgo program
// cannot do. The folder is the caller's to remove.
func build(ctx context.Context, out io.Writer) (built, error) {
	dir, err := os.MkdirTemp("", "realaccount-build")
	if err != nil {
		return built{}, err
	}
	exe := ""
	if runtime.GOOS == "windows" {
		exe = ".exe"
	}
	b := built{dir: dir, test: filepath.Join(dir, "realaccount.test"+exe), pty: filepath.Join(dir, "mestre-pty"+exe)}
	env := []string{"CGO_ENABLED=0"}
	if err := runCmd(ctx, out, env, "go", "test", "-c", "-tags", "realaccount", "-o", b.test, "./internal/realaccount"); err != nil {
		return b, fmt.Errorf("build the test binary: %w", err)
	}
	if err := runCmd(ctx, out, env, "go", "build", "-o", b.pty, "-ldflags", "-X main.version="+realaccount.PTYVersion, "./cmd/mestre-pty"); err != nil {
		return b, fmt.Errorf("build the mestre-pty: %w", err)
	}
	return b, nil
}

// runCmd executes a command, its output going to out.
func runCmd(ctx context.Context, out io.Writer, env []string, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %v: %w", name, args, err)
	}
	return nil
}

// output executes a command and returns what it printed.
func output(ctx context.Context, name string, args ...string) (string, error) { //nolint:unused // used by the Linux files
	b, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return string(b), fmt.Errorf("%s %v: %w: %s", name, args, err, b)
	}
	return string(b), nil
}
