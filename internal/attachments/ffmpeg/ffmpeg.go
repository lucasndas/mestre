// Package ffmpeg is the only place of the attachments that starts a process: it
// runs the ffmpeg of the machine with the audio on the standard input and
// returns what it writes to the standard output. It is a layer designated to
// start processes, so it carries the guard of the fast suite
// (docs/qualidade/testes.md#regras-e-erros): there the conversion is the fake of
// the test.
package ffmpeg

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// EnvTestSuite names the suite being run; set by tools/testrun.
const EnvTestSuite = "MESTRE_TEST_SUITE"

// ErrExternalProcessInFastSuite is returned when the fast suite asks for a real process.
var ErrExternalProcessInFastSuite = errors.New("external process in fast suite: use the fake converter")

// maxOutput is the most audio ffmpeg may give back (a WAV of an audio of the
// size of the biggest attachment, 20 MB of Opus, is far below it).
const maxOutput = 1 << 30

// Run starts the program with the arguments and the stdin, and returns its stdout.
func Run(ctx context.Context, name string, args []string, stdin []byte) ([]byte, error) {
	if os.Getenv(EnvTestSuite) == "fast" {
		return nil, ErrExternalProcessInFastSuite
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = bytes.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &capped{w: &stdout, left: maxOutput}, &capped{w: &stderr, left: 4096}
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// capped keeps the first bytes of what a program writes, and drops the rest.
type capped struct {
	w    *bytes.Buffer
	left int
}

func (c *capped) Write(p []byte) (int, error) {
	n := len(p)
	if c.left <= 0 {
		return n, nil
	}
	if len(p) > c.left {
		p = p[:c.left]
	}
	c.left -= len(p)
	c.w.Write(p)
	return n, nil
}
