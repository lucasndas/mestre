package main

import (
	"os"

	"golang.org/x/sys/windows"
)

// rawConsole puts a console stdin in raw mode, as the real CLIs do: Ctrl-C
// arrives as the byte 0x03 instead of a console control event, and keys
// arrive one by one as VT sequences (arrows, bracketed paste). On a pipe
// (no console) it does nothing. restore puts the old mode back.
func rawConsole() (restore func()) {
	h := windows.Handle(os.Stdin.Fd())
	var mode uint32
	if windows.GetConsoleMode(h, &mode) != nil {
		return func() {}
	}
	if windows.SetConsoleMode(h, rawMode(mode)) != nil {
		return func() {}
	}
	return func() { _ = windows.SetConsoleMode(h, mode) }
}

// rawMode turns off the console's line editing, echo and Ctrl-C handling
// and turns on VT input.
func rawMode(mode uint32) uint32 {
	mode &^= windows.ENABLE_PROCESSED_INPUT | windows.ENABLE_LINE_INPUT | windows.ENABLE_ECHO_INPUT
	return mode | windows.ENABLE_VIRTUAL_TERMINAL_INPUT
}
