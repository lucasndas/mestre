package attach

import (
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/charmbracelet/x/vt"

	"github.com/mestre-ia/mestre/internal/i18n"
)

// screen is the picture of the terminal of the character: the emulator the
// machines use (docs/arquitetura/terminal.md), at the fixed size of the PTY.
type screen struct {
	cols, rows int
	// say writes the line of state in the language of the person (nil: pt-BR).
	say func(i18n.Msg) string

	mu sync.Mutex
	em *vt.Emulator
}

func newScreen(cols, rows int) *screen {
	s := &screen{cols: cols, rows: rows}
	s.em = s.newVT()
	return s
}

// newVT builds the emulator and drains its answers: the questions of the program
// (where is the cursor, which colors) are the emulator's to answer and the
// answers go nowhere. They never reach the agent as typing, and the client never
// asks for a resize.
func (s *screen) newVT() *vt.Emulator {
	em := vt.NewEmulator(s.cols, s.rows)
	go func() { _, _ = io.Copy(io.Discard, em) }()
	return em
}

// Reset starts the picture over (a replay is about to come).
func (s *screen) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	closePipe(s.em)
	s.em = s.newVT()
}

// closePipe ends the goroutine draining the answers (see ptyd.closeVT).
func closePipe(em *vt.Emulator) {
	if pw, ok := em.InputPipe().(*io.PipeWriter); ok {
		_ = pw.CloseWithError(io.EOF)
		return
	}
	_ = em.Close()
}

// Close ends the emulator.
func (s *screen) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	closePipe(s.em)
}

// Write feeds the output of the terminal.
func (s *screen) Write(p []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _ = s.em.Write(p)
}

// view is the part of the terminal a window shows.
type view struct {
	left, top, cols, rows int
}

// viewFor chooses the part of a cols×rows terminal that fits a window of
// winCols×contentRows, following the cursor: the cursor stays inside, near the
// middle when the terminal is bigger than the window, and the view never leaves the
// terminal.
func viewFor(cols, rows, winCols, contentRows, curX, curY int) view {
	v := view{cols: min(cols, max(winCols, 1)), rows: min(rows, max(contentRows, 1))}
	v.left = clamp(curX-v.cols/2, 0, cols-v.cols)
	v.top = clamp(curY-v.rows/2, 0, rows-v.rows)
	return v
}

func clamp(n, lo, hi int) int {
	if hi < lo {
		hi = lo
	}
	return max(lo, min(n, hi))
}

// Frame draws what a window of winCols×winRows shows: the part of the terminal
// around the cursor, and on the last row a line of state with the size of the
// terminal. status is the rest of that line. Nothing in it moves the terminal of
// the person or asks for another size. showCursor places the cursor of the
// person where the one of the agent is (when typing).
func (s *screen) Frame(winCols, winRows int, status string, showCursor bool) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur := s.em.CursorPosition()
	v := viewFor(s.cols, s.rows, winCols, winRows-1, cur.X, cur.Y)
	var b strings.Builder
	b.WriteString("\x1b[?25l\x1b[0m")
	for y := 0; y < v.rows; y++ {
		fmt.Fprintf(&b, "\x1b[%d;1H", y+1)
		for x := v.left; x < v.left+v.cols; x++ {
			c := s.em.CellAt(x, v.top+y)
			switch {
			case c == nil:
				b.WriteString(" ")
			case c.Width == 0:
				// the second column of a wide character: the character already took it
			case c.Content == "":
				b.WriteString(c.Style.Styled(" "))
			default:
				b.WriteString(c.Style.Styled(c.Content))
			}
		}
		b.WriteString("\x1b[0m\x1b[K")
	}
	// the rows of the window under the picture are cleared
	for y := v.rows; y < winRows-1; y++ {
		fmt.Fprintf(&b, "\x1b[%d;1H\x1b[K", y+1)
	}
	say := s.say
	if say == nil {
		say = func(m i18n.Msg) string { return i18n.Render(i18n.PtBR, m) }
	}
	line := " " + say(i18n.M("person.frame_line", "cols", s.cols, "rows", s.rows, "wcols", winCols, "wrows", winRows,
		"r1", v.top+1, "r2", v.top+v.rows, "c1", v.left+1, "c2", v.left+v.cols))
	if status != "" {
		line += " · " + status
	}
	fmt.Fprintf(&b, "\x1b[%d;1H\x1b[7m%s\x1b[0m\x1b[K", max(winRows, 1), fit(line, winCols))
	if showCursor && !s.em.CursorHidden() && cur.X >= v.left && cur.X < v.left+v.cols && cur.Y >= v.top && cur.Y < v.top+v.rows {
		fmt.Fprintf(&b, "\x1b[%d;%dH\x1b[?25h", cur.Y-v.top+1, cur.X-v.left+1)
	}
	return b.String()
}

// fit cuts a line to n columns and pads it with spaces (the line of state is
// drawn in reverse video).
func fit(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		r = r[:max(n, 0)]
	}
	return string(r) + strings.Repeat(" ", max(n-len(r), 0))
}
