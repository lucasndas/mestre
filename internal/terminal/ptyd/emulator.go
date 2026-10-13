package ptyd

// DefaultHistoryLines is the scrollback of the emulator
// (docs/arquitetura/terminal.md#emulador-de-terminal).
const DefaultHistoryLines = 5000

// Emulator is the VT emulator behind every terminal: the only source of
// "screen" for the Mestre. It sits behind this interface so the library can
// be replaced (docs/arquitetura/terminal.md#verificar-na-implementação).
// Implementations are safe for concurrent use.
type Emulator interface {
	// Write feeds process output. It never blocks on the process side.
	Write(p []byte)
	// Screen returns the visible screen as plain text: one line per row,
	// trailing blanks and trailing empty rows trimmed, no control sequences.
	Screen() string
	// History returns the last n lines of the scrollback followed by the
	// screen (all of the scrollback when n < 0), as plain text.
	History(n int) string
	// Cursor returns the zero-based cursor column and row.
	Cursor() (col, row int)
	// Size returns the fixed size of the screen.
	Size() (cols, rows int)
	// Reset forgets screen and history (the screen is rebuilt from the
	// buffer when the emulator fell behind).
	Reset()
	// Close releases the emulator; Write and the reads become no-ops.
	Close()
}

// EmulatorFactory builds an emulator. reply receives what the emulated
// terminal answers to the queries the process prints (cursor position,
// device attributes, ...); the terminal types it into the PTY.
type EmulatorFactory func(cols, rows, history int, reply func([]byte)) Emulator
