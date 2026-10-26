package attach

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/x/term"
)

// Console is the terminal of the person: where the login is asked, the screen
// of the agent is drawn and the keys come from. The tests give a fake.
type Console interface {
	// Interactive says whether the input and the output are a terminal.
	Interactive() bool
	// ReadLine asks a line (with echo); ReadPassword asks one with no echo.
	ReadLine(prompt string) (string, error)
	ReadPassword(prompt string) (string, error)
	// Size is the size of the window of the person.
	Size() (cols, rows int)
	// EnterRaw puts the console in raw mode (VT mode on Windows) and on its
	// alternate screen, and returns what puts it back.
	EnterRaw() (restore func(), err error)
	// Input is the keys, raw, after EnterRaw; Output is where the screen is drawn.
	Input() io.Reader
	Output() io.Writer
}

// osConsole is the console of the process.
type osConsole struct {
	in  *os.File
	out *os.File
	rd  *bufio.Reader
}

// OSConsole is the console of this process: standard input and output.
func OSConsole() Console {
	return &osConsole{in: os.Stdin, out: os.Stdout, rd: bufio.NewReader(os.Stdin)}
}

func (c *osConsole) Interactive() bool {
	return term.IsTerminal(c.in.Fd()) && term.IsTerminal(c.out.Fd())
}

func (c *osConsole) ReadLine(prompt string) (string, error) {
	fmt.Fprint(c.out, prompt)
	line, err := c.rd.ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// drain drops what the line reader holds beyond the line it returned: the password
// is read from the descriptor, and a password pasted with the login must be neither
// echoed nor left in a buffer to be typed into an agent later.
func (c *osConsole) drain() { _, _ = c.rd.Discard(c.rd.Buffered()) }

func (c *osConsole) ReadPassword(prompt string) (string, error) {
	c.drain()
	fmt.Fprint(c.out, prompt)
	b, err := term.ReadPassword(c.in.Fd())
	fmt.Fprintln(c.out)
	return string(b), err
}

func (c *osConsole) Size() (int, int) {
	w, h, err := term.GetSize(c.out.Fd())
	if err != nil || w <= 0 || h <= 0 {
		return 80, 24
	}
	return w, h
}

func (c *osConsole) EnterRaw() (func(), error) {
	c.drain()
	state, err := term.MakeRaw(c.in.Fd())
	if err != nil {
		return nil, err
	}
	undoVT := enableVT(c.in, c.out)
	fmt.Fprint(c.out, "\x1b[?1049h\x1b[2J")
	return func() {
		fmt.Fprint(c.out, "\x1b[0m\x1b[?25h\x1b[?1049l")
		undoVT()
		_ = term.Restore(c.in.Fd(), state)
	}, nil
}

func (c *osConsole) Input() io.Reader  { return c.rd }
func (c *osConsole) Output() io.Writer { return c.out }
