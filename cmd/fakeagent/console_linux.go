package main

import (
	"os"

	"golang.org/x/sys/unix"
)

// rawConsole puts a terminal stdin in raw mode, as the real CLIs do: no
// echo, no line editing, Ctrl-C arrives as the byte 0x03 and Enter as \r.
// On a pipe (no terminal) it does nothing. restore puts the old mode back.
func rawConsole() (restore func()) {
	fd := int(os.Stdin.Fd())
	old, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		return func() {}
	}
	if unix.IoctlSetTermios(fd, unix.TCSETS, rawTermios(*old)) != nil {
		return func() {}
	}
	return func() { _ = unix.IoctlSetTermios(fd, unix.TCSETS, old) }
}

// rawTermios is cfmakeraw.
func rawTermios(t unix.Termios) *unix.Termios {
	t.Iflag &^= unix.IGNBRK | unix.BRKINT | unix.PARMRK | unix.ISTRIP | unix.INLCR | unix.IGNCR | unix.ICRNL | unix.IXON
	t.Oflag &^= unix.OPOST
	t.Lflag &^= unix.ECHO | unix.ECHONL | unix.ICANON | unix.ISIG | unix.IEXTEN
	t.Cflag &^= unix.CSIZE | unix.PARENB
	t.Cflag |= unix.CS8
	t.Cc[unix.VMIN], t.Cc[unix.VTIME] = 1, 0
	return &t
}
