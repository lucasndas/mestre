//go:build windows

package attach

import (
	"os"

	"golang.org/x/sys/windows"
)

// enableVT puts the console in VT mode: the escape sequences of the screen are
// understood on the output, and the keys arrive as VT sequences on the input.
// It returns what puts the modes back.
func enableVT(in, out *os.File) func() {
	var restores []func()
	set := func(f *os.File, add uint32) {
		h := windows.Handle(f.Fd())
		var mode uint32
		if windows.GetConsoleMode(h, &mode) != nil {
			return
		}
		if windows.SetConsoleMode(h, mode|add) == nil {
			restores = append(restores, func() { _ = windows.SetConsoleMode(h, mode) })
		}
	}
	set(out, windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING|windows.DISABLE_NEWLINE_AUTO_RETURN)
	set(in, windows.ENABLE_VIRTUAL_TERMINAL_INPUT)
	return func() {
		for _, r := range restores {
			r()
		}
	}
}
