package terminal

import (
	"bytes"
	"fmt"
	"io"
	"sync"

	"github.com/lucasndas/mestre/internal/logging"
)

// maxLogLine caps a line that never ends (a progress bar redrawn with \r):
// it is masked and written when it grows past this size.
const maxLogLine = 64 << 10

// LogWriter turns terminal output into the terminal log: one line at a time,
// escape sequences removed, secrets masked (docs/arquitetura/seguranca.md
// #mascaramento-em-logs). The engine writes it from what the mestre-pty
// sends, so the masking uses the vault values the mestre-pty never sees; the
// buffer and the live stream keep the raw bytes. Rotation belongs to the
// file behind w.
type LogWriter struct {
	w      io.Writer
	masker *logging.Masker

	mu      sync.Mutex
	partial []byte
	err     error // first write error; later lines are dropped, not retried
}

// NewLogWriter returns a LogWriter over w. A nil masker still strips the
// escape sequences and applies the known secret formats.
func NewLogWriter(w io.Writer, masker *logging.Masker) *LogWriter {
	if masker == nil {
		masker = logging.NewMasker()
	}
	return &LogWriter{w: w, masker: masker}
}

// Write accepts raw output. It always reports success: a failing log must
// not stop the terminal (Err tells what happened).
func (l *LogWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.partial = append(l.partial, p...)
	for {
		i := bytes.IndexByte(l.partial, '\n')
		if i < 0 {
			break
		}
		l.emit(l.partial[:i])
		l.partial = l.partial[i+1:]
	}
	if len(l.partial) > maxLogLine {
		l.emit(l.partial)
		l.partial = nil
	}
	return len(p), nil
}

// Flush writes the unfinished last line, if any.
func (l *LogWriter) Flush() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.partial) > 0 {
		l.emit(l.partial)
		l.partial = nil
	}
}

// WriteLost marks n bytes of output that never reached the log: they left
// the buffer of the mestre-pty before the engine could read them.
func (l *LogWriter) WriteLost(n uint64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.partial) > 0 {
		l.emit(l.partial)
		l.partial = nil
	}
	if l.err != nil {
		return
	}
	if _, err := fmt.Fprintf(l.w, "[trecho perdido: %d bytes]\n", n); err != nil {
		l.err = err
	}
}

// Err returns the first error from the underlying writer.
func (l *LogWriter) Err() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.err
}

func (l *LogWriter) emit(line []byte) {
	line = bytes.TrimSuffix(line, []byte{'\r'})
	if len(line) == 0 || l.err != nil {
		return
	}
	if _, err := io.WriteString(l.w, l.masker.MaskTerminalLine(string(line))+"\n"); err != nil {
		l.err = err
	}
}
