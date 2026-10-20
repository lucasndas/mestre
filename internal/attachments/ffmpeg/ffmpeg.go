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

// MaxOutput is the most audio ffmpeg may give back: a WAV 16 kHz mono of the
// longest audio the engine transcribes (15 minutes), with some room. A hostile
// file that decodes into hours of silence is stopped here.
const MaxOutput = 15*60*32000 + 1<<20

// ErrOutputTooLarge is the conversion that wanted to give back more than MaxOutput.
var ErrOutputTooLarge = errors.New("ffmpeg: the converted audio is too long")

// Run starts the program with the arguments and the stdin, and returns its stdout.
func Run(ctx context.Context, name string, args []string, stdin []byte) ([]byte, error) {
	if os.Getenv(EnvTestSuite) == "fast" {
		return nil, ErrExternalProcessInFastSuite
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = bytes.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	out := &capped{w: &stdout, left: MaxOutput, strict: true}
	cmd.Stdout, cmd.Stderr = out, &capped{w: &stderr, left: 4096}
	if err := cmd.Run(); err != nil {
		if out.over {
			return nil, ErrOutputTooLarge
		}
		return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// capped keeps the first bytes of what a program writes, and drops the rest.
type capped struct {
	w    *bytes.Buffer
	left int
	// strict makes the write fail when the limit is passed (the program is
	// stopped); otherwise the rest is dropped.
	strict bool
	over   bool
}

func (c *capped) Write(p []byte) (int, error) {
	n := len(p)
	if len(p) > c.left && c.strict {
		c.over = true
		return 0, ErrOutputTooLarge
	}
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
