package ffmpeg_test

import (
	"errors"
	"testing"

	"github.com/lucasndas/mestre/internal/attachments/ffmpeg"
)

// The fast suite never starts a process.
func TestTheFastSuiteDoesNotStartAProcess(t *testing.T) {
	t.Setenv(ffmpeg.EnvTestSuite, "fast")
	if _, err := ffmpeg.Run(t.Context(), "ffmpeg", nil, nil); !errors.Is(err, ffmpeg.ErrExternalProcessInFastSuite) {
		t.Fatalf("err = %v", err)
	}
}
