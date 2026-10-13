//go:build windows

package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// cmdProbe is the caller: it connects to the identity pipe, sends its label
// and prints what the service decided. Options shape the process tree first.
func cmdProbe(args []string) error {
	fs := flag.NewFlagSet("probe", flag.ExitOnError)
	label := fs.String("label", "probe", "case name")
	waitParent := fs.Bool("wait-parent", false, "wait (up to 15 s) for the parent to exit before calling: an orphan")
	breakaway := fs.Bool("breakaway", false, "start a child probe (<label>-child) with CREATE_BREAKAWAY_FROM_JOB")
	orphanChild := fs.Bool("orphan-child", false, "with -breakaway: the child waits for this process to exit")
	nestJob := fs.Bool("nestjob", false, "put this process in a new job of its own first")
	_ = fs.Parse(args)

	var notes []string
	if *nestJob {
		j, err := windows.CreateJobObject(nil, nil)
		if err == nil {
			err = windows.AssignProcessToJobObject(j, windows.CurrentProcess())
		}
		notes = append(notes, fmt.Sprintf("own nested job: err=%v", err))
	}
	if *waitParent {
		notes = append(notes, waitForParent(15*time.Second))
	}
	if *breakaway {
		notes = append(notes, spawnBreakaway(*label+"-child", *orphanChild))
	}
	msg := probeMsg{Label: *label, Note: strings.Join(notes, "; "), SelfPID: windows.GetCurrentProcessId()}
	var id identity
	f, err := openPipe(identityPipe, 10*time.Second)
	if err != nil {
		return err
	}
	b, _ := json.Marshal(msg)
	if _, err := f.Write(append(b, '\n')); err != nil {
		return err
	}
	line, err := bufio.NewReader(f).ReadBytes('\n')
	f.Close()
	if err != nil {
		return fmt.Errorf("read answer: %w", err)
	}
	_ = json.Unmarshal(line, &id)
	fmt.Printf("probe %s pid %d: tree_toolhelp=%q tree_ntqsi=%q job=%q pidlist=%q\n", *label, msg.SelfPID,
		id.TreeToolhelp.Terminal, id.TreeNtqsi.Terminal, id.JobInJob.Terminal, id.JobPIDList.Terminal)
	return nil
}

func waitForParent(d time.Duration) string {
	ppid := parentPID()
	h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, ppid)
	if err != nil {
		return fmt.Sprintf("parent %d not open: %v", ppid, err)
	}
	defer windows.CloseHandle(h)
	start := time.Now()
	ev, _ := windows.WaitForSingleObject(h, uint32(d.Milliseconds()))
	return fmt.Sprintf("parent %d exited=%v after %v (deadline %v)", ppid, ev == windows.WAIT_OBJECT_0, time.Since(start).Round(time.Millisecond), d)
}

// spawnBreakaway starts a child probe outside the job. Unless orphan, it
// waits (up to 30 s) for the child before calling, so the tree is intact.
func spawnBreakaway(label string, orphan bool) string {
	exe, _ := os.Executable()
	argv := []string{exe, "probe", "-label", label}
	if orphan {
		argv = append(argv, "-wait-parent")
	}
	si := windows.StartupInfo{Cb: uint32(unsafe.Sizeof(windows.StartupInfo{}))}
	var pi windows.ProcessInformation
	err := windows.CreateProcess(nil, wstr(windows.ComposeCommandLine(argv)), nil, nil, false,
		windows.CREATE_BREAKAWAY_FROM_JOB, nil, nil, &si, &pi)
	if err != nil {
		return fmt.Sprintf("CreateProcess(CREATE_BREAKAWAY_FROM_JOB): %v", err)
	}
	defer windows.CloseHandle(pi.Process)
	defer windows.CloseHandle(pi.Thread)
	in, _ := isProcessInJob(pi.Process, 0) // 0: any job
	note := fmt.Sprintf("CreateProcess(CREATE_BREAKAWAY_FROM_JOB) ok: child %d in some job=%v", pi.ProcessId, in)
	if !orphan {
		start := time.Now()
		ev, _ := windows.WaitForSingleObject(pi.Process, 30000)
		note += fmt.Sprintf(", child exited=%v after %v (deadline 30s)", ev == windows.WAIT_OBJECT_0, time.Since(start).Round(time.Millisecond))
	}
	return note
}
