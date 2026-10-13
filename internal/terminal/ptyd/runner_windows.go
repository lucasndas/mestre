package ptyd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"
	"unsafe"

	"github.com/aymanbagabas/go-pty"
	"golang.org/x/sys/windows"
)

// sysAgent is the Windows part of an agent: the Job Object of the terminal.
// The mestre-pty creates it with KILL_ON_JOB_CLOSE and keeps the only
// handle, so the tree dies with the mestre-pty. The agent starts suspended,
// is put in the Job and only then resumes, so no descendant is born outside
// it (docs/arquitetura/windows.md#conpty).
type sysAgent struct {
	job windows.Handle
}

var ntResumeProcess = windows.NewLazySystemDLL("ntdll.dll").NewProc("NtResumeProcess")

func prepare(cmd *pty.Cmd) (*sysAgent, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("create job object: %w", err)
	}
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	// No JOB_OBJECT_LIMIT_BREAKAWAY_OK: nothing leaves the Job.
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		_ = windows.CloseHandle(job)
		return nil, fmt.Errorf("set job limits: %w", err)
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_SUSPENDED}
	return &sysAgent{job: job}, nil
}

func (s *sysAgent) abort() { _ = windows.CloseHandle(s.job) }

// started puts the suspended agent in the Job and resumes it.
func (s *sysAgent) started(cmd *pty.Cmd, _ pty.Pty) error {
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_SUSPEND_RESUME,
		false, uint32(cmd.Process.Pid))
	if err != nil {
		return fmt.Errorf("open agent: %w", err)
	}
	defer windows.CloseHandle(h)
	if err := windows.AssignProcessToJobObject(s.job, h); err != nil {
		return fmt.Errorf("assign agent to the job: %w", err)
	}
	if r, _, _ := ntResumeProcess.Call(uintptr(h)); r != 0 {
		return fmt.Errorf("resume agent: NTSTATUS 0x%x", r)
	}
	return nil
}

// release keeps the Job: closing the handle would end the tree, and the
// handle goes away with the mestre-pty.
func (*sysAgent) release() {}

// signal is ClosePseudoConsole: the console host ends the clients attached
// to it.
func (*sysAgent) signal(a *ptyAgent) error { return a.closePTY() }

// kill is TerminateJobObject, mandatory: a process with its own console and
// the conhost.exe of that console survive ClosePseudoConsole.
func (s *sysAgent) kill(*ptyAgent) error {
	if err := windows.TerminateJobObject(s.job, 1); err != nil {
		return fmt.Errorf("terminate job: %w", err)
	}
	return nil
}

func endOfOutput(err error) error {
	if errors.Is(err, os.ErrClosed) || errors.Is(err, windows.ERROR_BROKEN_PIPE) {
		return io.EOF
	}
	return err
}

func exitInfo(ps *os.ProcessState) ExitInfo { return ExitInfo{Code: ps.ExitCode()} }
