package main

import (
	"testing"

	"golang.org/x/sys/windows"
)

func TestRawModeDeliversCtrlCAsAByte(t *testing.T) {
	t.Parallel()
	cooked := uint32(windows.ENABLE_PROCESSED_INPUT | windows.ENABLE_LINE_INPUT | windows.ENABLE_ECHO_INPUT | windows.ENABLE_INSERT_MODE)
	got := rawMode(cooked)
	if got&(windows.ENABLE_PROCESSED_INPUT|windows.ENABLE_LINE_INPUT|windows.ENABLE_ECHO_INPUT) != 0 {
		t.Fatalf("raw mode %#x keeps line editing, echo or Ctrl-C processing", got)
	}
	if got&windows.ENABLE_VIRTUAL_TERMINAL_INPUT == 0 || got&windows.ENABLE_INSERT_MODE == 0 {
		t.Fatalf("raw mode %#x lost VT input or an unrelated flag", got)
	}
}
