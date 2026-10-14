package main

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// shellCommand runs line with cmd /s /c "<line>", the form that keeps the
// quoted paths of line intact.
func shellCommand(ctx context.Context, line string) *exec.Cmd {
	c := exec.CommandContext(ctx, "cmd")
	c.SysProcAttr = &syscall.SysProcAttr{CmdLine: `cmd /s /c "` + line + `"`}
	return c
}

var ntResumeProcess = windows.NewLazySystemDLL("ntdll.dll").NewProc("NtResumeProcess")

// hookTree is the Job Object of the hook: cmd starts suspended, goes into
// the Job and only then runs, so no descendant is born outside it. The time
// limit terminates the Job, and closing it (KILL_ON_JOB_CLOSE) ends what the
// hook left behind.
type hookTree struct {
	job windows.Handle

	mu     sync.Mutex
	killed bool // the time limit came before cmd was in the Job
}

func newHookTree(c *exec.Cmd) (*hookTree, error) {
	// mestre:allow nil-sa: an anonymous Job held only by this call; it bounds the tree of the hook and nobody else opens it
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("create the job of the hook: %w", err)
	}
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		_ = windows.CloseHandle(job)
		return nil, fmt.Errorf("set the limits of the job of the hook: %w", err)
	}
	t := &hookTree{job: job}
	c.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED
	c.Cancel = func() error {
		t.kill()
		return nil
	}
	return t, nil
}

// started puts the suspended cmd in the Job and resumes it; a time limit
// that came first leaves it suspended, to be killed.
func (t *hookTree) started(c *exec.Cmd) error {
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_SUSPEND_RESUME,
		false, uint32(c.Process.Pid))
	if err != nil {
		return fmt.Errorf("open the hook: %w", err)
	}
	defer func() { _ = windows.CloseHandle(h) }()
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.killed {
		return errors.New("the time limit of the hook ended before it ran")
	}
	if err := windows.AssignProcessToJobObject(t.job, h); err != nil {
		return fmt.Errorf("put the hook in its job: %w", err)
	}
	if r, _, _ := ntResumeProcess.Call(uintptr(h)); r != 0 {
		return fmt.Errorf("resume the hook: NTSTATUS 0x%x", r)
	}
	return nil
}

func (t *hookTree) kill() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.killed = true
	_ = windows.TerminateJobObject(t.job, 1)
}

func (t *hookTree) close() { _ = windows.CloseHandle(t.job) }
