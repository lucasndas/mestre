package ptyd

import (
	"io"
	"strings"
	"sync"

	"github.com/charmbracelet/x/vt"
)

// vtEmulator is the Emulator backed by github.com/charmbracelet/x/vt.
type vtEmulator struct {
	cols, rows, history int
	reply               func([]byte)

	mu     sync.Mutex
	em     *vt.Emulator
	closed bool
}

var _ Emulator = (*vtEmulator)(nil)

// NewVTEmulator is the default EmulatorFactory.
func NewVTEmulator(cols, rows, history int, reply func([]byte)) Emulator {
	e := &vtEmulator{cols: cols, rows: rows, history: history, reply: reply}
	e.em = e.newVT()
	return e
}

// newVT builds the library emulator and starts draining its answers: the
// library hands them over through a synchronous pipe, so without a reader
// the first query from the process would block Write for good.
func (e *vtEmulator) newVT() *vt.Emulator {
	em := vt.NewEmulator(e.cols, e.rows)
	em.SetScrollbackSize(e.history)
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := em.Read(buf)
			if n > 0 && e.reply != nil {
				e.reply(append([]byte(nil), buf[:n]...))
			}
			if err != nil {
				return
			}
		}
	}()
	return em
}

// closeVT ends the goroutine draining the library's answers by closing the
// pipe it reads from. It avoids vt.Emulator.Close, which writes a flag that
// the blocked Read checks without synchronisation (a data race).
func closeVT(em *vt.Emulator) {
	if pw, ok := em.InputPipe().(*io.PipeWriter); ok {
		_ = pw.CloseWithError(io.EOF)
		return
	}
	_ = em.Close()
}

func (e *vtEmulator) Write(p []byte) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.closed {
		_, _ = e.em.Write(p)
	}
}

func (e *vtEmulator) Screen() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.screenLocked()
}

func (e *vtEmulator) screenLocked() string {
	lines := strings.Split(strings.ReplaceAll(e.em.String(), "\r\n", "\n"), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " ")
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n")
}

func (e *vtEmulator) History(n int) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	var lines []string
	if sb := e.em.Scrollback(); sb != nil && !e.em.IsAltScreen() {
		for i := range sb.Len() {
			lines = append(lines, strings.TrimRight(sb.Line(i).String(), " "))
		}
	}
	if s := e.screenLocked(); s != "" {
		lines = append(lines, strings.Split(s, "\n")...)
	}
	if n >= 0 && len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

func (e *vtEmulator) Cursor() (col, row int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	p := e.em.CursorPosition()
	return p.X, p.Y
}

func (e *vtEmulator) Size() (cols, rows int) { return e.cols, e.rows }

func (e *vtEmulator) Reset() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return
	}
	closeVT(e.em)
	e.em = e.newVT()
}

func (e *vtEmulator) Close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.closed {
		e.closed = true
		closeVT(e.em)
	}
}
