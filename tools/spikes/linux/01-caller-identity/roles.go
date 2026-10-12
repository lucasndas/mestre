//go:build linux

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// The processes of the scenarios are this same binary with a role in
// os.Args[1] (the test binary dispatches them in TestMain).
const (
	roleClient  = "spike-client"  // the `mestre` CLI: connect, send agent_id, read the answer
	roleShim    = "spike-shim"    // PR_SET_CHILD_SUBREAPER, then exec bash -c <script>
	roleFakeCLI = "spike-fakecli" // a provider CLI running a hook or shell tool
	roleProbe   = "spike-probe"   // print the ancestry (for real providers)
)

const (
	prSetPdeathsig      = 1
	prSetChildSubreaper = 36
	roleDeadline        = 10 * time.Second
)

// RunRole runs the role named in args[0] and returns the exit code; ok is
// false when args[0] is not a role.
func RunRole(args []string) (code int, ok bool) {
	if len(args) == 0 || !strings.HasPrefix(args[0], "spike-") {
		return 0, false
	}
	var err error
	switch args[0] {
	case roleClient:
		code, err = runClient(args[1:])
	case roleShim:
		err = runShim(args[1:])
	case roleFakeCLI:
		code, err = runFakeCLI(args[1:])
	case roleProbe:
		err = runProbe(args[1:])
	default:
		err = fmt.Errorf("unknown role %s", args[0])
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", args[0], err)
		return 2, true
	}
	return code, true
}

func prctl(op, arg uintptr) error {
	if _, _, e := syscall.RawSyscall(syscall.SYS_PRCTL, op, arg, 0); e != 0 {
		return e
	}
	return nil
}

// waitOrphaned returns once this process has been reparented: its parent
// is the terminal root ($SPIKE_ROOT, a subreaper) or a process outside the
// root's tree. PR_SET_PDEATHSIG is armed before the check, so a parent that
// dies at any moment is seen (the kernel reparents before signalling).
func waitOrphaned(ctx context.Context) error {
	root, err := strconv.Atoi(os.Getenv("SPIKE_ROOT"))
	if err != nil {
		return fmt.Errorf("SPIKE_ROOT: %w", err)
	}
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGUSR1)
	defer signal.Stop(ch)
	if err := prctl(prSetPdeathsig, uintptr(syscall.SIGUSR1)); err != nil {
		return err
	}
	orphaned := func() bool {
		pp := os.Getppid()
		if pp == root {
			return true
		}
		_, _, err := Walk(SysProc{}, pp, func(s Stat) bool { return s.PID == root })
		return err != nil
	}
	for !orphaned() {
		select {
		case <-ch:
		case <-ctx.Done():
			return fmt.Errorf("still not orphaned: %w", ctx.Err())
		}
	}
	return nil
}

func runClient(args []string) (int, error) {
	fs := flag.NewFlagSet(roleClient, flag.ContinueOnError)
	orphan := fs.Bool("orphan", false, "connect only after the parent dies")
	exitAfterWrite := fs.Bool("exit-after-write", false, "exit right after sending, without reading")
	if err := fs.Parse(args); err != nil {
		return 0, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), roleDeadline)
	defer cancel()
	if *orphan {
		if err := waitOrphaned(ctx); err != nil {
			return 0, err
		}
	}
	var d net.Dialer
	c, err := d.DialContext(ctx, "unix", os.Getenv("SPIKE_SOCK"))
	if err != nil {
		return 0, err
	}
	defer c.Close()
	stop := context.AfterFunc(ctx, func() { c.Close() })
	defer stop()
	b, _ := json.Marshal(request{AgentID: os.Getenv("MESTRE_AGENT_ID"), Label: fs.Arg(0)})
	if _, err := c.Write(append(b, '\n')); err != nil {
		return 0, err
	}
	if *exitAfterWrite {
		return 0, nil
	}
	line, err := bufio.NewReader(c).ReadBytes('\n')
	if err != nil {
		return 0, err
	}
	fmt.Printf("%s: %s", fs.Arg(0), line)
	var r response
	if err := json.Unmarshal(line, &r); err != nil {
		return 0, err
	}
	if !r.OK {
		return 3, nil // not_identified
	}
	return 0, nil
}

// runShim makes the terminal root a child subreaper, then becomes bash.
// The attribute survives execve, so no extra process stays around.
func runShim(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: %s <script>", roleShim)
	}
	if err := prctl(prSetChildSubreaper, 1); err != nil {
		return err
	}
	return syscall.Exec("/bin/bash", []string{"bash", "-c", args[0]}, os.Environ())
}

// runFakeCLI is a multi-threaded process (like a provider CLI) that runs a
// hook or shell-tool command with sh -c, in a new session with -setsid
// (Node's spawn({detached: true}) does that).
func runFakeCLI(args []string) (int, error) {
	fs := flag.NewFlagSet(roleFakeCLI, flag.ContinueOnError)
	setsid := fs.Bool("setsid", false, "run the command in a new session")
	if err := fs.Parse(args); err != nil {
		return 0, err
	}
	cmd := exec.Command("sh", "-c", fs.Arg(0))
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: *setsid}
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return ee.ExitCode(), nil
		}
		return 0, err
	}
	return 0, nil
}

// runProbe appends the ancestry of this process to $SPIKE_PROBE_OUT (or
// stdout): one line per process up to init, with pgid and sid.
func runProbe(args []string) error {
	fs := flag.NewFlagSet(roleProbe, flag.ContinueOnError)
	orphan := fs.Bool("orphan", false, "probe only after the parent dies")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *orphan {
		ctx, cancel := context.WithTimeout(context.Background(), roleDeadline)
		defer cancel()
		if err := waitOrphaned(ctx); err != nil {
			return err
		}
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "== probe %s\n", fs.Arg(0))
	for pid := os.Getpid(); pid > 0; {
		s, err := SysProc{}.Stat(pid)
		if err != nil {
			fmt.Fprintf(&sb, "  %d: %v\n", pid, err)
			break
		}
		fmt.Fprintf(&sb, "  pid=%d ppid=%d pgid=%d sid=%d comm=%s\n", s.PID, s.PPID, s.PGID, s.SID, s.Comm)
		pid = s.PPID
	}
	out := os.Stdout
	if p := os.Getenv("SPIKE_PROBE_OUT"); p != "" {
		f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		defer f.Close()
		out = f
	}
	_, err := out.WriteString(sb.String())
	return err
}
