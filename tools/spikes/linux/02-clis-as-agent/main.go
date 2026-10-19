//go:build linux

// Command 02-clis-as-agent is the M1 spike "claude, codex e agy rodando como
// mestre-agente no Linux" (docs/arquitetura/seguranca.md#contas-do-sistema-e-isolamento-dos-agentes).
//
// It runs one command inside a real PTY (stdlib only: /dev/ptmx + ioctls),
// answers the terminal queries TUIs make at start-up (cursor position, device
// attributes, colours, kitty keyboard), plays a script of "wait for regex,
// then type keys" steps and stops at a regex or at the deadline. The child is
// whatever the caller passes, usually `sudo -n -u mestre-agente env … <cli>`
// (the real engine uses CAP_SETUID instead of sudo).
//
//	go run ./tools/spikes/linux/02-clis-as-agent -deadline 30s \
//	    -step 'trust this folder|||\r' -until 'Try "' -out /tmp/t.raw -- sudo -n -u mestre-agente env … claude
//
// Stdout gets the transcript with escape sequences stripped; -out keeps the
// raw bytes. Exit status: 0 when -until matched (or no -until and the child
// exited), 2 on deadline, 1 on errors.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

type step struct {
	wait *regexp.Regexp
	keys string
}

type stepList []step

func (s *stepList) String() string { return fmt.Sprint(len(*s)) }

func (s *stepList) Set(v string) error {
	re, keys, ok := strings.Cut(v, "|||")
	if !ok {
		return errors.New("step must be 'regex|||keys'")
	}
	k, err := strconv.Unquote(`"` + keys + `"`)
	if err != nil {
		return fmt.Errorf("keys %q: %w", keys, err)
	}
	r, err := regexp.Compile(re)
	if err != nil {
		return err
	}
	*s = append(*s, step{wait: r, keys: k})
	return nil
}

func main() {
	var steps stepList
	deadline := flag.Duration("deadline", 30*time.Second, "total time limit")
	until := flag.String("until", "", "stop when the stripped output matches this regex (after all steps)")
	out := flag.String("out", "", "file for the raw transcript")
	cols := flag.Int("cols", 120, "terminal columns")
	rows := flag.Int("rows", 40, "terminal rows")
	settle := flag.Duration("settle", 300*time.Millisecond, "quiet period before a step fires (lets the TUI finish its start-up negotiation)")
	flag.Var(&steps, "step", "'regex|||keys' (repeatable, in order); keys use Go escapes")
	flag.Parse()
	if flag.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: 02-clis-as-agent [flags] -- command args…")
		os.Exit(1)
	}
	code, err := run(flag.Args(), steps, *until, *out, *deadline, *settle, uint16(*cols), uint16(*rows))
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
	}
	os.Exit(code)
}

func run(argv []string, steps []step, until, outPath string, deadline, settle time.Duration, cols, rows uint16) (int, error) {
	master, slave, err := openPTY(cols, rows)
	if err != nil {
		return 1, err
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.Env = append(os.Environ(), "TERM=xterm-256color", "COLORTERM=truecolor")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		return 1, err
	}
	_ = slave.Close()

	var raw io.Writer = io.Discard
	if outPath != "" {
		f, err := os.Create(outPath)
		if err != nil {
			return 1, err
		}
		defer f.Close()
		raw = f
	}

	var untilRe *regexp.Regexp
	if until != "" {
		untilRe = regexp.MustCompile(until)
	}

	var (
		mu      sync.Mutex
		all     strings.Builder // stripped, whole session
		pending strings.Builder // stripped, since the last step fired
		lastOut = time.Now()
	)
	changed := make(chan struct{}, 1)
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		buf := make([]byte, 32*1024)
		for {
			n, err := master.Read(buf)
			if n > 0 {
				chunk := buf[:n]
				_, _ = raw.Write(chunk)
				answerQueries(master, chunk, rows, cols)
				s := strip(string(chunk))
				mu.Lock()
				all.WriteString(s)
				pending.WriteString(s)
				lastOut = time.Now()
				mu.Unlock()
				select {
				case changed <- struct{}{}:
				default:
				}
			}
			if err != nil {
				return
			}
		}
	}()
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

	timer := time.NewTimer(deadline)
	defer timer.Stop()
	code := 2
	var childErr error
	childGone := false
	var settleC <-chan time.Time
loop:
	for {
		mu.Lock()
		text := pending.String()
		quiet := time.Since(lastOut)
		mu.Unlock()
		settleC = nil
		if len(steps) > 0 && steps[0].wait.MatchString(flatten(text)) && quiet < settle {
			settleC = time.After(settle - quiet)
		} else if len(steps) > 0 && steps[0].wait.MatchString(flatten(text)) {
			fmt.Fprintf(os.Stderr, "[pty] step matched %q, sending %q\n", steps[0].wait, steps[0].keys)
			mu.Lock()
			pending.Reset()
			mu.Unlock()
			if err := typeKeys(master, steps[0].keys); err != nil {
				return 1, err
			}
			steps = steps[1:]
			continue
		}
		if len(steps) == 0 && untilRe != nil {
			mu.Lock()
			whole := all.String()
			mu.Unlock()
			if untilRe.MatchString(flatten(whole)) {
				fmt.Fprintf(os.Stderr, "[pty] until matched %q\n", until)
				code = 0
				break loop
			}
		}
		select {
		case <-changed:
		case <-settleC:
		case childErr = <-exited:
			childGone = true
			<-readDone
			if len(steps) == 0 && untilRe == nil {
				code = 0
			} else {
				mu.Lock()
				whole := all.String()
				mu.Unlock()
				if len(steps) == 0 && untilRe.MatchString(flatten(whole)) {
					code = 0
				}
			}
			break loop
		case <-timer.C:
			fmt.Fprintf(os.Stderr, "[pty] deadline %s reached (%d steps left)\n", deadline, len(steps))
			break loop
		}
	}
	// Hang up: closing the master sends SIGHUP to the session (works across
	// uids, unlike kill from this process to a sudo child).
	_ = master.Close()
	if !childGone {
		select {
		case childErr = <-exited:
		case <-time.After(5 * time.Second):
			fmt.Fprintln(os.Stderr, "[pty] child did not exit 5s after hangup")
		}
	}
	mu.Lock()
	fmt.Print(all.String())
	mu.Unlock()
	fmt.Fprintf(os.Stderr, "\n[pty] child: %v\n", childErr)
	return code, nil
}

// typeKeys sends keys; a lone "\r" after text is sent as a separate write, as
// a human Enter would be.
func typeKeys(w io.Writer, keys string) error {
	if strings.HasSuffix(keys, "\r") && len(keys) > 1 {
		if _, err := io.WriteString(w, strings.TrimSuffix(keys, "\r")); err != nil {
			return err
		}
		_, err := io.WriteString(w, "\r")
		return err
	}
	_, err := io.WriteString(w, keys)
	return err
}

func openPTY(cols, rows uint16) (*os.File, *os.File, error) {
	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, nil, err
	}
	var unlock int32
	if err := ioctl(m.Fd(), syscall.TIOCSPTLCK, uintptr(unsafe.Pointer(&unlock))); err != nil {
		return nil, nil, fmt.Errorf("unlockpt: %w", err)
	}
	var n uint32
	if err := ioctl(m.Fd(), syscall.TIOCGPTN, uintptr(unsafe.Pointer(&n))); err != nil {
		return nil, nil, fmt.Errorf("ptsname: %w", err)
	}
	s, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return nil, nil, err
	}
	ws := struct{ row, col, x, y uint16 }{rows, cols, 0, 0}
	if err := ioctl(s.Fd(), syscall.TIOCSWINSZ, uintptr(unsafe.Pointer(&ws))); err != nil {
		return nil, nil, fmt.Errorf("winsize: %w", err)
	}
	return m, s, nil
}

func ioctl(fd, req, arg uintptr) error {
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, req, arg); e != 0 {
		return e
	}
	return nil
}

// answerQueries replies to the start-up queries crossterm/ink/bubbletea make;
// without a reply some TUIs stall or abort ("cursor position could not be read").
func answerQueries(w io.Writer, chunk []byte, rows, cols uint16) {
	s := string(chunk)
	if strings.Contains(s, "\x1b[6n") {
		_, _ = io.WriteString(w, "\x1b[1;1R")
	}
	if strings.Contains(s, "\x1b[c") || strings.Contains(s, "\x1b[0c") {
		_, _ = io.WriteString(w, "\x1b[?62;22c")
	}
	if strings.Contains(s, "\x1b]10;?") {
		_, _ = io.WriteString(w, "\x1b]10;rgb:ffff/ffff/ffff\x1b\\")
	}
	if strings.Contains(s, "\x1b]11;?") {
		_, _ = io.WriteString(w, "\x1b]11;rgb:0000/0000/0000\x1b\\")
	}
	if strings.Contains(s, "\x1b[18t") {
		_, _ = fmt.Fprintf(w, "\x1b[8;%d;%dt", rows, cols)
	}
}

var ansi = regexp.MustCompile(`\x1b\[[0-9;?<>=!]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(\x07|\x1b\\)|\x1b[PX^_][^\x1b]*\x1b\\|\x1b[()][0-9A-Za-z]|\x1b[=>78DEHMc]`)

func strip(s string) string { return ansi.ReplaceAllString(s, " ") }

var spaces = regexp.MustCompile(`\s+`)

// flatten collapses whitespace so redrawn TUI text still matches.
func flatten(s string) string { return spaces.ReplaceAllString(s, " ") }
