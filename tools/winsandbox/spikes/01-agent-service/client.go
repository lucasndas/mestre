//go:build windows

package main

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/windows"
)

func call(service string, req request) (response, error) {
	var resp response
	f, err := os.OpenFile(pipeName(service), os.O_RDWR, 0)
	if err != nil {
		return resp, fmt.Errorf("connect to %s: %w", service, err)
	}
	defer f.Close()
	b, _ := json.Marshal(req)
	if _, err := f.Write(append(b, '\n')); err != nil {
		return resp, err
	}
	line, err := bufio.NewReader(f).ReadBytes('\n')
	if err != nil {
		return resp, err
	}
	if err := json.Unmarshal(line, &resp); err != nil {
		return resp, err
	}
	if resp.Error != "" {
		return resp, errors.New(resp.Error)
	}
	return resp, nil
}

// client is the client side of one terminal session.
type client struct {
	in, out *os.File
	echo    io.Writer // raw output copy (interactive mode), may be nil
	rawLog  io.Writer // raw output log, may be nil

	mu      sync.Mutex
	changed chan struct{}
	raw     []byte
	logs    []string
	started *startInfo
	exit    *exitInfo
	pids    *pidsInfo
	gone    bool
}

func openClient(service string, req request) (*client, error) {
	resp, err := call(service, req)
	if err != nil {
		return nil, err
	}
	out, err := os.OpenFile(resp.Out, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	in, err := os.OpenFile(resp.In, os.O_WRONLY, 0)
	if err != nil {
		out.Close()
		return nil, err
	}
	c := &client{in: in, out: out, changed: make(chan struct{})}
	go c.read()
	return c, nil
}

func (c *client) notify() {
	close(c.changed)
	c.changed = make(chan struct{})
}

func (c *client) read() {
	r := bufio.NewReader(c.out)
	for {
		var hdr [5]byte
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			break
		}
		p := make([]byte, binary.LittleEndian.Uint32(hdr[1:]))
		if _, err := io.ReadFull(r, p); err != nil {
			break
		}
		if hdr[0] == frOutput {
			if c.echo != nil {
				_, _ = c.echo.Write(p)
			}
			if c.rawLog != nil {
				_, _ = c.rawLog.Write(p)
			}
		}
		c.mu.Lock()
		switch hdr[0] {
		case frOutput:
			c.raw = append(c.raw, p...)
		case frLog:
			c.logs = append(c.logs, string(p))
		case frStarted:
			c.started = new(startInfo)
			_ = json.Unmarshal(p, c.started)
		case frExit:
			c.exit = new(exitInfo)
			_ = json.Unmarshal(p, c.exit)
		case frPIDs:
			c.pids = new(pidsInfo)
			_ = json.Unmarshal(p, c.pids)
		}
		c.notify()
		c.mu.Unlock()
	}
	c.mu.Lock()
	c.gone = true
	c.notify()
	c.mu.Unlock()
}

func (c *client) send(kind byte, p []byte) error {
	var hdr [5]byte
	hdr[0] = kind
	binary.LittleEndian.PutUint32(hdr[1:], uint32(len(p)))
	_, err := c.in.Write(append(hdr[:], p...))
	return err
}

func (c *client) typeKeys(s string) error { return c.send(frInput, []byte(s)) }

func (c *client) resize(cols, rows uint16) error {
	var p [4]byte
	binary.LittleEndian.PutUint16(p[:], cols)
	binary.LittleEndian.PutUint16(p[2:], rows)
	return c.send(frResize, p[:])
}

func (c *client) close() {
	c.in.Close()
	c.out.Close()
}

// waitFor waits until pred (called with c.mu held) is true or d passes.
func (c *client) waitFor(d time.Duration, pred func() bool) bool {
	deadline := time.Now().Add(d)
	for {
		c.mu.Lock()
		ok, ch, gone := pred(), c.changed, c.gone
		c.mu.Unlock()
		if ok {
			return true
		}
		left := time.Until(deadline)
		if left <= 0 || gone {
			return false
		}
		t := time.NewTimer(left)
		select {
		case <-ch:
			t.Stop()
		case <-t.C:
		}
	}
}

var ansi = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(\x07|\x1b\\)|\x1b[@-Z\\-_]|\x1b[()][0-9A-Za-z]`)

func strip(b []byte) string { return ansi.ReplaceAllString(string(b), "") }

// text returns the output since offset, without escape sequences. Call with
// c.mu held.
func (c *client) textFrom(off int) string { return strip(c.raw[off:]) }

func (c *client) mark() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.raw)
}

// expect waits for re in the output after off.
func (c *client) expect(off int, re *regexp.Regexp, d time.Duration) (string, bool) {
	var m string
	ok := c.waitFor(d, func() bool {
		m = re.FindString(c.textFrom(off))
		return m != ""
	})
	return m, ok
}

func (c *client) queryPIDs(d time.Duration) []uint32 {
	c.mu.Lock()
	c.pids = nil
	c.mu.Unlock()
	if err := c.send(frQuery, nil); err != nil {
		return nil
	}
	c.waitFor(d, func() bool { return c.pids != nil })
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pids == nil {
		return nil
	}
	return c.pids.PIDs
}

func (c *client) snapshot() (logs []string, started *startInfo, exit *exitInfo, text string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.logs...), c.started, c.exit, strip(c.raw)
}

// Process inspection from the client side (Administrators with
// SeDebugPrivilege enabled; the service does not need to see other
// accounts' processes).

func processAlive(pid uint32) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, pid)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	ev, _ := windows.WaitForSingleObject(h, 0)
	return ev == uint32(windows.WAIT_TIMEOUT)
}

func processImage(pid uint32) string {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return "?"
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, 1024)
	n := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &n); err != nil {
		return "?"
	}
	s := windows.UTF16ToString(buf[:n])
	return s[strings.LastIndex(s, `\`)+1:]
}

func describePIDs(pids []uint32) string {
	var parts []string
	for _, p := range pids {
		parts = append(parts, fmt.Sprintf("%d:%s", p, processImage(p)))
	}
	return strings.Join(parts, " ")
}

// interactive relays the local console to the session until the process
// exits or the user presses Ctrl+].
func (c *client) interactive() error {
	stdin, stdout := windows.Handle(os.Stdin.Fd()), windows.Handle(os.Stdout.Fd())
	var inMode, outMode uint32
	if err := windows.GetConsoleMode(stdin, &inMode); err != nil {
		return fmt.Errorf("stdin is not a console: %w", err)
	}
	_ = windows.GetConsoleMode(stdout, &outMode)
	_ = windows.SetConsoleMode(stdin, windows.ENABLE_VIRTUAL_TERMINAL_INPUT)
	_ = windows.SetConsoleMode(stdout, outMode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING|windows.DISABLE_NEWLINE_AUTO_RETURN)
	defer windows.SetConsoleMode(stdin, inMode)
	defer windows.SetConsoleMode(stdout, outMode)

	done := make(chan struct{})
	go func() {
		c.waitFor(24*time.Hour, func() bool { return c.exit != nil })
		close(done)
	}()
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := os.Stdin.Read(buf)
			if err != nil {
				return
			}
			if i := strings.IndexByte(string(buf[:n]), 0x1d); i >= 0 { // Ctrl+]
				_ = c.typeKeys(string(buf[:i]))
				close(detach)
				return
			}
			_ = c.typeKeys(string(buf[:n]))
		}
	}()
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	lastC, lastR := consoleSize()
	for {
		select {
		case <-done:
			return nil
		case <-detach:
			return nil
		case <-tick.C:
			if cc, rr := consoleSize(); cc != lastC || rr != lastR {
				lastC, lastR = cc, rr
				_ = c.resize(cc, rr)
			}
		}
	}
}

var detach = make(chan struct{})

func consoleSize() (uint16, uint16) {
	var info windows.ConsoleScreenBufferInfo
	if err := windows.GetConsoleScreenBufferInfo(windows.Handle(os.Stdout.Fd()), &info); err != nil {
		return 200, 50
	}
	return uint16(info.Window.Right - info.Window.Left + 1), uint16(info.Window.Bottom - info.Window.Top + 1)
}
