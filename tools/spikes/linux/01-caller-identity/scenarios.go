//go:build linux

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

// Scenario is one way of calling the CLI from inside a terminal. Script runs
// in the terminal root (bash -c); $SPIKE_BIN is this binary.
type Scenario struct {
	Name   string
	Script string
	Needs  string // executable that must exist in PATH
	// Outside runs the client as a child of the engine, not of the terminal.
	Outside bool
	// EarlyExit holds the identification until the client has exited and
	// been reaped.
	EarlyExit bool
	Setup     func(dir string) error
	// Want is the expected decision per Mode (index = Mode).
	Want [4]bool
}

const client = `"$SPIKE_BIN" spike-client`

var (
	all   = [4]bool{true, true, true, true}
	none  = [4]bool{}
	notTr = [4]bool{false, true, true, true}  // orphan in the same session
	onlyB = [4]bool{false, false, true, true} // orphan in a new session
)

// Scenarios is the matrix of the spike.
var Scenarios = []Scenario{
	{Name: "direct", Script: client + ` direct`, Want: all},
	{Name: "sh-c", Script: `sh -c '` + client + ` sh-c; true'`, Want: all},
	{Name: "bash-c-nested", Script: `bash -c 'sh -c "\"\$SPIKE_BIN\" spike-client bash-c-nested; true"; true'`, Want: all},
	{Name: "script", Script: `"$SPIKE_DIR/tool.sh"`, Want: all, Setup: func(dir string) error {
		return os.WriteFile(filepath.Join(dir, "tool.sh"), []byte("#!/bin/sh\n"+client+" script\nexit $?\n"), 0o755)
	}},
	{Name: "nohup-wait", Script: `nohup ` + client + ` nohup-wait >/dev/null 2>&1 & wait`, Want: all},
	{Name: "nohup-orphan", Script: `sh -c 'nohup ` + client + ` -orphan nohup-orphan >/dev/null 2>&1 &'`, Want: notTr},
	{Name: "setsid", Script: `setsid ` + client + ` setsid; true`, Want: all},
	{Name: "setsid-fork", Script: `setsid -f ` + client + ` -orphan setsid-fork`, Want: onlyB},
	{Name: "double-fork", Script: `( ` + client + ` -orphan double-fork & )`, Want: notTr},
	{Name: "hook", Script: `"$SPIKE_BIN" spike-fakecli '` + client + ` hook; true'`, Want: all},
	{Name: "hook-setsid", Script: `"$SPIKE_BIN" spike-fakecli -setsid '` + client + ` hook-setsid; true'`, Want: all},
	{Name: "detached-bg", Script: `"$SPIKE_BIN" spike-fakecli -setsid '( ` + client + ` -orphan detached-bg & )'`, Want: onlyB},
	{Name: "tmux", Needs: "tmux", Script: `tmux -f /dev/null -S "$SPIKE_DIR/t.sock" new-session -d '` + client + ` tmux'`, Want: onlyB},
	{Name: "impersonate", Script: `MESTRE_AGENT_ID=agent-B ` + client + ` impersonate`, Want: none},
	{Name: "outsider", Script: `:`, Outside: true, Want: none},
	{Name: "early-exit", Script: client + ` -exit-after-write early-exit; echo x > "$SPIKE_DIR/gate"`, EarlyExit: true, Want: none},
}

// Run runs one scenario in a fresh engine and terminal.
func (sc Scenario) Run(ctx context.Context, mode Mode, cgroupBase string) (Result, error) {
	dir, err := os.MkdirTemp("", "spk-") // short: socket paths max 108 bytes
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(dir)
	if sc.Setup != nil {
		if err := sc.Setup(dir); err != nil {
			return Result{}, err
		}
	}
	var before func()
	if sc.EarlyExit {
		gate := filepath.Join(dir, "gate")
		if err := syscall.Mkfifo(gate, 0o600); err != nil {
			return Result{}, err
		}
		before = func() {
			f, err := os.OpenFile(gate, os.O_RDWR, 0)
			if err != nil {
				return
			}
			defer f.Close()
			stop := context.AfterFunc(ctx, func() { f.Close() })
			defer stop()
			_, _ = f.Read(make([]byte, 1))
		}
	}
	e, err := NewEngine(mode, filepath.Join(dir, "s.sock"), before)
	if err != nil {
		return Result{}, err
	}
	defer e.Close()
	r, err := e.Launch(Spec{ID: sc.Name, AgentID: "agent-A", Script: sc.Script, Dir: dir, CgroupBase: cgroupBase})
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = r.Stop(ctx) }()
	if sc.Outside {
		self, err := os.Executable()
		if err != nil {
			return Result{}, err
		}
		cmd := exec.CommandContext(ctx, self, roleClient, sc.Name)
		cmd.Env = append(cleanEnv(), "SPIKE_SOCK="+e.Sock, "MESTRE_AGENT_ID=agent-A")
		if err := cmd.Start(); err != nil {
			return Result{}, err
		}
		defer func() { _ = cmd.Wait() }()
	}
	select {
	case res := <-e.Results:
		return res, nil
	case <-ctx.Done():
		log, _ := os.ReadFile(filepath.Join(dir, sc.Name+".log"))
		return Result{}, fmt.Errorf("%s/%s: no connection: %w\n%s", mode, sc.Name, ctx.Err(), log)
	}
}

// EscapeeAfterKill starts a terminal whose child leaves the session
// (setsid -f) and stays alive, then ends the terminal as terminal.md says:
// SIGKILL to the root group and to every process of the root session (in
// ModeCgroup, cgroup.kill instead). It reports whether the escapee is still
// alive afterwards, and kills it if so.
func EscapeeAfterKill(ctx context.Context, mode Mode, cgroupBase string) (bool, error) {
	dir, err := os.MkdirTemp("", "spk-")
	if err != nil {
		return false, err
	}
	defer os.RemoveAll(dir)
	fifo := filepath.Join(dir, "esc")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		return false, err
	}
	e, err := NewEngine(mode, filepath.Join(dir, "s.sock"), nil)
	if err != nil {
		return false, err
	}
	defer e.Close()
	r, err := e.Launch(Spec{ID: "escapee", AgentID: "agent-A", Dir: dir, CgroupBase: cgroupBase,
		Script: `setsid -f sh -c 'echo $$ > "$SPIKE_DIR/esc"; exec tail -f /dev/null'`})
	if err != nil {
		return false, err
	}
	f, err := os.OpenFile(fifo, os.O_RDWR, 0)
	if err != nil {
		return false, err
	}
	stop := context.AfterFunc(ctx, func() { f.Close() })
	buf := make([]byte, 32)
	n, rerr := f.Read(buf)
	stop()
	f.Close()
	if rerr != nil {
		return false, rerr
	}
	var esc int
	if _, err := fmt.Sscan(string(buf[:n]), &esc); err != nil {
		return false, err
	}
	if mode != ModeCgroup {
		_ = syscall.Kill(-r.PID, syscall.SIGKILL)
		ents, _ := os.ReadDir("/proc")
		for _, d := range ents {
			var pid int
			if _, err := fmt.Sscan(d.Name(), &pid); err != nil {
				continue
			}
			if s, err := (SysProc{}).Stat(pid); err == nil && s.SID == r.PID {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	}
	if err := r.Stop(ctx); err != nil { // ModeCgroup: cgroup.kill and wait for empty
		return false, err
	}
	s, err := (SysProc{}).Stat(esc)
	alive := err == nil && s.State != 'Z'
	if alive {
		_ = syscall.Kill(esc, syscall.SIGKILL)
	}
	return alive, nil
}
