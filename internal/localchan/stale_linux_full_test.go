//go:build full && linux

package localchan_test

import (
	"net"
	"syscall"
	"testing"

	"github.com/mestre-ia/mestre/internal/localchan"
)

// bindStale binds a stream socket to path and leaves it unlistening: the file
// an engine that died leaves behind. It returns the descriptor, still open.
func bindStale(t *testing.T, path string) int {
	t.Helper()
	fd, err := syscall.Socket(syscall.AF_UNIX, syscall.SOCK_STREAM|syscall.SOCK_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Bind(fd, &syscall.SockaddrUnix{Name: path}); err != nil {
		_ = syscall.Close(fd)
		t.Fatal(err)
	}
	return fd
}

// leaveStaleSocket leaves at path the socket file of an engine that died: the
// file is there and nothing listens on it. A listener that is closed without
// unlinking would do it too, but a process forked by a parallel test between
// the creation and the close holds a copy of the descriptor until it execs,
// and the socket keeps listening for that long: Listen then (rightly) found
// "another engine". A socket that was only bound cannot be made to listen by a
// copy of its descriptor.
func leaveStaleSocket(t *testing.T, path string) {
	t.Helper()
	if err := syscall.Close(bindStale(t, path)); err != nil {
		t.Fatal(err)
	}
}

// The cause of the flake of TestListenAppliesTheAccess, with the fork played by
// a copy of the descriptor: a listener closed without unlinking still answers
// while a copy lives, and Listen takes it for a running engine.
func TestAListeningSocketKeptByACopyIsAnEngineButABoundOneIsStale(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	listening := dir + "/l.sock"
	ln, err := net.Listen("unix", listening)
	if err != nil {
		t.Fatal(err)
	}
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	f, err := ln.(*net.UnixListener).File()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	_ = ln.Close() // the copy keeps it listening
	if _, err := localchan.Listen(listening, localchan.NoAccess{}); err == nil {
		t.Fatal("a socket that still listens, through a copy, was replaced")
	}

	bound := dir + "/b.sock"
	fd := bindStale(t, bound)
	dup, err := syscall.Dup(fd)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Close(dup) })
	_ = syscall.Close(fd)
	got, err := localchan.Listen(bound, localchan.NoAccess{})
	if err != nil {
		t.Fatalf("the stale socket, with a copy of its descriptor alive: %v", err)
	}
	_ = got.Close()
}
