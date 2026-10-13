package winsvc

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"golang.org/x/sys/windows"
)

// listen makes a pipe only the current user and SYSTEM can open.
func listen(t *testing.T) *pipeListener {
	t.Helper()
	self, err := currentUserSID()
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf(`\\.\pipe\mestre-test-%d-%s`, os.Getpid(), strings.NewReplacer("/", "-", " ", "-").Replace(t.Name()))
	ln, err := ListenPipe(name, fmt.Sprintf("D:P(A;;GA;;;SY)(A;;GA;;;%s)", self))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	return ln.(*pipeListener)
}

// A client that leaves before the instance connected (ERROR_NO_DATA) spoils
// that instance only: Accept goes on to the next one, instead of returning
// the error and ending the launcher.
func TestAcceptSurvivesInstancesThatFailToConnect(t *testing.T) {
	t.Parallel()
	l := listen(t)
	real := l.connectFn
	var calls atomic.Int32
	ready := make(chan struct{})
	l.connectFn = func(h windows.Handle) error {
		switch calls.Add(1) {
		case 1:
			return windows.ERROR_NO_DATA
		case 2:
			return windows.ERROR_BROKEN_PIPE
		case 3:
			return windows.ERROR_PIPE_NOT_CONNECTED
		case 4:
			close(ready) // the instance exists and waits for a client
		}
		return real(h)
	}
	type accepted struct {
		c   net.Conn
		err error
	}
	got := make(chan accepted, 1)
	go func() {
		c, err := l.Accept()
		got <- accepted{c, err}
	}()
	<-ready
	client, err := DialPipe(t.Context(), l.name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	a := <-got
	if a.err != nil {
		t.Fatalf("Accept = %v after three instances failed to connect", a.err)
	}
	t.Cleanup(func() { _ = a.c.Close() })

	if _, err := client.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 1)
	if _, err := io.ReadFull(a.c, b); err != nil || b[0] != 'x' {
		t.Fatalf("read %q, %v", b, err)
	}
}

// A pipe that fails to connect every time is broken: Accept says so after a
// bound, and a closed listener ends it at once with net.ErrClosed.
func TestAcceptGivesUpOnAPipeThatAlwaysFails(t *testing.T) {
	t.Parallel()
	t.Run("always failing", func(t *testing.T) {
		t.Parallel()
		l := listen(t)
		var calls atomic.Int32
		l.connectFn = func(windows.Handle) error { calls.Add(1); return windows.ERROR_NO_DATA }
		if _, err := l.Accept(); !errors.Is(err, windows.ERROR_NO_DATA) {
			t.Fatalf("Accept = %v", err)
		}
		if calls.Load() != maxConnectFailures {
			t.Fatalf("tried %d instances, want %d", calls.Load(), maxConnectFailures)
		}
	})
	t.Run("closed", func(t *testing.T) {
		t.Parallel()
		l := listen(t)
		l.connectFn = func(windows.Handle) error { return windows.ERROR_NO_DATA }
		_ = l.Close()
		if _, err := l.Accept(); !errors.Is(err, net.ErrClosed) {
			t.Fatalf("Accept = %v", err)
		}
	})
}
