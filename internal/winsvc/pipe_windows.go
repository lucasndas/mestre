//go:build windows

package winsvc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Named pipes as net.Listener and net.Conn, byte mode, overlapped I/O:
// a read or write blocked in the kernel is ended by Close (CancelIoEx), which
// the launcher relies on to drop a peer that says nothing.
//
// The pipe is made for a service that talks to one other service: the first
// instance is created with FILE_FLAG_FIRST_PIPE_INSTANCE (nobody else can
// have the name), remote clients are rejected, and a client opens it
// anonymously, so the server cannot impersonate it.

const (
	pipeBuffer = 64 << 10
	// pipeClientAccess is what an allowed client gets: read, write and the
	// attributes. FILE_CREATE_PIPE_INSTANCE is not in it, so the engine cannot
	// open instances of the pipe of the launcher.
	pipeClientAccess = windows.FILE_READ_DATA | windows.FILE_WRITE_DATA | windows.FILE_READ_ATTRIBUTES | windows.SYNCHRONIZE
)

// PipeName is the pipe of the launcher of an instance.
func PipeName(instance string) string { return `\\.\pipe\mestre-` + instance + `-launcher` }

type pipeAddr string

func (pipeAddr) Network() string  { return "pipe" }
func (a pipeAddr) String() string { return string(a) }

// pipeListener accepts connections on a named pipe.
type pipeListener struct {
	name string
	sa   *windows.SecurityAttributes
	stop windows.Handle // set when the listener closes

	// connectFn waits for a client on an instance (connect; a test replaces it).
	connectFn func(windows.Handle) error

	mu     sync.Mutex
	next   windows.Handle // instance already created and waiting
	closed bool
	first  bool
}

// ListenPipe creates the named pipe with the given SDDL. Closing the
// listener cancels a pending Accept.
func ListenPipe(name, sddl string) (net.Listener, error) {
	sa, err := securityAttributes(sddl)
	if err != nil {
		return nil, err
	}
	stop, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return nil, fmt.Errorf("CreateEvent: %w", err)
	}
	l := &pipeListener{name: name, sa: sa, stop: stop, first: true}
	l.connectFn = l.connect
	h, err := l.create()
	if err != nil {
		_ = windows.CloseHandle(stop)
		return nil, err
	}
	l.next = h
	return l, nil
}

// create makes one instance of the pipe.
func (l *pipeListener) create() (windows.Handle, error) {
	flags := uint32(windows.PIPE_ACCESS_DUPLEX | windows.FILE_FLAG_OVERLAPPED)
	if l.first {
		flags |= windows.FILE_FLAG_FIRST_PIPE_INSTANCE
	}
	h, err := windows.CreateNamedPipe(wstr(l.name), flags,
		windows.PIPE_TYPE_BYTE|windows.PIPE_READMODE_BYTE|windows.PIPE_WAIT|windows.PIPE_REJECT_REMOTE_CLIENTS,
		windows.PIPE_UNLIMITED_INSTANCES, pipeBuffer, pipeBuffer, 0, l.sa)
	if err != nil {
		return 0, fmt.Errorf("CreateNamedPipe %s: %w", l.name, err)
	}
	l.first = false
	return h, nil
}

func (l *pipeListener) Addr() net.Addr { return pipeAddr(l.name) }

// maxConnectFailures is how many instances in a row may fail to connect
// before Accept gives up: a client that connects and leaves (ERROR_NO_DATA)
// fails one instance and says nothing about the next, but a pipe that fails
// every time is broken.
const maxConnectFailures = 16

func (l *pipeListener) Accept() (net.Conn, error) {
	for failures := 0; ; failures++ {
		h, err := l.instance()
		if err != nil {
			return nil, err
		}
		if err := l.connectFn(h); err != nil {
			_ = windows.CloseHandle(h)
			// The failure belongs to this instance and its client (it left
			// before the connection was made: ERROR_NO_DATA, ERROR_BROKEN_PIPE,
			// ERROR_PIPE_NOT_CONNECTED): serve the next one. Ending Accept
			// would end the launcher.
			if errors.Is(err, net.ErrClosed) || l.isClosed() || failures+1 >= maxConnectFailures {
				return nil, err
			}
			continue
		}
		return l.serve(h)
	}
}

func (l *pipeListener) isClosed() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.closed
}

// instance returns the instance waiting for the next client, or makes one.
func (l *pipeListener) instance() (windows.Handle, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return 0, net.ErrClosed
	}
	h := l.next
	l.next = 0
	if h == 0 {
		return l.create()
	}
	return h, nil
}

// serve makes the connection of an instance a client connected to.
func (l *pipeListener) serve(h windows.Handle) (net.Conn, error) {
	// The next instance is ready before this client is served, so that the
	// name is never without one.
	l.mu.Lock()
	if !l.closed {
		if nh, err := l.create(); err == nil {
			l.next = nh
		}
	}
	l.mu.Unlock()
	return newPipeConn(h, string(pipeAddr(l.name)), true)
}

// connect waits for a client on h, or for Close.
func (l *pipeListener) connect(h windows.Handle) error {
	ev, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return fmt.Errorf("CreateEvent: %w", err)
	}
	defer func() { _ = windows.CloseHandle(ev) }()
	ov := &windows.Overlapped{HEvent: ev} // on the heap: it must not move while the kernel holds it
	err = windows.ConnectNamedPipe(h, ov)
	switch {
	case err == nil, errors.Is(err, windows.ERROR_PIPE_CONNECTED):
		return nil
	case !errors.Is(err, windows.ERROR_IO_PENDING):
		return fmt.Errorf("ConnectNamedPipe: %w", err)
	}
	which, err := windows.WaitForMultipleObjects([]windows.Handle{ev, l.stop}, false, windows.INFINITE)
	if err != nil {
		return fmt.Errorf("wait for a client: %w", err)
	}
	if which == windows.WAIT_OBJECT_0 {
		return nil
	}
	// Closed: cancel, and wait for the kernel to let go of ov.
	_ = windows.CancelIoEx(h, ov)
	var n uint32
	_ = windows.GetOverlappedResult(h, ov, &n, true)
	return net.ErrClosed
}

func (l *pipeListener) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	l.closed = true
	_ = windows.SetEvent(l.stop)
	if l.next != 0 {
		_ = windows.CloseHandle(l.next)
		l.next = 0
	}
	return nil
}

// DialPipe opens the pipe as an allowed client. It waits for a busy pipe
// until ctx ends.
func DialPipe(ctx context.Context, name string) (net.Conn, error) {
	for {
		h, err := windows.CreateFile(wstr(name), pipeClientAccess, 0, nil, windows.OPEN_EXISTING,
			windows.FILE_FLAG_OVERLAPPED|windows.SECURITY_SQOS_PRESENT|windows.SECURITY_ANONYMOUS, 0)
		if err == nil {
			return newPipeConn(h, name, false)
		}
		if !errors.Is(err, windows.ERROR_PIPE_BUSY) {
			return nil, fmt.Errorf("open %s: %w", name, err)
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// Every instance is serving someone: let the system say when one is free.
		r, _, _ := procWaitNamedPipeW.Call(uintptr(unsafe.Pointer(wstr(name))), 100)
		_ = r
	}
}

// pipeConn is one end of a connected pipe.
type pipeConn struct {
	name   string
	server bool

	h        windows.Handle
	rev, wev windows.Handle // events of the read and the write in flight
	rmu, wmu sync.Mutex     // one read and one write at a time

	mu     sync.Mutex
	closed bool
	refs   int // operations in flight, and the flush of a closing server end
}

func newPipeConn(h windows.Handle, name string, server bool) (*pipeConn, error) {
	rev, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		_ = windows.CloseHandle(h)
		return nil, err
	}
	wev, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		_ = windows.CloseHandle(h)
		_ = windows.CloseHandle(rev)
		return nil, err
	}
	return &pipeConn{name: name, server: server, h: h, rev: rev, wev: wev}, nil
}

// ClientPID is the process on the other end of a server connection
// (GetNamedPipeClientProcessId).
func (c *pipeConn) ClientPID() (uint32, error) {
	if !c.server {
		return 0, errors.New("not the server end of the pipe")
	}
	var pid uint32
	if err := windows.GetNamedPipeClientProcessId(c.h, &pid); err != nil {
		return 0, fmt.Errorf("GetNamedPipeClientProcessId: %w", err)
	}
	return pid, nil
}

func (c *pipeConn) acquire() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return false
	}
	c.refs++
	return true
}

// release drops one reference; the last one after Close frees the handles.
func (c *pipeConn) release() {
	c.mu.Lock()
	c.refs--
	free := c.closed && c.refs == 0
	c.mu.Unlock()
	if free {
		_ = windows.CloseHandle(c.h)
		_ = windows.CloseHandle(c.rev)
		_ = windows.CloseHandle(c.wev)
	}
}

func (c *pipeConn) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

// transfer does one overlapped read or write of buf.
func (c *pipeConn) transfer(write bool, buf []byte) (int, error) {
	ev := c.rev
	if write {
		ev = c.wev
	}
	_ = windows.ResetEvent(ev)
	ov := &windows.Overlapped{HEvent: ev}
	var n uint32
	var err error
	if write {
		err = windows.WriteFile(c.h, buf, &n, ov)
	} else {
		err = windows.ReadFile(c.h, buf, &n, ov)
	}
	if errors.Is(err, windows.ERROR_IO_PENDING) {
		if c.isClosed() { // Close may have cancelled before this operation was issued
			_ = windows.CancelIoEx(c.h, ov)
		}
		err = windows.GetOverlappedResult(c.h, ov, &n, true)
	}
	return int(n), err
}

func (c *pipeConn) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if !c.acquire() {
		return 0, net.ErrClosed
	}
	defer c.release()
	c.rmu.Lock()
	defer c.rmu.Unlock()
	buf := make([]byte, min(len(p), pipeBuffer)) // heap: the kernel writes into it after the call returns
	n, err := c.transfer(false, buf)
	copy(p, buf[:n])
	switch {
	case err == nil:
		return n, nil
	case errors.Is(err, windows.ERROR_BROKEN_PIPE), errors.Is(err, windows.ERROR_PIPE_NOT_CONNECTED), errors.Is(err, windows.ERROR_NO_DATA):
		if n > 0 {
			return n, nil
		}
		return 0, io.EOF
	case errors.Is(err, windows.ERROR_OPERATION_ABORTED) && c.isClosed():
		return n, net.ErrClosed
	}
	return n, err
}

func (c *pipeConn) Write(p []byte) (int, error) {
	if !c.acquire() {
		return 0, net.ErrClosed
	}
	defer c.release()
	c.wmu.Lock()
	defer c.wmu.Unlock()
	written := 0
	for written < len(p) {
		buf := append([]byte(nil), p[written:min(len(p), written+pipeBuffer)]...)
		n, err := c.transfer(true, buf)
		written += n
		if err != nil {
			switch {
			case errors.Is(err, windows.ERROR_OPERATION_ABORTED) && c.isClosed():
				return written, net.ErrClosed
			case errors.Is(err, windows.ERROR_BROKEN_PIPE), errors.Is(err, windows.ERROR_NO_DATA), errors.Is(err, windows.ERROR_PIPE_NOT_CONNECTED):
				return written, io.ErrClosedPipe
			}
			return written, err
		}
	}
	return written, nil
}

// Close ends the connection. Operations in flight are cancelled, and the
// handles are freed when the last one is done. What the server end wrote
// stays in the pipe for the client to read: closing the handle does not
// discard it (DisconnectNamedPipe would).
func (c *pipeConn) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	free := c.refs == 0 // no one is left to free them
	c.mu.Unlock()
	_ = windows.CancelIoEx(c.h, nil)
	if free {
		_ = windows.CloseHandle(c.h)
		_ = windows.CloseHandle(c.rev)
		_ = windows.CloseHandle(c.wev)
	}
	return nil
}

func (c *pipeConn) LocalAddr() net.Addr  { return pipeAddr(c.name) }
func (c *pipeConn) RemoteAddr() net.Addr { return pipeAddr(c.name) }

// Deadlines are not supported: the launcher ends a slow peer by closing it.
func (c *pipeConn) SetDeadline(time.Time) error      { return os.ErrNoDeadline }
func (c *pipeConn) SetReadDeadline(time.Time) error  { return os.ErrNoDeadline }
func (c *pipeConn) SetWriteDeadline(time.Time) error { return os.ErrNoDeadline }
